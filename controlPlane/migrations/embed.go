// Package migrations embeds and applies the versioned SQL schema migrations
// for the control plane database.
//
// Migrations live in this directory alongside this file and are embedded
// into the binary so the schema is reproducible and self-contained.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"
)

//go:embed *.sql
var fs embed.FS

// Apply runs every embedded migration that has not yet been recorded in the
// schema_migrations table, in filename order. Each migration executes inside
// its own transaction.
func Apply(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	files, err := fs.ReadDir(".")
	if err != nil {
		return fmt.Errorf("reading embedded migrations: %w", err)
	}

	var names []string
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".sql") {
			names = append(names, file.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		applied, err := migrationApplied(ctx, db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		content, err := fs.ReadFile(name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}

		if err := applyMigration(ctx, db, name, string(content)); err != nil {
			return err
		}
	}

	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, name).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking migration %s: %w", name, err)
	}
	return count > 0, nil
}

func applyMigration(ctx context.Context, db *sql.DB, name, content string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction for migration %s: %w", name, err)
	}
	defer tx.Rollback()

	for _, statement := range splitStatements(content) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("applying migration %s: %w", name, err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		name, nowUTC()); err != nil {
		return fmt.Errorf("recording migration %s: %w", name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration %s: %w", name, err)
	}
	return nil
}

// splitStatements splits a migration file into individual SQL statements,
// ignoring comments and empty segments.  We must be careful to not split on
// semicolons that appear inside comment lines (e.g. "Terraform;" in a comment),
// so we first remove inline semicolons from comment lines before splitting.
func splitStatements(content string) []string {
	// Remove semicolons that appear inside comment lines only.
	// A comment line is a line whose trimmed content starts with "--".
	cleaned := make([]byte, 0, len(content))
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") && strings.Contains(trimmed, ";") {
			// Remove all semicolons from this comment line.
			line = strings.ReplaceAll(line, ";", "")
		}
		cleaned = append(cleaned, []byte(line)...)
		if i < len(lines)-1 {
			cleaned = append(cleaned, '\n')
		}
	}
	content = string(cleaned)

	var statements []string
	for _, raw := range strings.Split(content, ";") {
		statement := strings.TrimSpace(strings.TrimSuffix(raw, ";"))
		if statement == "" {
			continue
		}
		var lines []string
		for _, line := range strings.Split(statement, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}
			lines = append(lines, line)
		}
		statement = strings.Join(lines, "\n")
		if strings.TrimSpace(statement) != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000000Z07:00")
}
