package main

import (
    "encoding/json"
    "net/http"
    "regexp"

    "github.com/go-chi/chi/v5"
)

// mountResolve registers routes used to resolve an organization (and its
// default flow) by a CPF/CNPJ. This allows external systems like n8n to
// discover the internal org and flow IDs based on a tax identifier.
//
// It mounts the handler on GET /orgs/resolve/{tax_id}. The tax_id may
// include punctuation (dots, dashes, slashes) which will be stripped
// before lookup. If no organization is found for the given tax_id or
// if it has no associated flows, the handler returns a 404.
func (a *App) mountResolve(r chi.Router) {
    r.Get("/orgs/resolve/{tax_id}", a.resolveOrg)
}

// resolveOrg resolves an organization and flow ID by its tax identifier
// (CPF or CNPJ). It returns a JSON object containing org_id and flow_id.
//
// Example response:
//   { "org_id": 1, "flow_id": 1 }
//
// If no matching org is found or if the org has no flows, it returns
// a 404 Not Found.
func (a *App) resolveOrg(w http.ResponseWriter, r *http.Request) {
    raw := chi.URLParam(r, "tax_id")
    // Remove all non-digit characters from the provided tax ID.
    re := regexp.MustCompile(`\D`)
    digits := re.ReplaceAllString(raw, "")
    if digits == "" {
        http.Error(w, "invalid tax_id", http.StatusBadRequest)
        return
    }

    // Look up the organization by its tax_id. If none is found, return 404.
    var orgID int64
    err := a.DB.QueryRow(r.Context(), `SELECT id FROM orgs WHERE tax_id=$1`, digits).Scan(&orgID)
    if err != nil {
        http.Error(w, "org not found", http.StatusNotFound)
        return
    }

    // Fetch the first flow for the organization. If no flows exist, return 404.
    var flowID int64
    err = a.DB.QueryRow(r.Context(), `SELECT id FROM flows WHERE org_id=$1 ORDER BY id LIMIT 1`, orgID).Scan(&flowID)
    if err != nil {
        http.Error(w, "flow not found", http.StatusNotFound)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]int64{
        "org_id":  orgID,
        "flow_id": flowID,
    })
}
