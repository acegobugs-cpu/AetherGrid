// Package identity persists the agent's assigned node identity so a restart
// reconnects to the same control-plane node instead of registering a new one.
package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when no identity has been persisted yet.
var ErrNotFound = errors.New("node identity not found")

// FileName is the file used to store the node identity within the data dir.
const FileName = "node-id"

// Store persists a single node identity as a small local file. The location
// is configurable via the agent data directory.
type Store struct {
	path string
}

// NewStore returns a Store rooted at the given data directory.
func NewStore(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, FileName)}
}

// Path returns the absolute-ish file path the identity is stored in.
func (s *Store) Path() string {
	return s.path
}

// Load returns the persisted node identity. It returns ErrNotFound when no
// identity file exists yet.
func (s *Store) Load() (string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("reading identity file %q: %w", s.path, err)
	}

	// A corrupt/empty file must be reported clearly rather than silently
	// reused as a valid identity.
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("identity file %q is empty or corrupt", s.path)
	}
	return value, nil
}

// Save persists the given node identity. The parent directory is created if
// necessary.
func (s *Store) Save(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("cannot persist an empty node identity")
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating identity directory: %w", err)
	}

	// 0600 keeps the identity file readable only by the agent's user.
	if err := os.WriteFile(s.path, []byte(id+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing identity file %q: %w", s.path, err)
	}
	return nil
}
