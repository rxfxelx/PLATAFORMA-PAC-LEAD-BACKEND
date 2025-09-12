// wa_instances_api.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// ================================
// Montagem das rotas (usada pelo main.go)
// ================================
func (app *App) mountWhatsApp(r chi.Router) {
	// Garante que as tabelas necessárias existam (idempotente)
	if err := app.ensureWhatsAppTables(context.Background()); err != nil {
		log.Printf("ensureWhatsAppTables: %v", err)
	}

	r.Route("/wa", func(r chi.Router) {
		r.Post("/instances", app.waCreateInstance)

		r.Get("/instances/{instance}/status", app.waInstanceStatus)
		r.Get("/instances/{instance}/qr", app.waInstanceQR)      // fallback
		r.Get("/instances/{instance}/qrcode", app.waInstanceQR)  // fallback (alias)

		r.Post("/instances/{instance}/webhook", app.waSetWebhook)
		r.Post("/instances/{instance}/send/text", app.waSendText)
	})
}

// ================================
// Estruturas/Helpers
// ================================
type waCreateReq struct {
	Name string `json:"name"`
}

type waSendTextReq struct {
	Token string `json:"token"`
	To    string `json:"to"`
	Text  string `json:"text"`
}

type uazClient struct {
	BaseURL    string
	APIKey     string
	AuthHeader string // nome do header, ex.: "Authorization" ou "X-API-KEY"
	AuthValue  string // valor do header; pode conter %s p/ interpolar APIKey (ex.: "Bearer %s")
	HTTP       *http.Client
}

func newUAZClient() *uazClient {
	base := strings.TrimRight(os.Getenv("UAZAPI_BASE"), "/") // ex.: https://sua-uazapi.com
	apiKey := os.Getenv("UAZAPI_TOKEN")                      // token/chave do provedor
	hName := os.Getenv("UAZAPI_AUTH_HEADER")
	if hName == "" {
		hName = "Authorization"
	}
	hVal := os.Getenv("UAZAPI_AUTH_VALUE")
	if hVal == "" {
		hVal = "Bearer %s"
	}
	return &uazClient{
		BaseURL:    base,
		APIKey:     apiKey,
		AuthHeader: hName,
		AuthValue:  hVal,
		HTTP:       &http.Client{Timeout: 35 * time.Second},
	}
}

func (c *uazClient) configured() bool { return c.BaseURL != "" }

// Faz requisição JSON ao provedor uazapi; se body!=nil, envia como JSON.
func (c *uazClient) doJSON(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	if !c.configured() {
		return nil, errors.New("uazapi not configured (defina UAZAPI_BASE)")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Header de autenticação do provedor
	if c.AuthHeader != "" {
		val := c.AuthValue
		if strings.Contains(val, "%s") {
			val = fmt.Sprintf(val, c.APIKey)
		}
		if val == "" {
			val = c.APIKey
		}
		if val != "" {
			req.Header.Set(c.AuthHeader, val)
		}
	}
	return c.HTTP.Do(req)
}

func parseIntHeader(r *http.Request, key string, def int64) int64 {
	v := strings.TrimSpace(r.Header.Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// Extrai string de mapa JSON com múltiplas chaves candidatas
func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return t
				}
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			case json.Number:
				return t.String()
			}
		}
	}
	return ""
}

