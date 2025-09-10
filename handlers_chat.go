package main

import (
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "sync"
    "time"

    "github.com/go-chi/chi/v5"
    openai "github.com/sashabaranov/go-openai"
)

// ================================================================
//  Estado em memória para produtos pendentes
// ================================================================

// productSuggest representa os dados sugeridos pela IA para um produto.
// Os campos correspondem ao JSON esperado de resposta.
type productSuggest struct {
    Title       string   `json:"title"`
    Description string   `json:"description"`
    Category    string   `json:"category"`
    Tags        []string `json:"tags"`
}

// pendingProduct guarda uma sugestão de produto que aguarda o preço do usuário.
type pendingProduct struct {
    OrgID     int
    FlowID    int
    ImagePath string // caminho local onde o arquivo foi salvo
    ImageURL  string // URL pública (/uploads/...) para exibir no chat
    Suggest   productSuggest
}

// pendBySession armazena os produtos pendentes indexados por sessionId. É
// protegido por pendMu para acesso concorrente.
var (
    pendMu       sync.Mutex
    pendBySession = make(map[string]*pendingProduct)
)

func setPending(session string, p *pendingProduct) {
    pendMu.Lock()
    defer pendMu.Unlock()
    if session == "" {
        return
    }
    pendBySession[session] = p
}

func getPending(session string) (*pendingProduct, bool) {
    pendMu.Lock()
    defer pendMu.Unlock()
    p, ok := pendBySession[session]
    return p, ok
}

func clearPending(session string) {
    pendMu.Lock()
    defer pendMu.Unlock()
    delete(pendBySession, session)
}

// ================================================================
//  Rotas de chat
// ================================================================

// mountChat registra as rotas de chat e de upload de visão. O endpoint
// vision/upload agora cria pendências para produtos. O endpoint chat
// trata preços pendentes e conversa normal.
func (a *App) mountChat(r chi.Router) {
    r.Post("/chat", a.chatHandler)
    r.Post("/vision/upload", a.visionUpload)
}

// chatReq representa o payload recebido em /api/chat. Inclui o message,
// history, sessionId (para rastrear pendências) e um campo opcional
// System que pode alterar o comportamento da IA.
type chatReq struct {
    Message   string `json:"message"`
    System    string `json:"system,omitempty"`
    SessionID string `json:"sessionId,omitempty"`
    History   []struct {
        Role    string `json:"role"`
        Content string `json:"content"`
    } `json:"history,omitempty"`
    Timestamp string `json:"timestamp,omitempty"`
}

// chatHandler atende /api/chat. Se houver um produto pendente para o
// sessionId e o usuário enviar um preço, cria o produto na base e
// responde informando. Caso contrário, repassa a mensagem para a IA.
func (a *App) chatHandler(w http.ResponseWriter, r *http.Request) {
    apiKey := os.Getenv("OPENAI_API_KEY")
    if apiKey == "" {
        http.Error(w, "OPENAI_API_KEY not set", http.StatusInternalServerError)
        return
    }
    model := getenv("TEXT_MODEL", "gpt-4o-mini")

    var in chatReq
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
        return
    }
    in.Message = strings.TrimSpace(in.Message)
    if in.Message == "" {
        http.Error(w, "message required", http.StatusBadRequest)
        return
    }

    // Se há pendência para esta sessão e a mensagem contém um preço,
    // processa a criação do produto.
    if p, ok := getPending(in.SessionID); ok {
        if cents, okp := parsePriceToCents(in.Message); okp {
            // lê org/flow do cabeçalho ou fallback para pendência
            orgID := mustAtoi(strings.TrimSpace(r.Header.Get("X-Org-ID")))
            flowID := mustAtoi(strings.TrimSpace(r.Header.Get("X-Flow-ID")))
            if orgID <= 0 {
                orgID = p.OrgID
            }
            if flowID <= 0 {
                flowID = p.FlowID
            }
            if orgID <= 0 {
                orgID = 1
            }
            if flowID <= 0 {
                flowID = 1
            }

            // monta slug usando description ou tags
            slug := firstNonEmpty(p.Suggest.Description, strings.Join(p.Suggest.Tags, ", "))

            row := a.DB.QueryRow(r.Context(), `
                INSERT INTO products (org_id, flow_id, title, slug, status, image_base64, price_cents, stock, category)
                VALUES ($1,$2,$3,$4,'active',$5,$6,0,$7)
                RETURNING id, org_id, flow_id, title, slug, status, image_base64, price_cents, stock, category
            `,
                orgID, flowID,
                limitRunes(p.Suggest.Title, 60),
                limitRunes(slug, 300),
                p.ImageURL,
                cents,
                limitRunes(p.Suggest.Category, 80),
            )

            var prod struct {
                ID         int64  `json:"id"`
                OrgID      int64  `json:"org_id"`
                FlowID     int64  `json:"flow_id"`
                Title      string `json:"title"`
                Slug       string `json:"slug"`
                Status     string `json:"status"`
                ImageURL   string `json:"image_url"`
                PriceCents int    `json:"price_cents"`
                Stock      int    `json:"stock"`
                Category   string `json:"category"`
            }
            if err := row.Scan(&prod.ID, &prod.OrgID, &prod.FlowID, &prod.Title, &prod.Slug, &prod.Status, &prod.ImageURL, &prod.PriceCents, &prod.Stock, &prod.Category); err != nil {
                http.Error(w, "db insert error: "+err.Error(), http.StatusInternalServerError)
                return
            }

            // limpa a pendência
            clearPending(in.SessionID)

            msg := fmt.Sprintf("✅ Produto **%s** cadastrado por R$ %.2f.\nCategoria: %s\nImagem: %s",
                prod.Title, float64(prod.PriceCents)/100.0, prod.Category, prod.ImageURL)

            writeJSON(w, map[string]any{
                "ok":      true,
                "reply":   msg,
                "product": prod,
            })
            return
        }
        // existe pendência mas não identificamos preço
        writeJSON(w, map[string]any{
            "ok":    true,
            "reply": "Por favor, informe o preço no formato 12,34 ou 12.34 (ex.: 129,90).",
        })
        return
    }

    // Sem pendência: fluxo normal de chat
    client := openai.NewClient(apiKey)

    var msgs []openai.ChatCompletionMessage
    if s := strings.TrimSpace(in.System); s != "" {
        msgs = append(msgs, openai.ChatCompletionMessage{
            Role:    openai.ChatMessageRoleSystem,
            Content: s,
        })
    }
    for _, h := range in.History {
        role := h.Role
        if role != "user" && role != "assistant" && role != "system" {
            role = "user"
        }
        msgs = append(msgs, openai.ChatCompletionMessage{
            Role:    role,
            Content: h.Content,
        })
    }
    msgs = append(msgs, openai.ChatCompletionMessage{
        Role:    openai.ChatMessageRoleUser,
        Content: in.Message,
    })

    resp, err := client.CreateChatCompletion(r.Context(), openai.ChatCompletionRequest{
        Model:    model,
        Messages: msgs,
    })
    if err != nil || len(resp.Choices) == 0 {
        http.Error(w, "openai error: "+err.Error(), http.StatusBadGateway)
        return
    }
    text := strings.TrimSpace(resp.Choices[0].Message.Content)
    writeJSON(w, map[string]any{
        "ok":      true,
        "reply":   text,
        "message": text,
        "text":    text,
        "content": text,
        "choices": []map[string]any{
            {"message": map[string]any{"content": text}},
        },
    })
}

