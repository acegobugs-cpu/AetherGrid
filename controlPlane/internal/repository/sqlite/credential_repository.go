package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/auth"
)

// CredentialRepository is the SQLite-backed implementation of
// auth.Repository. It persists token hashes only.
type CredentialRepository struct {
	db *sql.DB
}

// NewCredentialRepository constructs a credential repository sharing the
// control plane's single database handle.
func NewCredentialRepository(db *sql.DB) *CredentialRepository {
	return &CredentialRepository{db: db}
}

// Create persists a new credential hash.
func (r *CredentialRepository) Create(ctx context.Context, credential *auth.Credential) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO node_credentials (
			token_hash, node_id, kind, status, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		credential.TokenHash,
		credential.NodeID,
		credential.Kind,
		credential.Status,
		formatTime(credential.CreatedAt),
		formatTime(credential.ExpiresAt),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return auth.ErrTokenExists
		}
		return fmt.Errorf("inserting credential: %w", err)
	}
	return nil
}

// GetByTokenHash returns the credential for the given SHA-256 hex hash.
func (r *CredentialRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*auth.Credential, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT token_hash, node_id, kind, status, created_at, expires_at,
		       used_at, revoked_at, last_used_at
		FROM node_credentials WHERE token_hash = ?`, tokenHash)

	credential, err := scanCredential(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying credential: %w", err)
	}
	return credential, nil
}

// UpdateStatus transitions a credential's lifecycle state.
func (r *CredentialRepository) UpdateStatus(ctx context.Context, tokenHash, status string, at time.Time) error {
	var column string
	switch status {
	case auth.StatusUsed:
		column = "used_at"
	case auth.StatusRevoked:
		column = "revoked_at"
	default:
		column = ""
	}

	query := `UPDATE node_credentials SET status = ?`
	args := []any{status}
	if column != "" {
		query += fmt.Sprintf(", %s = ?", column)
		args = append(args, formatTime(at))
	}
	query += ` WHERE token_hash = ?`
	args = append(args, tokenHash)

	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating credential status: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return auth.ErrNotFound
	}
	return nil
}

// TouchLastUsed records successful use of a credential.
func (r *CredentialRepository) TouchLastUsed(ctx context.Context, tokenHash string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE node_credentials SET last_used_at = ? WHERE token_hash = ?`,
		formatTime(at), tokenHash)
	if err != nil {
		return fmt.Errorf("touching credential last_used_at: %w", err)
	}
	return nil
}

// ListByNode returns every credential issued for a node.
func (r *CredentialRepository) ListByNode(ctx context.Context, nodeID string) ([]*auth.Credential, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT token_hash, node_id, kind, status, created_at, expires_at,
		       used_at, revoked_at, last_used_at
		FROM node_credentials WHERE node_id = ?
		ORDER BY created_at ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("listing credentials: %w", err)
	}
	defer rows.Close()

	credentials := []*auth.Credential{}
	for rows.Next() {
		credential, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating credentials: %w", err)
	}
	return credentials, nil
}

func scanCredential(row interface{ Scan(dest ...any) error }) (*auth.Credential, error) {
	var credential auth.Credential
	var createdAt, expiresAt string
	var usedAt, revokedAt, lastUsedAt sql.NullString

	if err := row.Scan(
		&credential.TokenHash,
		&credential.NodeID,
		&credential.Kind,
		&credential.Status,
		&createdAt,
		&expiresAt,
		&usedAt,
		&revokedAt,
		&lastUsedAt,
	); err != nil {
		return nil, err
	}

	credential.CreatedAt = mustParseTime(createdAt)
	credential.ExpiresAt = mustParseTime(expiresAt)
	credential.UsedAt = parseNullableTime(usedAt)
	credential.RevokedAt = parseNullableTime(revokedAt)
	credential.LastUsedAt = parseNullableTime(lastUsedAt)
	return &credential, nil
}

func mustParseTime(value string) time.Time {
	parsed, err := time.Parse(TimeLayout, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed := mustParseTime(value.String)
	return &parsed
}
