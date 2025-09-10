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

// Tipos auxiliares para integração com a uazapi. Cada struct representa
// os dados esperados nos requests e responses usados pelos handlers abaixo.
type waCreateReq struct {
    Name string `json:"name"`
}

// waCreateOut encapsula a resposta ao criar uma instância: contém o ID,
// o token e, opcionalmente, o payload retornado pela chamada de conexão
// inicial (que pode incluir o QR code ou código de pareamento).
type waCreateOut struct {
    InstanceID string                 `json:"instanceId"`
    Token      string                 `json:"token"`
    Connect    map[string]interface{} `json:"connect,omitempty"`
}

// waWebhookReq define o corpo necessário para registrar um webhook de
// eventos da instância. A API da uazapi aceita os eventos "messages" e
// "connection", podendo ser estendidos no futuro.
type waWebhookReq struct {
    URL    string   `json:"url"`
    Events []string `json:"events"`
    Token  string   `json:"token"`
}

// waSendTextReq define o corpo para envio de mensagem de texto via
// instância do WhatsApp. Todos os campos são obrigatórios.
type waSendTextReq struct {
    Token string `json:"token"`
    To    string `json:"to"`
    Text  string `json:"text"`
}

// uazapiClient encapsula as interações HTTP com o serviço uazapi. Mantém
// a base URL e um http.Client configurado com timeout razoável.
type uazapiClient struct {
    base string
    http *http.Client
}

// newUazapiClient cria um novo cliente, lendo a base da uazapi das
// variáveis de ambiente. Caso UAZAPI_BASE não esteja definido, usa um
// endpoint gratuito por padrão.
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

// post realiza uma requisição POST ao endpoint de caminho dado. Headers
// adicionais podem ser passados pelo mapa. O corpo é codificado em
// JSON. Se vout for não nulo, o resultado é decodificado em vout.
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
        return json.NewDecoder(resp.Body).Decode(vout)
    }
    return nil
}

// get realiza uma requisição GET ao endpoint de caminho dado. Headers
// adicionais podem ser passados pelo mapa. Se vout não é nulo, a
// resposta JSON é decodificada nele.
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
        return json.NewDecoder(resp.Body).Decode(vout)
    }
    return nil
}

// mountWhatsApp registra as rotas de integração com WhatsApp sob /api/wa.
// Estas rotas permitem criar instâncias, consultar status, definir
// webhooks e enviar mensagens.
func (app *App) mountWhatsApp(r chi.Router) {
    r.Route("/wa", func(r chi.Router) {
        r.Post("/instances", app.waCreateInstance)
        r.Get("/instances/{instance}/status", app.waStatus)
        r.Post("/instances/{instance}/webhook", app.waSetWebhook)
        r.Post("/instances/{instance}/send/text", app.waSendText)
    })
}

// waCreateInstance cria uma instância nova na uazapi e inicia o fluxo
// de conexão (gerando QR code ou código de pareamento). Requer o
// UAZAPI_ADMIN_TOKEN definido nas variáveis de ambiente.
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
    // 1) criar instância na uazapi
    createBody := map[string]any{"name": in.Name}
    var outInit map[string]any
    if err := c.post("/instance/init", map[string]string{"admintoken": admin}, createBody, &outInit); err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    // 2) extrair id e token (algumas versões retornam dentro de "instance")
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
    // 3) iniciar conexão (sem phone => QR code). A resposta pode incluir
    // campos como "qrcode", "code" ou "pairingCode".
    var outConnect map[string]any
    if err := c.post("/instance/connect", map[string]string{"token": token}, map[string]any{}, &outConnect); err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    resp := waCreateOut{InstanceID: instanceID, Token: token, Connect: outConnect}
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(resp)
}

// waStatus retorna o estado de conexão da instância. A uazapi fornece
// informações como status (disconnected, connecting, connected) e o QR
// code/paircode enquanto connecting.
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

// waSetWebhook configura a URL de webhook de uma instância. Requer o
// token da instância. Se nenhum evento for especificado, assume
// "messages" e "connection".
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
    if len(in.Events) == 0 {
        in.Events = []string{"messages", "connection"}
    }
    c := newUazapiClient()
    body := map[string]any{"url": in.URL, "events": in.Events}
    var out map[string]any
    if err := c.post("/webhook", map[string]string{"token": in.Token}, body, &out); err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    _ = json.NewEncoder(w).Encode(out)
}

// waSendText envia uma mensagem de texto para um número específico usando
// uma instância de WhatsApp. Exige token, número e texto. O backend da
// uazapi espera o campo "number" no corpo da requisição.
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
    var out map[string]any
    if err := c.post("/send/text", map[string]string{"token": in.Token, "Accept": "application/json"}, body, &out); err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    _ = json.NewEncoder(w).Encode(out)
}
