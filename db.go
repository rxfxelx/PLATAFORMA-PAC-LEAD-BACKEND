package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ensureSchema cria/ajusta o schema necessário de forma idempotente.
func ensureSchema(ctx context.Context, db *pgxpool.Pool) error {
	// Força search_path public (também feito no AfterConnect)
	_, _ = db.Exec(ctx, `SET search_path TO public`)

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

		// USERS (compatível com seu handlers.go -> coluna "password")
		`CREATE TABLE IF NOT EXISTS public.users (
			id            BIGSERIAL PRIMARY KEY,
			org_id        BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id       BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			name          TEXT NOT NULL,
			email         TEXT NOT NULL UNIQUE,
			password      TEXT NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// Ajustes defensivos para bases antigas (adiciona colunas que faltarem)
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users' AND column_name='org_id'
			) THEN
				EXECUTE 'ALTER TABLE public.users ADD COLUMN org_id BIGINT';
			END IF;

			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users' AND column_name='flow_id'
			) THEN
				EXECUTE 'ALTER TABLE public.users ADD COLUMN flow_id BIGINT';
			END IF;

			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users' AND column_name='password'
			) THEN
				EXECUTE 'ALTER TABLE public.users ADD COLUMN password TEXT NOT NULL DEFAULT '''''';
			END IF;

			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users' AND column_name='created_at'
			) THEN
				EXECUTE 'ALTER TABLE public.users ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()';
			END IF;
		END $$;`,

		`CREATE INDEX IF NOT EXISTS idx_users_email_lower ON public.users ((LOWER(email)));`,

		// COMPANY (1 registro por org)
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
		`CREATE INDEX IF NOT EXISTS idx_sales_hour_org_flow_t ON public.analytics_sales_by_hour (org_id, flow_id, t);`,

		// WHATSAPP: INSTÂNCIAS
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
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_wa_instances_instance_id ON public.wa_instances(instance_id);`,

		// WHATSAPP: MENSAGENS
		`CREATE TABLE IF NOT EXISTS public.wa_messages (
			id           BIGSERIAL PRIMARY KEY,
			org_id       BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id      BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			instance_id  TEXT,
			direction    TEXT,
			to_number    TEXT,
			from_number  TEXT,
			payload      JSONB,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_wa_messages_created ON public.wa_messages (created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_wa_messages_org_flow ON public.wa_messages (org_id, flow_id);`,

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
			id         BIGSERIAL PRIMARY KEY,
			org_id     BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
			flow_id    BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
			name       TEXT,
			profile    JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,

		// SEEDS (org=1 e flow=1)
		`INSERT INTO public.orgs (id, name) VALUES (1, 'Default Org')
		 ON CONFLICT (id) DO NOTHING;`,
		`INSERT INTO public.flows (id, org_id, name) VALUES (1, 1, 'Default Flow')
		 ON CONFLICT (id) DO NOTHING;`,
	}

	for _, q := range stmts {
		if _, err := db.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
