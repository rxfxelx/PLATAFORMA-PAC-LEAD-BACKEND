// handlers_whatsapp.go
package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type apiResp struct {
	Ok      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Monta rotas de WhatsApp sob /api/wa
func (app *App) mountWhatsApp(r chi.Router) {
	r.Route("/wa", func(r chi.Router) {
		r.Post("/instances", app.waCreateInstance)
		r.Get("/instances/{instance}/status", app.waStatus)
		r.Post("/instances/{instance}/webhook", app.waSetWebhook)
		r.Post("/instances/{instance}/send/text", app.waSendText)
	})
}

func (app *App) waCreateInstance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apiResp{Ok: true, Message: "stub: create instance"})
}

func (app *App) waStatus(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(apiResp{Ok: true, Message: "stub: status"})
}

func (app *App) waSetWebhook(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(apiResp{Ok: true, Message: "stub: set webhook"})
}

func (app *App) waSendText(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(apiResp{Ok: true, Message: "stub: send text"})
}
