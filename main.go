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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

type App struct{ DB *pgxpool.Pool }

func main() {
	_ = godotenv.Load()
	addr := getenv("APP_ADDR", ":8080")
	dsn := getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/app?sslmode=disable")

	ctx := context.Background()

	// Pool com AfterConnect para garantir search_path=public
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("db parse config: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET search_path TO public`)
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Cria/ajusta o schema ao subir (idempotente)
	if err := ensureSchema(ctx, pool); err != nil {
		log.Fatalf("schema: %v", err)
	}

	app := &App{DB: pool}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Org-ID", "X-Flow-ID"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
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

		r.Post("/webhooks/n8n", app.webhookN8N)
		r.Post("/webhooks/wa/{instance}", app.webhookWa) // webhook Uazapi -> plataforma -> agente
		app.mountWhatsApp(r)                              // rotas de integração com uazapi
	})

	// uploads estáticos
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
