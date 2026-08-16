package registry

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Store is the Postgres-backed node registry. It is the runtime source of truth for
// tari-exporter and tari-netstat's node list.
type Store struct {
	db *sql.DB
}

// NewStore opens a connection pool to the Postgres database at dsn (a standard
// "postgres://user:pass@host:port/dbname?sslmode=..." URL, or any libpq-style DSN pgx
// accepts) and verifies connectivity with a ping.
func NewStore(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("registry: opening store: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("registry: pinging store: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// List returns every enabled node in the registry, ordered by tier then name so
// output/poll order stays stable across calls.
func (s *Store) List(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, tier, pubkey, ip, p2p_port, grpc_port, source, enabled
		FROM nodes
		WHERE enabled = TRUE
		ORDER BY tier, name`)
	if err != nil {
		return nil, fmt.Errorf("registry: listing nodes: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var (
			n      Node
			pubkey sql.NullString
			p2p    sql.NullInt32
		)
		if err := rows.Scan(&n.ID, &n.Name, &n.Tier, &pubkey, &n.IP, &p2p, &n.GRPCPort, &n.Source, &n.Enabled); err != nil {
			return nil, fmt.Errorf("registry: scanning node row: %w", err)
		}
		n.Pubkey = pubkey.String
		n.P2PPort = int(p2p.Int32)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registry: iterating node rows: %w", err)
	}
	return nodes, nil
}

// Upsert inserts n if it doesn't exist yet, or updates it (bumping last_updated) if it
// does. Matching is by pubkey when n.Pubkey is non-empty (the stronger identity — a
// node's IP can change but its pubkey shouldn't); otherwise it falls back to matching
// by name.
func (s *Store) Upsert(ctx context.Context, n Node) error {
	pubkey := nullableString(n.Pubkey)
	p2p := nullableInt(n.P2PPort)

	var query string
	if n.Pubkey != "" {
		query = `
			INSERT INTO nodes (name, tier, pubkey, ip, p2p_port, grpc_port, source, enabled, last_updated)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (pubkey) WHERE pubkey IS NOT NULL DO UPDATE SET
				name = EXCLUDED.name,
				tier = EXCLUDED.tier,
				ip = EXCLUDED.ip,
				p2p_port = EXCLUDED.p2p_port,
				grpc_port = EXCLUDED.grpc_port,
				source = EXCLUDED.source,
				enabled = EXCLUDED.enabled,
				last_updated = now()`
	} else {
		query = `
			INSERT INTO nodes (name, tier, pubkey, ip, p2p_port, grpc_port, source, enabled, last_updated)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (name) DO UPDATE SET
				tier = EXCLUDED.tier,
				pubkey = EXCLUDED.pubkey,
				ip = EXCLUDED.ip,
				p2p_port = EXCLUDED.p2p_port,
				grpc_port = EXCLUDED.grpc_port,
				source = EXCLUDED.source,
				enabled = EXCLUDED.enabled,
				last_updated = now()`
	}

	enabled := n.Enabled
	if n.Source == "" {
		// Default Source for callers that didn't set it (shouldn't normally happen,
		// but avoid writing an empty source string).
		n.Source = "manual"
	}

	_, err := s.db.ExecContext(ctx, query, n.Name, n.Tier, pubkey, n.IP, p2p, n.GRPCPort, n.Source, enabled)
	if err != nil {
		return fmt.Errorf("registry: upserting node %q: %w", n.Name, err)
	}
	return nil
}

// Bootstrap idempotently seeds nodes into the registry. It is meant to be run once
// against a fresh database (cmd/tari-registry-bootstrap) but is safe to run repeatedly
// — each node is upserted, not duplicated, keyed the same way Upsert keys it.
func (s *Store) Bootstrap(ctx context.Context, nodes []Node) error {
	for i, n := range nodes {
		if n.Source == "" {
			n.Source = "bootstrap"
		}
		n.Enabled = true
		if err := s.Upsert(ctx, n); err != nil {
			return fmt.Errorf("registry: bootstrap failed at node %d (%q): %w", i, n.Name, err)
		}
	}
	return nil
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullableInt(i int) sql.NullInt32 {
	if i == 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(i), Valid: true}
}
