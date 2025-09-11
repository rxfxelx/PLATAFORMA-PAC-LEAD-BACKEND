package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

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

	// encaminha para o backend do Agente IA
	agentURL := os.Getenv("AGENT_BACKEND_URL")
	if agentURL == "" {
		agentURL = "https://paclead-agente-backend-production.up.railway.app/webhook/uazapi"
	}

	req, err := http.NewRequest("POST", agentURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("forward build err: %v", err)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Instance-ID", instance)

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
