package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
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

	// recupera token da instância
	token, _ := app.lookupInstanceToken(r.Context(), instance)

	// encaminha para o backend do Agente IA
	agentURL := strings.TrimRight(os.Getenv("AGENT_BACKEND_URL"), "/")
	if agentURL == "" {
		agentURL = "https://paclead-agente-backend-production.up.railway.app/webhook/uazapi"
	}
	// se vier sem caminho, garante o /webhook/uazapi
	if !strings.Contains(agentURL, "/webhook/") {
		agentURL = agentURL + "/webhook/uazapi"
	}

	req, err := http.NewRequest("POST", agentURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("forward build err: %v", err)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Instance-ID", instance)
	if token != "" {
		req.Header.Set("X-Instance-Token", token)
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

// lookupInstanceToken busca o token para uma instância armazenada na plataforma
func (app *App) lookupInstanceToken(ctx context.Context, instance string) (string, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return "", nil
	}
	var token string
	// Ajuste o schema/nome da tabela/colunas conforme seu banco
	err := app.DB.QueryRow(ctx,
		`SELECT token FROM public.wa_instances WHERE instance_id = $1 LIMIT 1`,
		instance,
	).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}
