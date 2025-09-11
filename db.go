// db.go
package main

import (
	"database/sql"
)

func ensureSchema(db *sql.DB) error {
	// Tudo qualificado no schema public por segurança.
	// Idempotente: CREATE TABLE IF NOT EXISTS + ON CONFLICT.
	_, err := db.Exec(`
-- =========================
-- BASE
-- =========================
CREATE TABLE IF NOT EXISTS public.orgs (
  id   BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL DEFAULT 'Default Org'
);

CREATE TABLE IF NOT EXISTS public.flows (
  id     BIGSERIAL PRIMARY KEY,
  org_id BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  name   TEXT   NOT NULL DEFAULT 'Default Flow'
);

CREATE TABLE IF NOT EXISTS public.users (
  id            BIGSERIAL PRIMARY KEY,
  org_id        BIGINT NOT NULL REFERENCES public.orgs(id)  ON DELETE CASCADE,
  flow_id       BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  name          TEXT   NOT NULL,
  email         TEXT   NOT NULL UNIQUE,
  tax_id        TEXT,
  password_hash TEXT   NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================
-- EMPRESA
-- =========================
CREATE TABLE IF NOT EXISTS public.company (
  id         BIGSERIAL PRIMARY KEY,
  org_id     BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  razao_social      TEXT,
  nome_fantasia     TEXT,
  tax_id            TEXT,
  inscricao_estadual TEXT,
  segmento          TEXT,
  telefone          TEXT,
  email             TEXT,
  bairro            TEXT,
  endereco          TEXT,
  numero            TEXT,
  cep               TEXT,
  cidade            TEXT,
  uf                TEXT,
  observacoes       TEXT,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================
-- PRODUTOS
-- =========================
CREATE TABLE IF NOT EXISTS public.products (
  id           BIGSERIAL PRIMARY KEY,
  org_id       BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id      BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  title        TEXT   NOT NULL,
  slug         TEXT,
  category     TEXT,
  price_cents  BIGINT NOT NULL DEFAULT 0,
  stock        BIGINT NOT NULL DEFAULT 0,
  status       TEXT   NOT NULL DEFAULT 'active',
  image_url    TEXT,
  image_base64 TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================
-- ANÁLISE
-- =========================
CREATE TABLE IF NOT EXISTS public.analytics_sales_by_hour (
  id      BIGSERIAL PRIMARY KEY,
  org_id  BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  t       TIMESTAMPTZ NOT NULL, -- instante/hora
  c       BIGINT NOT NULL DEFAULT 0
);

-- =========================
-- CONVERSAS & LEADS
-- =========================
CREATE TABLE IF NOT EXISTS public.conversations (
  id         BIGSERIAL PRIMARY KEY,
  org_id     BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id    BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  external_id TEXT,
  last_msg    TEXT,
  status      TEXT NOT NULL DEFAULT 'open',
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.leads (
  id        BIGSERIAL PRIMARY KEY,
  org_id    BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id   BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  name      TEXT,
  phone     TEXT,
  category  TEXT,
  last_msg  TIMESTAMPTZ
);

-- =========================
-- SESSÕES
-- =========================
CREATE TABLE IF NOT EXISTS public.sessions (
  id        BIGSERIAL PRIMARY KEY,
  org_id    BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id   BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  user_id   BIGINT REFERENCES public.users(id) ON DELETE SET NULL,
  token     TEXT   NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ
);

-- =========================
-- WHATSAPP / UAZAPI
-- =========================
CREATE TABLE IF NOT EXISTS public.wa_instances (
  id         BIGSERIAL PRIMARY KEY,
  org_id     BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id    BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  name       TEXT   NOT NULL,
  instance_id TEXT  NOT NULL,
  token      TEXT   NOT NULL,
  status     JSONB  DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.wa_messages (
  id         BIGSERIAL PRIMARY KEY,
  org_id     BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id    BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  instance_id TEXT,
  direction  TEXT NOT NULL, -- in/out
  to_number  TEXT,
  from_number TEXT,
  payload    JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.webhooks_log (
  id        BIGSERIAL PRIMARY KEY,
  org_id    BIGINT NOT NULL REFERENCES public.orgs(id) ON DELETE CASCADE,
  flow_id   BIGINT NOT NULL REFERENCES public.flows(id) ON DELETE CASCADE,
  source    TEXT NOT NULL, -- 'uazapi' etc.
  event     TEXT NOT NULL,
  payload   JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================
-- ÍNDICES ESSENCIAIS
-- =========================
CREATE INDEX IF NOT EXISTS idx_users_email ON public.users(email);
CREATE INDEX IF NOT EXISTS idx_products_org_flow ON public.products(org_id, flow_id);
CREATE INDEX IF NOT EXISTS idx_wa_messages_time ON public.wa_messages(created_at);
CREATE INDEX IF NOT EXISTS idx_webhooks_time ON public.webhooks_log(created_at);

-- =========================
-- SEEDS
-- =========================
INSERT INTO public.orgs (id, name) VALUES (1, 'Default Org')
ON CONFLICT (id) DO NOTHING;

INSERT INTO public.flows (id, org_id, name) VALUES (1, 1, 'Default Flow')
ON CONFLICT (id) DO NOTHING;
`)
	return err
}
