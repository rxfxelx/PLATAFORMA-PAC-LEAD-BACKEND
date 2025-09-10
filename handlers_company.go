package main

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
)

// mountCompany registers the company (organisation) management endpoints under
// the provided router. These endpoints allow clients to fetch and update
// organisation details such as the legal name, trade name, tax identifier and
// contact information. The organisation is derived from the authenticated
// user's token claims.
func (a *App) mountCompany(r chi.Router) {
    // Fetch current organisation details. Requires a valid JWT in the
    // Authorization header. Returns 401 if the token is missing or invalid.
    r.Get("/company", a.getCompany)
    // Update organisation details. Accepts a JSON body with the fields
    // defined in the CompanyInput struct. Requires authentication.
    r.Put("/company", a.updateCompany)
}

// Company represents the organisation record returned by getCompany. Most
// fields are pointers so that zero values (e.g. empty strings) are omitted
// when encoding to JSON. Note: additional fields can be added as needed.
type Company struct {
    ID             int64   `json:"id"`
    Name           string  `json:"name"`
    TaxID          string  `json:"tax_id"`
    RazaoSocial    *string `json:"razao_social,omitempty"`
    NomeFantasia   *string `json:"nome_fantasia,omitempty"`
    InscEstadual   *string `json:"inscricao_estadual,omitempty"`
    Segmento       *string `json:"segmento,omitempty"`
    Telefone       *string `json:"telefone,omitempty"`
    Email          *string `json:"email,omitempty"`
    Bairro         *string `json:"bairro,omitempty"`
    Endereco       *string `json:"endereco,omitempty"`
    Numero         *string `json:"numero,omitempty"`
    CEP            *string `json:"cep,omitempty"`
    Cidade         *string `json:"cidade,omitempty"`
    UF             *string `json:"uf,omitempty"`
    Observacoes    *string `json:"observacoes,omitempty"`
}

// getCompany retrieves the organisation associated with the authenticated
// user. It extracts the user and organisation IDs from the JWT claims and
// queries the orgs table for all relevant columns. If the record cannot be
// found a 404 is returned.
func (a *App) getCompany(w http.ResponseWriter, r *http.Request) {
    _, orgID, _, err := extractUserFromToken(r)
    if err != nil {
        http.Error(w, "invalid token", http.StatusUnauthorized)
        return
    }
    // Query all company fields. Some may be nullable; use pointers to scan.
    var c Company
    err = a.DB.QueryRow(r.Context(),
        `SELECT id, name, tax_id, razao_social, nome_fantasia, inscricao_estadual, segmento, telefone, email, bairro, endereco, numero, cep, cidade, uf, observacoes
         FROM orgs
         WHERE id=$1`, orgID).
        Scan(&c.ID, &c.Name, &c.TaxID, &c.RazaoSocial, &c.NomeFantasia, &c.InscEstadual, &c.Segmento,
            &c.Telefone, &c.Email, &c.Bairro, &c.Endereco, &c.Numero, &c.CEP, &c.Cidade, &c.UF, &c.Observacoes)
    if err != nil {
        http.Error(w, err.Error(), http.StatusNotFound)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(c)
}

// CompanyInput defines the payload accepted by updateCompany. It mirrors the
// fields in the Company struct but uses non-pointer strings so empty
// strings will clear the corresponding column. The TaxID is optional here
// because it is immutable in most jurisdictions; however, it can be updated
// if provided.
type CompanyInput struct {
    Name           *string `json:"name"`
    TaxID          *string `json:"tax_id"`
    RazaoSocial    *string `json:"razao_social"`
    NomeFantasia   *string `json:"nome_fantasia"`
    InscEstadual   *string `json:"inscricao_estadual"`
    Segmento       *string `json:"segmento"`
    Telefone       *string `json:"telefone"`
    Email          *string `json:"email"`
    Bairro         *string `json:"bairro"`
    Endereco       *string `json:"endereco"`
    Numero         *string `json:"numero"`
    CEP            *string `json:"cep"`
    Cidade         *string `json:"cidade"`
    UF             *string `json:"uf"`
    Observacoes    *string `json:"observacoes"`
}

// updateCompany persists changes to the organisation associated with the
// authenticated user. It accepts a JSON body and uses COALESCE to
// selectively update only the provided fields. Fields omitted in the
// payload remain unchanged. If the organisation cannot be found a 404 is
// returned.
func (a *App) updateCompany(w http.ResponseWriter, r *http.Request) {
    _, orgID, _, err := extractUserFromToken(r)
    if err != nil {
        http.Error(w, "invalid token", http.StatusUnauthorized)
        return
    }
    var in CompanyInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
        return
    }
    // Build update statement. Use COALESCE to keep existing values when nil.
    _, err = a.DB.Exec(r.Context(),
        `UPDATE orgs
         SET name=COALESCE($1, name),
             tax_id=COALESCE($2, tax_id),
             razao_social=COALESCE($3, razao_social),
             nome_fantasia=COALESCE($4, nome_fantasia),
             inscricao_estadual=COALESCE($5, inscricao_estadual),
             segmento=COALESCE($6, segmento),
             telefone=COALESCE($7, telefone),
             email=COALESCE($8, email),
             bairro=COALESCE($9, bairro),
             endereco=COALESCE($10, endereco),
             numero=COALESCE($11, numero),
             cep=COALESCE($12, cep),
             cidade=COALESCE($13, cidade),
             uf=COALESCE($14, uf),
             observacoes=COALESCE($15, observacoes)
         WHERE id=$16`,
        in.Name, in.TaxID, in.RazaoSocial, in.NomeFantasia, in.InscEstadual, in.Segmento, in.Telefone,
        in.Email, in.Bairro, in.Endereco, in.Numero, in.CEP, in.Cidade, in.UF, in.Observacoes, orgID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusNoContent)
}