func randToken(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// ================================
// Tabelas necessárias
// ================================
func (app *App) ensureWhatsAppTables(ctx context.Context) error {
	// wa_instances
	_, err := app.DB.Exec(ctx, `
CREATE TABLE IF NOT EXISTS public.wa_instances (
  instance_id TEXT PRIMARY KEY,
  token       TEXT NOT NULL,
  org_id      BIGINT NOT NULL DEFAULT 1,
  flow_id     BIGINT NOT NULL DEFAULT 1,
  webhook_url TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	if err != nil {
		return err
	}
	// webhooks_log (usada pelo webhook_wa.go)
	_, err = app.DB.Exec(ctx, `
CREATE TABLE IF NOT EXISTS public.webhooks_log (
  id         BIGSERIAL PRIMARY KEY,
  source     TEXT,
  payload    JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	return err
}

// Upsert da instância no banco
func (app *App) upsertWAInstance(ctx context.Context, instanceID, token string, orgID, flowID int64, webhookURL string) error {
	_, err := app.DB.Exec(ctx, `
INSERT INTO public.wa_instances (instance_id, token, org_id, flow_id, webhook_url)
VALUES ($1, $2, $3, $4, NULLIF($5,''))
ON CONFLICT (instance_id) DO UPDATE
SET
  token       = EXCLUDED.token,
  org_id      = EXCLUDED.org_id,
  flow_id     = EXCLUDED.flow_id,
  webhook_url = COALESCE(EXCLUDED.webhook_url, public.wa_instances.webhook_url),
  updated_at  = NOW()
`, instanceID, token, orgID, flowID, webhookURL)
	return err
}

// ================================
// Handlers
// ================================

// POST /api/wa/instances
func (app *App) waCreateInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = app.ensureWhatsAppTables(ctx)

	var in waCreateReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		http.Error(w, "invalid body: expected {\"name\":\"...\"}", http.StatusBadRequest)
		return
	}
	orgID := parseIntHeader(r, "X-Org-ID", 1)
	flowID := parseIntHeader(r, "X-Flow-ID", 1)

	uaz := newUAZClient()

	// Caso não exista configuração de UAZAPI, retornamos um "mock" funcional para o front (modo demo).
	if !uaz.configured() {
		inst := strings.ToLower(strings.ReplaceAll(in.Name, " ", "-")) + "-" + randToken(6)
		tok := randToken(32)

		// persiste/atualiza
		_ = app.upsertWAInstance(ctx, inst, tok, orgID, flowID, "")

		out := map[string]any{
			"instanceId": inst,
			"token":      tok,
			"connect": map[string]any{
				"status":  "waiting-qr",
				"qrcode":  "UAZAPI_MOCK_" + inst,
				"message": "UAZAPI_BASE não configurado; retornando modo mock.",
			},
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}

	// Provedor real: tentamos caminho padrão "/instances"
	resp, err := uaz.doJSON(ctx, http.MethodPost, "/instances", nil, map[string]any{
		"name": in.Name,
	})
	if err != nil {
		http.Error(w, "provider error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Passamos a resposta do provedor, mas também garantimos instanceId/token persistidos
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	instanceID := pickStr(raw, "instanceId", "instance", "name", "id")
	if instanceID == "" {
		// fallback: geramos um nome
		instanceID = strings.ToLower(strings.ReplaceAll(in.Name, " ", "-")) + "-" + randToken(4)
	}
	token := pickStr(raw, "token", "instanceToken", "instance_token")

	// persiste/atualiza
	if token != "" {
		if err := app.upsertWAInstance(ctx, instanceID, token, orgID, flowID, ""); err != nil {
			log.Printf("upsert wa_instances: %v", err)
		}
	}

	// devolve o que o provedor retornou + normalizações úteis ao front
	if raw == nil {
		raw = map[string]any{}
	}
	raw["instanceId"] = instanceID
	if token != "" {
		raw["token"] = token
	}
	writeJSON(w, http.StatusCreated, raw)
}

// GET /api/wa/instances/{instance}/status?token=...
func (app *App) waInstanceStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	instance := chi.URLParam(r, "instance")
	if strings.TrimSpace(instance) == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))

	uaz := newUAZClient()
	// Sem provedor: modo mock
	if !uaz.configured() {
		out := map[string]any{
			"instance": instance,
			"status":   "waiting-qr",
			"qrcode":   "UAZAPI_MOCK_" + instance,
			"connect": map[string]any{
				"status": "waiting-qr",
			},
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	q := url.Values{}
	if token != "" {
		q.Set("token", token)
	}
	resp, err := uaz.doJSON(ctx, http.MethodGet, "/instances/"+url.PathEscape(instance)+"/status", q, nil)
	if err != nil {
		http.Error(w, "provider error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&data)
	if data == nil {
		data = map[string]any{}
	}
	// Normalizações amigáveis ao front (sem apagar campos originais)
	if _, ok := data["instance"]; !ok {
		data["instance"] = instance
	}
	if _, ok := data["status"]; !ok {
		// tenta deduzir
		if c, ok := data["connect"].(map[string]any); ok {
			if s := pickStr(c, "status", "state"); s != "" {
				data["status"] = s
			}
		} else if s := pickStr(data, "state"); s != "" {
			data["status"] = s
		}
	}
	writeJSON(w, http.StatusOK, data)
}

// GET /api/wa/instances/{instance}/qr  (ou /qrcode)
func (app *App) waInstanceQR(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	instance := chi.URLParam(r, "instance")
	if strings.TrimSpace(instance) == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))

	uaz := newUAZClient()
	if !uaz.configured() {
		out := map[string]any{
			"instance": instance,
			"qrcode":   "UAZAPI_MOCK_" + instance,
			"status":   "waiting-qr",
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	q := url.Values{}
	if token != "" {
		q.Set("token", token)
	}
	// Tentamos endpoint /qr e /qrcode
	paths := []string{
		"/instances/" + url.PathEscape(instance) + "/qr",
		"/instances/" + url.PathEscape(instance) + "/qrcode",
	}
	var lastBody []byte
	for _, p := range paths {
		resp, err := uaz.doJSON(ctx, http.MethodGet, p, q, nil)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && len(b) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
			return
		}
		lastBody = b
	}
	// fallback
	if len(lastBody) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(lastBody)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instance, "status": "waiting-qr"})
}

// POST /api/wa/instances/{instance}/webhook
func (app *App) waSetWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	instance := chi.URLParam(r, "instance")
	if strings.TrimSpace(instance) == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	webhookURL := strings.TrimSpace(fmt.Sprint(body["url"]))
	token := strings.TrimSpace(fmt.Sprint(body["token"]))

	// Atualiza DB (salva URL do webhook)
	_ = app.upsertWAInstance(ctx, instance, token, parseIntHeader(r, "X-Org-ID", 1), parseIntHeader(r, "X-Flow-ID", 1), webhookURL)

	uaz := newUAZClient()
	if !uaz.configured() {
		// Modo demo: registra localmente e responde ok
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "webhook salvo (mock)"})
		return
	}
	// Proxy p/ provedor
	resp, err := uaz.doJSON(ctx, http.MethodPost, "/instances/"+url.PathEscape(instance)+"/webhook", nil, body)
	if err != nil {
		http.Error(w, "provider error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out == nil {
		out = map[string]any{"ok": resp.StatusCode >= 200 && resp.StatusCode < 300}
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/wa/instances/{instance}/send/text
func (app *App) waSendText(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	instance := chi.URLParam(r, "instance")
	if strings.TrimSpace(instance) == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}
	var in waSendTextReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.To) == "" || strings.TrimSpace(in.Text) == "" {
		http.Error(w, "missing to/text", http.StatusBadRequest)
		return
	}

	uaz := newUAZClient()
	if !uaz.configured() {
		// Modo demo: tudo certo
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"mock":    true,
			"message": "Mensagem simulada (UAZAPI_BASE não configurado)",
		})
		return
	}

	// Proxy p/ provedor
	reqBody := map[string]any{
		"token": in.Token,
		"to":    in.To,
		"text":  in.Text,
	}
	resp, err := uaz.doJSON(ctx, http.MethodPost, "/instances/"+url.PathEscape(instance)+"/send/text", nil, reqBody)
	if err != nil {
		http.Error(w, "provider error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Se o provedor responder erro, propagamos um 503 amigável (o front trata "disconnected")
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = "disconnected or provider error"
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out == nil {
		out = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, out)
}

// ================================
// Util de resposta JSON
// ================================
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
