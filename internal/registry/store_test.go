package registry

import (
	"context"
	"os"
	"testing"
)

// testStore returns a Store connected to a real Postgres instance for DB-dependent
// tests, or calls t.Skip if none is reachable. It looks for TARI_OBSERVABILITY_TEST_DSN
// first; if unset, these tests are skipped explicitly rather than faked. It creates
// (and cleans) its own throwaway "nodes" table state per test via TRUNCATE.
func testStore(t *testing.T) *Store {
	t.Helper()

	dsn := os.Getenv("TARI_OBSERVABILITY_TEST_DSN")
	if dsn == "" {
		t.Skip("no postgres available: TARI_OBSERVABILITY_TEST_DSN is not set, skipping DB-dependent test")
	}

	store, err := NewStore(dsn)
	if err != nil {
		t.Skipf("no postgres available: could not connect to TARI_OBSERVABILITY_TEST_DSN: %v", err)
	}

	if _, err := store.db.ExecContext(context.Background(), schemaSQLForTests); err != nil {
		t.Fatalf("applying schema: %v", err)
	}
	if _, err := store.db.ExecContext(context.Background(), "TRUNCATE TABLE nodes"); err != nil {
		t.Fatalf("truncating nodes table: %v", err)
	}

	t.Cleanup(func() { store.Close() })
	return store
}

// schemaSQLForTests mirrors schema.sql. Kept inline (rather than reading the file) so
// these tests don't depend on the working directory the test binary runs from.
const schemaSQLForTests = `
CREATE TABLE IF NOT EXISTS nodes (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    tier          TEXT NOT NULL,
    pubkey        TEXT,
    ip            TEXT NOT NULL,
    p2p_port      INT,
    grpc_port     INT NOT NULL,
    source        TEXT NOT NULL,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    first_seen    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_updated  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS nodes_name_key ON nodes (name);
CREATE UNIQUE INDEX IF NOT EXISTS nodes_pubkey_key ON nodes (pubkey) WHERE pubkey IS NOT NULL;
CREATE INDEX IF NOT EXISTS nodes_enabled_idx ON nodes (enabled);
CREATE INDEX IF NOT EXISTS nodes_tier_idx ON nodes (tier);
`

func TestStore_UpsertInsertsOnEmptyDB(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	n := Node{Name: "seed-01", Tier: "seed", Pubkey: "pk1", IP: "10.0.0.1", P2PPort: 18189, GRPCPort: 18142, Source: "manual", Enabled: true}
	if err := store.Upsert(ctx, n); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	nodes, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after insert, got %d", len(nodes))
	}
	if nodes[0].Name != "seed-01" || nodes[0].Pubkey != "pk1" {
		t.Errorf("unexpected node after insert: %+v", nodes[0])
	}
}

func TestStore_UpsertOnExistingPubkeyUpdatesNotDuplicates(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	n := Node{Name: "seed-01", Tier: "seed", Pubkey: "pk1", IP: "10.0.0.1", P2PPort: 18189, GRPCPort: 18142, Source: "manual", Enabled: true}
	if err := store.Upsert(ctx, n); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// Same pubkey, different IP/name — should update the existing row, not insert a
	// second one.
	n.IP = "10.0.0.99"
	n.Name = "seed-01-renamed"
	if err := store.Upsert(ctx, n); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	nodes, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected exactly 1 node (update, not duplicate), got %d", len(nodes))
	}
	if nodes[0].IP != "10.0.0.99" || nodes[0].Name != "seed-01-renamed" {
		t.Errorf("expected update to have applied, got %+v", nodes[0])
	}
}

func TestStore_ListReturnsOnlyEnabled(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	enabledNode := Node{Name: "enabled-1", Tier: "seed", Pubkey: "pk-enabled", IP: "10.0.0.1", GRPCPort: 18142, Source: "manual", Enabled: true}
	disabledNode := Node{Name: "disabled-1", Tier: "seed", Pubkey: "pk-disabled", IP: "10.0.0.2", GRPCPort: 18142, Source: "manual", Enabled: false}

	if err := store.Upsert(ctx, enabledNode); err != nil {
		t.Fatalf("Upsert enabled: %v", err)
	}
	if err := store.Upsert(ctx, disabledNode); err != nil {
		t.Fatalf("Upsert disabled: %v", err)
	}

	nodes, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected exactly 1 enabled node, got %d", len(nodes))
	}
	if nodes[0].Name != "enabled-1" {
		t.Errorf("expected the enabled node, got %+v", nodes[0])
	}
}

func TestStore_BootstrapIsIdempotent(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	seed := []Node{
		{Name: "seed-01", Tier: "seed", Pubkey: "pk1", IP: "10.0.0.1", GRPCPort: 18142},
		{Name: "seed-02", Tier: "seed", Pubkey: "pk2", IP: "10.0.0.2", GRPCPort: 18142},
	}

	if err := store.Bootstrap(ctx, seed); err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	if err := store.Bootstrap(ctx, seed); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}

	nodes, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes after running Bootstrap twice, got %d", len(nodes))
	}
}
