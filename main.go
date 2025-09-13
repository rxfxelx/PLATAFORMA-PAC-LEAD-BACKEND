// main.go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "strings"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/go-chi/cors"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/joho/godotenv"
)

type App struct{ DB *pgxpool.Pool }

func main() {
    _ = godotenv.Load()
    addr := getenv("APP_ADDR", ":8080")
    dsn := getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/app?sslmode=disable")

    ctx := context.Background()
    pool, err := pgxpool.New(ctx, dsn)
    if err != nil {
        log.Fatalf("db: %v", err)
    }
    defer pool.Close()

    app := &App{DB: pool}

    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    r.Use(middleware.Timeout(60 * time.Second))

    // CORS via github.com/go-chi/cors
    r.Use(cors.Handler(cors.Options{
        // ALLOWED_ORIGINS="https://a.com,https://b.com" ou "*" (padrão)
        AllowedOrigins:   allowedOrigins(),
        AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        // (ATUALIZADO) Inclui headers usados para escopo multi-tenant/instância
        AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Org-ID", "X-Flow-ID", "X-Instance-ID", "X-Instance-Token"},
        ExposedHeaders:   []string{"Link"},
        AllowCredentials: false,
        MaxAge:           300,
    }))
    // Preflight catch-all
    r.Options("/*", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

    // Healthcheck
    r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })

    // API
    r.Route("/api", func(r chi.Router) {
        app.mountAuth(r)
        app.mountCatalog(r)
        app.mountLeads(r)
        app.mountOrders(r)
        app.mountAnalytics(r)
        app.mountChat(r)    // /api/chat, /api/vision/upload
        app.mountCompany(r) // /api/company
        app.mountUpload(r)  // /api/upload
        app.mountResolve(r) // /api/orgs/resolve/{tax_id}

        // >>> ADICIONADO: configurações do agente (multi-tenant)
        app.mountAgentConfig(r)

        r.Post("/webhooks/n8n", app.webhookN8N)
        // Webhook para eventos da uazapi (multi-instância).
        r.Post("/webhooks/wa/{instance}", app.webhookWa)

        // Rotas de integração com WhatsApp (uazapi).
        app.mountWhatsApp(r)
    })

    // Servir uploads estáticos (sem /api)
    uploadDir := getenv("UPLOAD_DIR", "uploads")
    r.Mount("/uploads", http.StripPrefix("/uploads", http.FileServer(http.Dir(uploadDir))))

    log.Printf("listening on %s", addr)
    log.Fatal(http.ListenAndServe(addr, r))
}

func getenv(k, def string) string {
    if v := os.Getenv(k); v != "" {
        return v
    }
    return def
}

func allowedOrigins() []string {
    v := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS"))
    if v == "" || v == "*" {
        return []string{"*"}
    }
    parts := strings.Split(v, ",")
    out := make([]string, 0, len(parts))
    for _, p := range parts {
        s := strings.TrimSpace(p)
        if s != "" {
            out = append(out, s)
        }
    }
    if len(out) == 0 {
        return []string{"*"}
    }
    return out
}