// ================================================================
//  visionUpload: analisa imagem, sugere produto e pede preço
// ================================================================

// visionUpload recebe uma imagem, utiliza a IA de visão para sugerir
// dados de produto (nome, descrição, categoria, tags), salva a imagem
// em /uploads e registra uma pendência aguardando o preço.
func (a *App) visionUpload(w http.ResponseWriter, r *http.Request) {
    apiKey := os.Getenv("OPENAI_API_KEY")
    if apiKey == "" {
        http.Error(w, "OPENAI_API_KEY not set", http.StatusInternalServerError)
        return
    }
    model := getenv("VISION_MODEL", "gpt-4o")

    if err := r.ParseMultipartForm(20 << 20); err != nil {
        http.Error(w, "multipart parse error: "+err.Error(), http.StatusBadRequest)
        return
    }
    file, hdr, err := r.FormFile("image")
    if err != nil {
        http.Error(w, "image file required", http.StatusBadRequest)
        return
    }
    defer file.Close()

    raw, err := io.ReadAll(file)
    if err != nil {
        http.Error(w, "read file error: "+err.Error(), http.StatusBadRequest)
        return
    }
    mime := contentTypeFromHeader(hdr)
    if !strings.HasPrefix(mime, "image/") {
        mime = "image/png"
    }
    dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)

    // sessão e prompts opcionais
    sessionID := strings.TrimSpace(r.FormValue("sessionId"))
    nameHint := strings.TrimSpace(r.FormValue("prompt"))

    // construímos o prompt para gerar JSON estrito
    prompt := "Você é um assistente de catalogação de e-commerce. Gere APENAS um JSON com os campos: " +
        `{"title": string (máx 60 chars), "description": string (150-300 chars), "category": string, "tags": string[]}` +
        ". Sem comentários, sem markdown, sem texto extra. Se a imagem não for clara, dê um título genérico."

    client := openai.NewClient(apiKey)
    msg := openai.ChatCompletionMessage{
        Role: openai.ChatMessageRoleUser,
        MultiContent: []openai.ChatMessagePart{
            {Type: openai.ChatMessagePartTypeText, Text: prompt + "\nDica: " + nameHint},
            {
                Type: openai.ChatMessagePartTypeImageURL,
                ImageURL: &openai.ChatMessageImageURL{URL: dataURL},
            },
        },
    }
    resp, err := client.CreateChatCompletion(r.Context(), openai.ChatCompletionRequest{
        Model:       model,
        Messages:    []openai.ChatCompletionMessage{msg},
        Temperature: 0.2,
    })
    if err != nil || len(resp.Choices) == 0 {
        http.Error(w, "openai error: "+err.Error(), http.StatusBadGateway)
        return
    }
    // tenta parsear JSON estrito
    var sug productSuggest
    if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Choices[0].Message.Content)), &sug); err != nil || strings.TrimSpace(sug.Title) == "" {
        // fallback defensivo
        sug.Title = nonEmpty(nameHint, "Produto")
        sug.Description = "Produto cadastrado automaticamente."
        if sug.Category == "" {
            sug.Category = "Geral"
        }
    }

    // salva imagem em uploads
    uploadDir := getenv("UPLOAD_DIR", "uploads")
    if err := os.MkdirAll(uploadDir, 0o755); err != nil {
        http.Error(w, "create upload dir error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    filename := fmt.Sprintf("prod_%d%s", time.Now().UnixNano(), guessExt(mime))
    dst := filepath.Join(uploadDir, filename)
    if err := os.WriteFile(dst, raw, 0o644); err != nil {
        http.Error(w, "save file error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    publicURL := "/uploads/" + filename

    // captura org/flow dos headers para quando formos criar o produto
    orgID := mustAtoi(strings.TrimSpace(r.Header.Get("X-Org-ID")))
    flowID := mustAtoi(strings.TrimSpace(r.Header.Get("X-Flow-ID")))
    if orgID <= 0 {
        orgID = 1
    }
    if flowID <= 0 {
        flowID = 1
    }

    // registra pendência
    setPending(sessionID, &pendingProduct{
        OrgID:     orgID,
        FlowID:    flowID,
        ImagePath: dst,
        ImageURL:  publicURL,
        Suggest:   sug,
    })

    text := fmt.Sprintf(
        "Sugeri **%s**.\nDescrição: %s\nCategoria: %s\nMe diga o preço (ex.: 129,90) que eu já cadastro.",
        limitRunes(sug.Title, 60),
        limitRunes(sug.Description, 280),
        limitRunes(sug.Category, 80),
    )

    writeJSON(w, map[string]any{
        "ok":       true,
        "reply":    text,
        "image_url": publicURL,
        "suggest":  sug,
    })
}

// ================================================================
//  Funções auxiliares
// ================================================================

// contentTypeFromHeader retorna o Content-Type de um cabeçalho de arquivo
// multipart, usando image/png como padrão se estiver vazio.
func contentTypeFromHeader(h *multipart.FileHeader) string {
    if ct := h.Header.Get("Content-Type"); ct != "" {
        return ct
    }
    return "image/png"
}

// writeJSON codifica qualquer objeto como JSON e envia ao cliente.
func writeJSON(w http.ResponseWriter, v any) {
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(v)
}

// guessExt retorna uma extensão de arquivo adequada a partir do tipo MIME.
func guessExt(mime string) string {
    switch strings.ToLower(mime) {
    case "image/jpeg", "image/jpg":
        return ".jpg"
    case "image/webp":
        return ".webp"
    case "image/png":
        fallthrough
    default:
        return ".png"
    }
}

// mustAtoi converte uma string para inteiro, retornando 0 se falhar.
func mustAtoi(s string) int {
    i, _ := strconv.Atoi(strings.TrimSpace(s))
    return i
}

// nonEmpty retorna v se não estiver em branco; caso contrário, def.
func nonEmpty(v, def string) string {
    if strings.TrimSpace(v) != "" {
        return v
    }
    return def
}

// firstNonEmpty retorna o primeiro valor não vazio de uma lista de strings.
func firstNonEmpty(values ...string) string {
    for _, v := range values {
        if strings.TrimSpace(v) != "" {
            return v
        }
    }
    return ""
}

// limitRunes limita uma string ao número máximo de caracteres, preservando
// runas unicode e removendo espaços extras. Se s for menor que max,
// retorna s sem alterações.
func limitRunes(s string, max int) string {
    rs := []rune(strings.TrimSpace(s))
    if len(rs) <= max {
        return strings.TrimSpace(s)
    }
    return string(rs[:max])
}

// parsePriceToCents converte uma string de preço para centavos. Aceita
// formatos como "1.234,56", "1234,56", "1234.56", "R$ 12,34". Retorna
// centavos e um booleano indicando sucesso.
func parsePriceToCents(s string) (int, bool) {
    str := strings.TrimSpace(strings.ToLower(s))
    // remove símbolo R$ e espaços
    str = strings.ReplaceAll(str, "r$", "")
    str = strings.TrimSpace(str)
    // se contém vírgula e não há ponto nos últimos 3 caracteres, substitui vírgula por ponto
    if strings.Contains(str, ",") && !strings.Contains(str[len(str)-3:], ".") {
        str = strings.ReplaceAll(str, ".", "")
        str = strings.ReplaceAll(str, ",", ".")
    } else {
        // remove separadores de milhar
        if strings.Count(str, ",") > 0 && strings.Count(str, ".") > 0 {
            // assume vírgula como separador de milhar
            str = strings.ReplaceAll(str, ",", "")
        }
    }
    // agora str deve estar em formato 1234.56, 129.90 ou 129
    f, err := strconv.ParseFloat(str, 64)
    if err != nil || f < 0 {
        return 0, false
    }
    cents := int(f*100 + 0.5)
    return cents, true
}
