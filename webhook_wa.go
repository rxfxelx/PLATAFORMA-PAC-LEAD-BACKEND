package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
)

// webhook que a Uazapi vai chamar: POST /api/webhooks/wa/{instance}
func (app *App) webhookWa(w http.ResponseWriter, r *http.Request) {
	instance := chi.URLParam(r, "instance")
	if instance == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}

	// lê payload bruto
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// loga no banco (opcional)
	_, _ = app.DB.Exec(r.Context(),
		`INSERT INTO public.webhooks_log(source, payload) VALUES($1, $2)`,
		"uazapi", json.RawMessage(body))

	// recupera credenciais/tenant da instância
	info, err := app.lookupInstanceInfo(r.Context(), instance)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("lookup instance err: %v", err)
	}

	// base do backend do Agente IA (podendo vir só o domínio)
	agentBase := strings.TrimRight(os.Getenv("AGENT_BACKEND_URL"), "/")
	if agentBase == "" {
		agentBase = "https://paclead-agente-backend-production.up.railway.app"
	}

	// monta URL de destino
	forwardURL := agentBase
	if strings.Contains(agentBase, "/webhook/") || strings.Contains(agentBase, "/webhooks/") {
		// já veio com caminho completo — usa como está
		forwardURL = agentBase
	} else {
		// usa slug multi-tenant: /webhooks/{instance}
		forwardURL = agentBase + "/webhooks/" + url.PathEscape(instance)
	}

	req, err := http.NewRequest("POST", forwardURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("forward build err: %v", err)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Instance-ID", instance)
	if info.Token != "" {
		req.Header.Set("X-Instance-Token", info.Token)
	}
	if info.OrgID != "" {
		req.Header.Set("X-Org-ID", info.OrgID)
	}
	if info.FlowID != "" {
		req.Header.Set("X-Flow-ID", info.FlowID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("forward err: %v", err)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	_ = resp.Body.Close()

	// sempre aceitar para que a Uazapi não reenvie o mesmo lote
	w.WriteHeader(http.StatusAccepted)
}

type instanceInfo struct {
	Token  string
	OrgID  string
	FlowID string
}

// lookupInstanceInfo busca token/org/flow para uma instância armazenada na plataforma
func (app *App) lookupInstanceInfo(ctx context.Context, instance string) (instanceInfo, error) {
	out := instanceInfo{}
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return out, nil
	}
	// Ajuste o schema/nome da tabela/colunas conforme seu banco.
	// Fazemos cast para texto para simplificar o Scan em strings.
	row := app.DB.QueryRow(ctx, `
		SELECT
			COALESCE(token, '')                                   AS token,
			COALESCE(org_id::text, '1')                           AS org_id,
			COALESCE(flow_id::text, '1')                          AS flow_id
		FROM public.wa_instances
		WHERE instance_id = $1
		LIMIT 1
	`, instance)

	if err := row.Scan(&out.Token, &out.OrgID, &out.FlowID); err != nil {
		return instanceInfo{}, err
	}
	return out, nil
}
