package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// ===== Tipos =====
type waCreateReq struct {
	Name string `json:"name"`
}
type waCreateOut struct {
	InstanceID string                 `json:"instanceId"`
	Token      string                 `json:"token"`
	Connect    map[string]interface{} `json:"connect,omitempty"`
}
type waWebhookReq struct {
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Token   string   `json:"token"`
	Exclude []string `json:"exclude"` // opcional: eventos para ignorar (ex.: wasSentByApi, isGroupYes)
	Listen  []string `json:"listen"`  // opcional: caso Uazapi use listen/include
}
type waSendTextReq struct {
	Token string `json:"token"`
	To    string `json:"to"`
	Text  string `json:"text"`
}

// ===== Cliente Uazapi =====
type uazapiClient struct {
	base string
	http *http.Client
}

func newUazapiClient() *uazapiClient {
	base := os.Getenv("UAZAPI_BASE")
	if base == "" {
		base = "https://free.uazapi.com"
	}
	return &uazapiClient{
		base: base,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// POST genérico (aceita qualquer JSON de resposta)
func (c *uazapiClient) post(path string, headers map[string]string, body any, vout any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest("POST", c.base+path, &buf)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("uazapi %s: http %d: %s", path, resp.StatusCode, string(b))
	}
	if vout != nil {
		dec := json.NewDecoder(resp.Body)
		dec.UseNumber()
		return dec.Decode(vout) // aceita array ou objeto
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *uazapiClient) get(path string, headers map[string]string, vout any) error {
	req, err := http.NewRequest("GET", c.base+path, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("uazapi %s: http %d: %s", path, resp.StatusCode, string(b))
	}
	if vout != nil {
		dec := json.NewDecoder(resp.Body)
		dec.UseNumber()
		return dec.Decode(vout)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ===== Rotas =====
func (app *App) mountWhatsApp(r chi.Router) {
	r.Route("/wa", func(r chi.Router) {
		r.Post("/instances", app.waCreateInstance)
		r.Get("/instances/{instance}/status", app.waStatus)
		r.Post("/instances/{instance}/webhook", app.waSetWebhook)
		r.Post("/instances/{instance}/send/text", app.waSendText)
	})
}

// Cria instância + connect (gera QR/pair code)
func (app *App) waCreateInstance(w http.ResponseWriter, r *http.Request) {
	var in waCreateReq
	_ = json.NewDecoder(r.Body).Decode(&in)
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "inst-" + time.Now().Format("20060102150405")
	}
	admin := os.Getenv("UAZAPI_ADMIN_TOKEN")
	if admin == "" {
		http.Error(w, "missing UAZAPI_ADMIN_TOKEN", http.StatusInternalServerError)
		return
	}
	c := newUazapiClient()

	// init
	createBody := map[string]any{"name": in.Name}
	var outInit map[string]any
	if err := c.post("/instance/init", map[string]string{"admintoken": admin}, createBody, &outInit); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// extrai id/token
	inst := map[string]any{}
	if v, ok := outInit["instance"]; ok {
		if m, ok2 := v.(map[string]any); ok2 {
			inst = m
		}
	}
	instanceID, _ := inst["id"].(string)
	token, _ := inst["token"].(string)
	if instanceID == "" || token == "" {
		if v, ok := outInit["id"].(string); ok {
			instanceID = v
		}
		if v, ok := outInit["token"].(string); ok {
			token = v
		}
	}
	if strings.TrimSpace(token) == "" {
		http.Error(w, "uazapi init: token vazio na resposta", http.StatusBadGateway)
		return
	}

	// connect (sem phone => QR)
	var outConnect map[string]any
	if err := c.post("/instance/connect", map[string]string{"token": token}, map[string]any{}, &outConnect); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	resp := waCreateOut{InstanceID: instanceID, Token: token, Connect: outConnect}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Status da instância
func (app *App) waStatus(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if strings.TrimSpace(token) == "" {
		http.Error(w, "token query param required", http.StatusBadRequest)
		return
	}
	c := newUazapiClient()
	var out map[string]any
	if err := c.get("/instance/status", map[string]string{"token": token}, &out); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// Define Webhook (aceita array ou objeto, e envia excludes)
func (app *App) waSetWebhook(w http.ResponseWriter, r *http.Request) {
	var in waWebhookReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.URL) == "" || strings.TrimSpace(in.Token) == "" {
		http.Error(w, "url and token are required", http.StatusBadRequest)
		return
	}
	// defaults de eventos e exclusões
	if len(in.Events) == 0 {
		in.Events = []string{"messages", "connection"}
	}
	if len(in.Exclude) == 0 {
		in.Exclude = []string{"wasSentByApi", "isGroupYes"}
	}

	c := newUazapiClient()

	// Algumas instalações da Uazapi usam chaves diferentes. Enviamos todas:
	body := map[string]any{
		"url":           in.URL,
		"events":        in.Events,   // padrão
		"listen":        in.Listen,   // caso seja exigido
		"exclude":       in.Exclude,  // alguns usam "exclude"
		"excludeEvents": in.Exclude,  // outros usam "excludeEvents"
		"ignore":        in.Exclude,  // e outros "ignore"
	}

	var out any // <- aceita array ou objeto
	if err := c.post("/webhook", map[string]string{"token": in.Token}, body, &out); err != nil {
		http.Error(w, "uazapi /webhook: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// Envia texto
func (app *App) waSendText(w http.ResponseWriter, r *http.Request) {
	var in waSendTextReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Token) == "" || strings.TrimSpace(in.To) == "" || strings.TrimSpace(in.Text) == "" {
		http.Error(w, "token, to and text are required", http.StatusBadRequest)
		return
	}
	c := newUazapiClient()
	body := map[string]any{"number": in.To, "text": in.Text}

	var out any
	if err := c.post("/send/text", map[string]string{"token": in.Token, "Accept": "application/json"}, body, &out); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
