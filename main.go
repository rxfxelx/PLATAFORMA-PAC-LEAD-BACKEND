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

	// --------- Pool com AfterConnect para garantir search_path=public ----------
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("db parse config: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// Garante que qualquer conexão do pool opere no schema public
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

	// CORS via github.com/go-chi/cors
	r.Use(cors.Handler(cors.Options{
		// ALLOWED_ORIGINS="https://a.com,https://b.com" ou "*" (padrão)
		AllowedOrigins:   allowedOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Org-ID", "X-Flow-ID"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	// Preflight catch-all (o middleware já cobre, mas deixamos por segurança)
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
		// Webhook para eventos da uazapi (multi-instância). Os eventos
		// chegarão em /webhooks/wa/{instance} e serão reconhecidos com status 202.
		// A lógica de encaminhamento para o agente IA deve ser implementada em webhook_wa.go.
		r.Post("/webhooks/wa/{instance}", app.webhookWa)

		// Rotas de integração com WhatsApp (uazapi).
		// As rotas abaixo permitem criar instâncias de WhatsApp,
		// acompanhar o status/QR Code, definir o webhook de entrada
		// e enviar mensagens de texto via cada instância.
		app.mountWhatsApp(r)
	})

	// Servir uploads estáticos (sem /api)
	uploadDir := getenv("UPLOAD_DIR", "uploads")
	r.Mount("/uploads", http.StripPrefix("/uploads", http.FileServer(http.Dir(uploadDir))))

	log.Printf("listening on %s", addr)
	// IMPORTANTE: usar o router diretamente (sem corsMiddleware aqui).
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

// ------------------- ensureSchema -------------------
// Cria as tabelas base e ajusta colunas vitais (idempotente).
func ensureSchema(ctx context.Context, db *pgxpool.Pool) error {
	stmts := []string{
		// ORGS
		`CREATE TABLE IF NOT EXISTS public.orgs (
			id          BIGSERIAL PRIMARY KEY,
			name        TEXT NOT NULL,
			tax_id      TEXT UNIQUE,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// FLOWS
		`CREATE TABLE IF NOT EXISTS public.flows (
			id          BIGSERIAL PRIMARY KEY,
			org_id      BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			name        TEXT NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// USERS
		`CREATE TABLE IF NOT EXISTS public.users (
			id            BIGSERIAL PRIMARY KEY,
			org_id        BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id       BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			name          TEXT NOT NULL,
			email         TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		// Em bases antigas, garante colunas que possam faltar
		`ALTER TABLE public.users ADD COLUMN IF NOT EXISTS org_id BIGINT;`,
		`ALTER TABLE public.users ADD COLUMN IF NOT EXISTS flow_id BIGINT;`,
		`ALTER TABLE public.users ADD COLUMN IF NOT EXISTS password_hash TEXT;`,
		`ALTER TABLE public.users ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();`,
		`CREATE INDEX IF NOT EXISTS idx_users_email_lower ON public.users (LOWER(email));`,

		// COMPANY (um registro por org)
		`CREATE TABLE IF NOT EXISTS public.company (
			org_id              BIGINT PRIMARY KEY REFERENCES public.orgs(id) ON DELETE CASCADE,
			razao_social        TEXT,
			nome_fantasia       TEXT,
			tax_id              TEXT,
			inscricao_estadual  TEXT,
			segmento            TEXT,
			telefone            TEXT,
			email               TEXT,
			bairro              TEXT,
			endereco            TEXT,
			numero              TEXT,
			cep                 TEXT,
			cidade              TEXT,
			uf                  TEXT,
			observacoes         TEXT,
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// PRODUCTS
		`CREATE TABLE IF NOT EXISTS public.products (
			id           BIGSERIAL PRIMARY KEY,
			org_id       BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id      BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			title        TEXT NOT NULL,
			slug         TEXT,
			category     TEXT,
			status       TEXT NOT NULL DEFAULT 'active',
			price_cents  INTEGER NOT NULL DEFAULT 0,
			stock        INTEGER NOT NULL DEFAULT 0,
			image_url    TEXT,
			image_base64 TEXT,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_products_org_flow ON public.products (org_id, flow_id);`,

		// LEADS
		`CREATE TABLE IF NOT EXISTS public.leads (
			id         BIGSERIAL PRIMARY KEY,
			org_id     BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id    BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			name       TEXT,
			phone      TEXT,
			email      TEXT,
			source     TEXT,
			stage      TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// CONVERSATIONS
		`CREATE TABLE IF NOT EXISTS public.conversations (
			id           BIGSERIAL PRIMARY KEY,
			org_id       BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id      BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			lead_id      BIGINT,
			last_message TEXT,
			status       TEXT,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// ANALYTICS (vendas por hora)
		`CREATE TABLE IF NOT EXISTS public.analytics_sales_by_hour (
			id      BIGSERIAL PRIMARY KEY,
			org_id  BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			t       TIMESTAMPTZ NOT NULL,
			c       INTEGER NOT NULL DEFAULT 0
		);`,

		// WA: instâncias e mensagens
		`CREATE TABLE IF NOT EXISTS public.wa_instances (
			id          BIGSERIAL PRIMARY KEY,
			org_id      BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id     BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			instance_id TEXT NOT NULL,
			token       TEXT NOT NULL,
			status      TEXT,
			jid         TEXT,
			logged_in   BOOLEAN,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE TABLE IF NOT EXISTS public.wa_messages (
			id           BIGSERIAL PRIMARY KEY,
			org_id       BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id      BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			instance_id  TEXT,
			direction    TEXT, -- in/out
			to_number    TEXT,
			from_number  TEXT,
			payload      JSONB,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_wa_messages_created ON public.wa_messages (created_at);`,

		// WEBHOOKS LOG
		`CREATE TABLE IF NOT EXISTS public.webhooks_log (
			id         BIGSERIAL PRIMARY KEY,
			org_id     BIGINT,
			flow_id    BIGINT,
			source     TEXT,
			event      TEXT,
			payload    JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// AGENT CONFIGS
		`CREATE TABLE IF NOT EXISTS public.agent_configs (
			id        BIGSERIAL PRIMARY KEY,
			org_id    BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id   BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			name      TEXT,
			profile   JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
	}

	for _, q := range stmts {
		if _, err := db.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
