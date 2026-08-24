package auth

import (
	"crypto/subtle"
	"fmt"
	"sort"
	"strings"
)

// StaticKey is one pre-provisioned human API key with its assigned role.
// Static keys are the human/operator authentication mechanism: operators
// provision keys out-of-band and inject them via configuration (never in
// source control).
type StaticKey struct {
	Token string
	Role  Role
}

// StaticKeyStore authenticates human principals against configured API keys.
// Lookup compares hashes in constant time; the store keeps only SHA-256
// digests in memory so a process dump does not yield usable tokens.
type StaticKeyStore struct {
	hashes map[string]StaticKey // sha256 hex -> key metadata
}

// NewStaticKeyStore parses "token:role" pairs ("agr_admin_key:admin,...")
// into an immutable store. Empty entries are skipped. An error is returned
// for malformed pairs, unknown roles or tokens shorter than 16 characters.
func NewStaticKeyStore(pairs []string) (*StaticKeyStore, error) {
	store := &StaticKeyStore{hashes: make(map[string]StaticKey, len(pairs))}
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		token, role, found := strings.Cut(pair, ":")
		token = strings.TrimSpace(token)
		roleValue := strings.TrimSpace(strings.ToLower(role))
		if !found || token == "" || roleValue == "" {
			return nil, fmt.Errorf("invalid static key %q: expected token:role", mask(pair))
		}
		if len(token) < 16 {
			return nil, fmt.Errorf("static key %s rejected: tokens must be at least 16 characters", mask(token))
		}
		if !ValidRole(roleValue) {
			return nil, fmt.Errorf("static key %s has unknown role %q", mask(token), roleValue)
		}
		store.hashes[HashToken(token)] = StaticKey{Token: token, Role: Role(roleValue)}
	}
	return store, nil
}

// Authenticate resolves a bearer token to a human principal. It returns nil
// when the token does not match any configured key.
func (s *StaticKeyStore) Authenticate(token string) *Principal {
	if s == nil || token == "" {
		return nil
	}
	candidate := HashToken(token)
	for hash, key := range s.hashes {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(candidate)) == 1 {
			return &Principal{Type: PrincipalHuman, Role: key.Role, TokenHash: hash}
		}
	}
	return nil
}

// Empty reports whether no static keys are configured.
func (s *StaticKeyStore) Empty() bool {
	return s == nil || len(s.hashes) == 0
}

// Roles returns the distinct roles present in the store (used for startup
// validation: at least one admin key should exist in production).
func (s *StaticKeyStore) Roles() []Role {
	if s == nil {
		return nil
	}
	seen := make(map[Role]bool, len(s.hashes))
	roles := make([]Role, 0, len(s.hashes))
	for _, key := range s.hashes {
		if !seen[key.Role] {
			seen[key.Role] = true
			roles = append(roles, key.Role)
		}
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	return roles
}

// mask renders a token for error messages without exposing it.
func mask(token string) string {
	if len(token) <= 6 {
		return "***"
	}
	return token[:4] + "…" + token[len(token)-2:]
}

// ParseStaticKeys splits a comma-separated AUTH_STATIC_KEYS value.
func ParseStaticKeys(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			result = append(result, part)
		}
	}
	return result
}
