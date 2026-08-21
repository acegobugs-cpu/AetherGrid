package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"
)

// CommandRepository is the SQLite-backed implementation of
// repository.CommandRepository. It stores agent commands alongside nodes in
// the same database file.
type CommandRepository struct {
	db *sql.DB
}

// NewCommandRepository constructs a command repository sharing the given
// database handle.
func NewCommandRepository(db *sql.DB) *CommandRepository {
	return &CommandRepository{db: db}
}

// Create inserts a new command record.
func (r *CommandRepository) Create(ctx context.Context, command *domain.Command) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO commands (
			id, node_id, type, parameters, status, result, error,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		command.ID,
		command.NodeID,
		command.Type,
		rawJSON(command.Parameters),
		string(command.Status),
		rawJSON(command.Result),
		command.Error,
		formatTime(command.CreatedAt),
		formatTime(command.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("inserting command %q: %w", command.ID, err)
	}
	return nil
}

// GetByID returns a single command by UUID.
func (r *CommandRepository) GetByID(ctx context.Context, id string) (*domain.Command, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, node_id, type, parameters, status, result, error,
		       created_at, updated_at
		FROM commands WHERE id = ?`, id)

	command, err := scanCommand(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying command %q: %w", id, err)
	}
	return command, nil
}

// ListByNode returns every command for a node, oldest first.
func (r *CommandRepository) ListByNode(ctx context.Context, nodeID string) ([]*domain.Command, error) {
	return r.queryCommands(ctx,
		`SELECT id, node_id, type, parameters, status, result, error,
		        created_at, updated_at
		 FROM commands WHERE node_id = ? ORDER BY created_at`, nodeID)
}

// ListPendingByNode returns every non-terminal command for a node, oldest
// first.
func (r *CommandRepository) ListPendingByNode(ctx context.Context, nodeID string) ([]*domain.Command, error) {
	return r.queryCommands(ctx,
		`SELECT id, node_id, type, parameters, status, result, error,
		        created_at, updated_at
		 FROM commands
		 WHERE node_id = ? AND status NOT IN ('SUCCEEDED', 'FAILED')
		 ORDER BY created_at`, nodeID)
}

// Update persists changes to an existing command.
func (r *CommandRepository) Update(ctx context.Context, command *domain.Command) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE commands SET
			type = ?, parameters = ?, status = ?, result = ?,
			error = ?, updated_at = ?
		WHERE id = ?`,
		command.Type,
		rawJSON(command.Parameters),
		string(command.Status),
		rawJSON(command.Result),
		command.Error,
		formatTime(command.UpdatedAt),
		command.ID,
	)
	if err != nil {
		return fmt.Errorf("updating command %q: %w", command.ID, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *CommandRepository) queryCommands(ctx context.Context, query, nodeID string) ([]*domain.Command, error) {
	rows, err := r.db.QueryContext(ctx, query, nodeID)
	if err != nil {
		return nil, fmt.Errorf("querying commands for node %q: %w", nodeID, err)
	}
	defer rows.Close()

	var commands []*domain.Command
	for rows.Next() {
		command, err := scanCommand(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning command row: %w", err)
		}
		commands = append(commands, command)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating command rows: %w", err)
	}
	return commands, nil
}

func scanCommand(scanner rowScanner) (*domain.Command, error) {
	var (
		id          string
		nodeID      string
		commandType string
		parameters  string
		status      string
		result      sql.NullString
		errorText   string
		createdAt   string
		updatedAt   string
	)

	if err := scanner.Scan(
		&id,
		&nodeID,
		&commandType,
		&parameters,
		&status,
		&result,
		&errorText,
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

	command := &domain.Command{
		ID:         id,
		NodeID:     nodeID,
		Type:       commandType,
		Parameters: json.RawMessage(parameters),
		Status:     domain.CommandStatus(status),
		Error:      errorText,
		CreatedAt:  created,
		UpdatedAt:  updated,
	}

	if result.Valid {
		command.Result = json.RawMessage(result.String)
	}

	return command, nil
}

// rawJSON converts a json.RawMessage into a value suitable for storage,
// normalizing nil/empty values to an empty string.
func rawJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}
