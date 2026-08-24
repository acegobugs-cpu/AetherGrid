package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CredentialFileName is the file used to store the node credential within
// the data dir. The credential is a bearer secret: possession authenticates
// the agent to the control plane.
const CredentialFileName = "node-credential"

// ErrCredentialNotFound is returned when no credential has been persisted
// yet (first boot before registration).
var ErrCredentialNotFound = errors.New("node credential not found")

// LoadCredential returns the persisted node credential.
func (s *Store) LoadCredential() (string, error) {
	data, err := os.ReadFile(s.credentialPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrCredentialNotFound
		}
		return "", fmt.Errorf("reading credential file %q: %w", s.credentialPath(), err)
	}

	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("credential file %q is empty or corrupt", s.credentialPath())
	}
	return value, nil
}

// SaveCredential persists the given node credential with owner-only
// permissions.
func (s *Store) SaveCredential(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("cannot persist an empty node credential")
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating identity directory: %w", err)
	}

	path := s.credentialPath()
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing credential file %q: %w", path, err)
	}
	return nil
}

func (s *Store) credentialPath() string {
	return filepath.Join(filepath.Dir(s.path), CredentialFileName)
}
