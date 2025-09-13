// handlers_agent_config.go
package main

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "strconv"
    "strings"
    "time"

    "github.com/go-chi/chi/v5"
)

type AgentSettings struct {
    OrgID              int64     `json:"org_id"`
    FlowID             int64     `json:"flow_id"`
    Name               string    `json:"name"`
    CommunicationStyle string    `json:"communicationStyle"`
    Sector             string    `json:"sector"`
    ProfileType        string    `json:"profileType"`
    ProfileCustom      string    `json:"profileCustom"`
    BasePrompt         string    `json:"basePrompt"`
    TaxID              string    `json:"tax_id"`
    UpdatedAt          time.Time `json:"updated_at"`
}

func (a *App) mountAgentConfig(r chi.Router) {
    r.Route("/agent", func(r chi.Router) {
        r.Get("/settings", a.getAgentSettings)
        r.Put("/settings", a.putAgentSettings)
    })
    // >>> Compatibilidade com rota antiga:
    r.Get("/agent-config", a.getAgentSettings)
    r.Put("/agent-config", a.putAgentSettings)
}

func (a *App) getAgentSettings(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    orgID, flowID := parseTenant(r)
    ctx := r.Context()

    var s AgentSettings
    err := a.DB.QueryRow(ctx, `
        SELECT org_id, flow_id,
               COALESCE(name, ''),
               COALESCE(communication_style, ''),
               COALESCE(sector, ''),
               COALESCE(profile_type, ''),
               COALESCE(profile_custom, ''),
               COALESCE(base_prompt, ''),
               COALESCE(tax_id, ''),
               updated_at
          FROM agent_settings
         WHERE org_id=$1 AND flow_id=$2
    `, orgID, flowID).Scan(
        &s.OrgID, &s.FlowID, &s.Name, &s.CommunicationStyle, &s.Sector,
        &s.ProfileType, &s.ProfileCustom, &s.BasePrompt, &s.TaxID, &s.UpdatedAt,
    )
    if err != nil {
        // Retorna payload “vazio” se não existir ainda (sem 404 para facilitar consumo)
        s = AgentSettings{OrgID: orgID, FlowID: flowID}
    }

    _ = json.NewEncoder(w).Encode(s)
}

func (a *App) putAgentSettings(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    orgID, flowID := parseTenant(r)

    var in AgentSettings
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, "bad json", http.StatusBadRequest)
        return
    }

    // Normalizações
    in.OrgID = orgID
    in.FlowID = flowID
    in.Name = strings.TrimSpace(in.Name)
    in.CommunicationStyle = strings.TrimSpace(in.CommunicationStyle)
    in.Sector = strings.TrimSpace(in.Sector)
    in.ProfileType = strings.TrimSpace(in.ProfileType)
    in.ProfileCustom = strings.TrimSpace(in.ProfileCustom)
    in.BasePrompt = strings.TrimSpace(in.BasePrompt)
    in.TaxID = onlyDigits(in.TaxID)

    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    // UPSERT
    _, err := a.DB.Exec(ctx, `
        INSERT INTO agent_settings
            (org_id, flow_id, name, communication_style, sector, profile_type, profile_custom, base_prompt, tax_id, updated_at)
        VALUES
            ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
        ON CONFLICT (org_id, flow_id)
        DO UPDATE SET
            name=EXCLUDED.name,
            communication_style=EXCLUDED.communication_style,
            sector=EXCLUDED.sector,
            profile_type=EXCLUDED.profile_type,
            profile_custom=EXCLUDED.profile_custom,
            base_prompt=EXCLUDED.base_prompt,
            tax_id=EXCLUDED.tax_id,
            updated_at=NOW()
    `,
        in.OrgID, in.FlowID, in.Name, in.CommunicationStyle, in.Sector, in.ProfileType, in.ProfileCustom, in.BasePrompt, in.TaxID,
    )
    if err != nil {
        http.Error(w, "db error", http.StatusInternalServerError)
        return
    }

    in.UpdatedAt = time.Now().UTC()
    _ = json.NewEncoder(w).Encode(in)
}

func parseTenant(r *http.Request) (int64, int64) {
    // Headers têm precedência; fallback para querystring (?org_id=, ?flow_id=); por fim, default "1".
    org := strings.TrimSpace(r.Header.Get("X-Org-ID"))
    flow := strings.TrimSpace(r.Header.Get("X-Flow-ID"))
    if org == "" {
        org = strings.TrimSpace(r.URL.Query().Get("org_id"))
    }
    if flow == "" {
        flow = strings.TrimSpace(r.URL.Query().Get("flow_id"))
    }
    if org == "" {
        org = "1"
    }
    if flow == "" {
        flow = "1"
    }
    orgID, _ := strconv.ParseInt(org, 10, 64)
    flowID, _ := strconv.ParseInt(flow, 10, 64)
    if orgID <= 0 {
        orgID = 1
    }
    if flowID <= 0 {
        flowID = 1
    }
    return orgID, flowID
}

// helper de limpeza de dígitos (útil para CPF/CNPJ)
func onlyDigits(s string) string {
    var b strings.Builder
    for _, r := range s {
        if r >= '0' && r <= '9' {
            b.WriteRune(r)
        }
    }
    return b.String()
}

// (opcional) proteção simples para manter import do "errors"
func must[T any](v T, err error) T {
    if err != nil {
        panic(err)
    }
    return v
}

var _ = errors.New // mantém import caso removam o must em versões futuras
