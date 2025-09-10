package main

// Auth: registro, login, refresh e perfil com JWT + bcrypt.
// Cada registro cria org e flow padrão. Tokens carregam user_id/org_id/flow_id.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/jwtauth/v5"
	jwxjwt "github.com/lestrrat-go/jwx/v2/jwt"
	"golang.org/x/crypto/bcrypt"
)

// signer/verifier global
var tokenAuth *jwtauth.JWTAuth

func init() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "secret"
	}
	tokenAuth = jwtauth.New("HS256", []byte(secret), nil)
}

// rotas
func (a *App) mountAuth(r chi.Router) {
	r.Post("/auth/register", a.register)
	r.Post("/auth/login", a.login)
	r.Post("/auth/refresh", a.refresh)
	r.Get("/auth/me", a.me)
}

// POST /auth/register
func (a *App) register(w http.ResponseWriter, r *http.Request) {
    // The registration endpoint now expects an additional tax identifier (CPF or
    // CNPJ). This identifier will be stored alongside the organisation so
    // individual flows and products can be associated with a specific legal
    // entity. We trim spaces and lower‑case the email for consistency. If any
    // required field is missing the request is rejected.
    var in struct {
        Name     string `json:"name"`
        Email    string `json:"email"`
        Password string `json:"password"`
        TaxID    string `json:"tax_id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
        return
    }
    in.Email = strings.TrimSpace(strings.ToLower(in.Email))
    in.Name = strings.TrimSpace(in.Name)
    in.TaxID = strings.TrimSpace(in.TaxID)
    if in.Email == "" || in.Password == "" || in.Name == "" || in.TaxID == "" {
        http.Error(w, "name, email, password and tax_id are required", http.StatusBadRequest)
        return
    }
    // validate TaxID: remove non‑digits and ensure it has either 11 (CPF) or 14 (CNPJ) digits
    digits := strings.Map(func(r rune) rune {
        if r >= '0' && r <= '9' {
            return r
        }
        return -1
    }, in.TaxID)
    if len(digits) != 11 && len(digits) != 14 {
        http.Error(w, "tax_id must be a valid CPF (11 digits) or CNPJ (14 digits)", http.StatusBadRequest)
        return
    }
    // normalise: store only digits
    in.TaxID = digits

	// já existe?
	var exists bool
	if err := a.DB.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email)=LOWER($1))`, in.Email).Scan(&exists); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "user already exists", http.StatusConflict)
		return
	}

	// hash
	hashed, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := context.WithValue(r.Context(), "op", "register")

    // org
    var orgID int64
    // insert organisation with tax_id; assumes the orgs table has a tax_id column.
    if err := a.DB.QueryRow(ctx,
        `INSERT INTO orgs(name, tax_id) VALUES($1, $2) RETURNING id`, in.Name, in.TaxID).Scan(&orgID); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
	// flow
	var flowID int64
	if err := a.DB.QueryRow(ctx,
		`INSERT INTO flows(org_id, name) VALUES($1, 'Fluxo 1') RETURNING id`, orgID).Scan(&flowID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// user
	var userID int64
	if err := a.DB.QueryRow(ctx,
		`INSERT INTO users(org_id, flow_id, name, email, password)
		 VALUES($1,$2,$3,$4,$5) RETURNING id`,
		orgID, flowID, in.Name, in.Email, string(hashed)).Scan(&userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// token
	token, err := generateToken(userID, orgID, flowID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{
        "access_token": token, "token_type": "bearer", "expires_in": 24 * 3600,
        "id": userID, "email": in.Email, "name": in.Name, "org_id": orgID, "flow_id": flowID,
        // include tax_id in the response so clients can persist it if needed
        "tax_id": in.TaxID,
    })
}

// POST /auth/login
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}

    var userID, orgID, flowID int64
    var hashed, name, taxID string
    // join users with orgs to fetch the tax identifier
    if err := a.DB.QueryRow(r.Context(),
        `SELECT u.id, u.org_id, u.flow_id, u.name, u.password, o.tax_id
         FROM users u
         JOIN orgs o ON u.org_id=o.id
         WHERE LOWER(u.email)=LOWER($1)`,
        in.Email).Scan(&userID, &orgID, &flowID, &name, &hashed, &taxID); err != nil {
        http.Error(w, "invalid credentials", http.StatusUnauthorized)
        return
    }
	if bcrypt.CompareHashAndPassword([]byte(hashed), []byte(in.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := generateToken(userID, orgID, flowID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{
        "access_token": token, "token_type": "bearer", "expires_in": 24 * 3600,
        "id": userID, "email": in.Email, "name": name, "org_id": orgID, "flow_id": flowID,
        "tax_id": taxID,
    })
}

// POST /auth/refresh
func (a *App) refresh(w http.ResponseWriter, r *http.Request) {
	uid, org, flow, err := extractUserFromToken(r)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	token, err := generateToken(uid, org, flow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": token, "token_type": "bearer", "expires_in": 24 * 3600,
	})
}

// GET /auth/me
func (a *App) me(w http.ResponseWriter, r *http.Request) {
	uid, org, flow, err := extractUserFromToken(r)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	var email, name string
	if err := a.DB.QueryRow(r.Context(),
		`SELECT email, name FROM users WHERE id=$1`, uid).Scan(&email, &name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": uid, "email": email, "name": name, "org_id": org, "flow_id": flow,
	})
}

// gera JWT
func generateToken(userID, orgID, flowID int64) (string, error) {
	claims := map[string]any{
		"user_id": userID,
		"org_id":  orgID,
		"flow_id": flowID,
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}
	_, tokenString, err := tokenAuth.Encode(claims)
	return tokenString, err
}

// extrai claims do Authorization: Bearer <token>
func extractUserFromToken(r *http.Request) (int64, int64, int64, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return 0, 0, 0, errors.New("no authorization header")
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return 0, 0, 0, errors.New("invalid authorization header")
	}
	raw := parts[1]

	// jwtauth v5 com jwx/v2: Decode -> (jwt.Token, error)
	tok, err := tokenAuth.Decode(raw)
	if err != nil || tok == nil {
		return 0, 0, 0, errors.New("invalid token")
	}
	// valida exp/iat
	if err := jwxjwt.Validate(tok); err != nil {
		return 0, 0, 0, errors.New("expired or invalid token")
	}

	uid := toInt64(getClaim(tok, "user_id"))
	org := toInt64(getClaim(tok, "org_id"))
	flow := toInt64(getClaim(tok, "flow_id"))
	if uid == 0 || org == 0 || flow == 0 {
		return 0, 0, 0, errors.New("missing claims")
	}
	return uid, org, flow, nil
}

func getClaim(tok jwxjwt.Token, key string) any {
	v, _ := tok.Get(key)
	return v
}

// conversão genérica p/ int64
func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int32:
		return int64(t)
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	default:
		return 0
	}
}
