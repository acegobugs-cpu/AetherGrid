// Package sqlite implements the repository interfaces against a SQLite
// database using the database/sql standard library and the modernc.org/sqlite
// pure-Go driver.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"

	_ "modernc.org/sqlite"
)

// TimeLayout is the canonical UTC timestamp format stored in the database.
const TimeLayout = "2006-01-02T15:04:05.000000Z07:00"

// NodeRepository is the SQLite-backed implementation of repository.NodeRepository.
type NodeRepository struct {
	db *sql.DB
}

// NewNodeRepository opens (creating if necessary) the SQLite database at path
// and returns a ready-to-use node repository.
func NewNodeRepository(path string) (*NodeRepository, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database %q: %w", path, err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Serialize concurrent writes; SQLite supports one writer at a time and a
	// single connection avoids SQLITE_BUSY errors in this control plane.
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting busy_timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	return &NodeRepository{db: db}, nil
}

// DB exposes the underlying database handle so callers (for example the
// migration runner) can share a single connection pool.
func (r *NodeRepository) DB() *sql.DB {
	return r.db
}

// Close releases the underlying database connection.
func (r *NodeRepository) Close() error {
	return r.db.Close()
}

// Create inserts a new node record.
func (r *NodeRepository) Create(ctx context.Context, node *domain.Node) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO nodes (
			id, name, status, desired_status, location, ip_address,
			kubernetes_enabled, wireguard_enabled, last_heartbeat,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.ID,
		node.Name,
		string(node.Status),
		string(node.DesiredStatus),
		node.Location,
		node.IPAddress,
		boolToInt(node.KubernetesEnabled),
		boolToInt(node.WireGuardEnabled),
		nullableTime(node.LastHeartbeat),
		formatTime(node.CreatedAt),
		formatTime(node.UpdatedAt),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: name %q", repository.ErrConflict, node.Name)
		}
		return fmt.Errorf("inserting node %q: %w", node.ID, err)
	}
	return nil
}

// GetByID returns a single node by UUID.
func (r *NodeRepository) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, status, desired_status, location, ip_address,
		       kubernetes_enabled, wireguard_enabled, last_heartbeat,
		       created_at, updated_at
		FROM nodes WHERE id = ?`, id)

	node, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying node %q: %w", id, err)
	}
	return node, nil
}

// GetAll returns every registered node.
func (r *NodeRepository) GetAll(ctx context.Context) ([]*domain.Node, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, status, desired_status, location, ip_address,
		       kubernetes_enabled, wireguard_enabled, last_heartbeat,
		       created_at, updated_at
		FROM nodes ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("querying nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*domain.Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning node row: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating nodes: %w", err)
	}
	return nodes, nil
}

// Update persists changes to an existing node.
func (r *NodeRepository) Update(ctx context.Context, node *domain.Node) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE nodes SET
			name = ?, status = ?, desired_status = ?, location = ?,
			ip_address = ?, kubernetes_enabled = ?, wireguard_enabled = ?,
			last_heartbeat = ?, updated_at = ?
		WHERE id = ?`,
		node.Name,
		string(node.Status),
		string(node.DesiredStatus),
		node.Location,
		node.IPAddress,
		boolToInt(node.KubernetesEnabled),
		boolToInt(node.WireGuardEnabled),
		nullableTime(node.LastHeartbeat),
		formatTime(node.UpdatedAt),
		node.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: name %q", repository.ErrConflict, node.Name)
		}
		return fmt.Errorf("updating node %q: %w", node.ID, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// Delete removes a node by UUID.
func (r *NodeRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting node %q: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// rowScanner abstracts *sql.Row and *sql.Rows for scanNode.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(scanner rowScanner) (*domain.Node, error) {
	var (
		id                string
		name              string
		status            string
		desiredStatus     string
		location          string
		ipAddress         string
		kubernetesEnabled int
		wireguardEnabled  int
		lastHeartbeat     sql.NullString
		createdAt         string
		updatedAt         string
	)

	if err := scanner.Scan(
		&id,
		&name,
		&status,
		&desiredStatus,
		&location,
		&ipAddress,
		&kubernetesEnabled,
		&wireguardEnabled,
		&lastHeartbeat,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}

	created, err := parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err)
	}

	node := &domain.Node{
		ID:                id,
		Name:              name,
		Status:            domain.NodeStatus(status),
		DesiredStatus:     domain.NodeStatus(desiredStatus),
		Location:          location,
		IPAddress:         ipAddress,
		KubernetesEnabled: intToBool(kubernetesEnabled),
		WireGuardEnabled:  intToBool(wireguardEnabled),
		CreatedAt:         created,
		UpdatedAt:         updated,
	}

	if lastHeartbeat.Valid {
		heartbeat, err := parseTime(lastHeartbeat.String)
		if err != nil {
			return nil, fmt.Errorf("parsing last_heartbeat %q: %w", lastHeartbeat.String, err)
		}
		node.LastHeartbeat = &heartbeat
	}

	return node, nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(TimeLayout, value)
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intToBool(value int) bool {
	return value != 0
}

func isUniqueViolation(err error) bool {
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") ||
		strings.Contains(message, "constraint failed")
}
