-- Schema for go-tari-observability's node registry.
--
-- Postgres is the runtime source of truth for the node list. This file is applied
-- once (manually, via `psql -f` or equivalent) against a fresh database before running
-- cmd/tari-registry-bootstrap. There is no migration tool wired up yet — follow the
-- ecosystem convention of plain schema.sql + manual migrations for now.

CREATE TABLE IF NOT EXISTS nodes (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    tier          TEXT NOT NULL, -- 'seed' | 'priority' | 'imported'
    pubkey        TEXT,          -- nullable: unknown for some imported nodes
    ip            TEXT NOT NULL,
    p2p_port      INT,           -- nullable: unknown for some imported nodes
    grpc_port     INT NOT NULL,
    source        TEXT NOT NULL, -- 'bootstrap' | 'manual' | 'mapping-tool-import'
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    first_seen    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_updated  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS nodes_name_key ON nodes (name);

-- Partial unique index: multiple rows may have a NULL pubkey (unknown), but any
-- non-null pubkey must be globally unique.
CREATE UNIQUE INDEX IF NOT EXISTS nodes_pubkey_key ON nodes (pubkey) WHERE pubkey IS NOT NULL;

CREATE INDEX IF NOT EXISTS nodes_enabled_idx ON nodes (enabled);
CREATE INDEX IF NOT EXISTS nodes_tier_idx ON nodes (tier);
