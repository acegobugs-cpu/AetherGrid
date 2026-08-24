package auth

import (
	"context"
	"fmt"
	"time"
)

// Service implements the credential lifecycle: bootstrap issuance, exchange
// (secure registration), verification, rotation and revocation.
type Service struct {
	repo Repository
	now  func() time.Time

	// BootstrapTokenTTL bounds how long a bootstrap credential may sit
	// unused before it expires.
	BootstrapTokenTTL time.Duration
	// AgentCredentialTTL is the maximum age of an agent credential. Agents
	// must rotate before expiry; the TTL bounds the blast radius of a stolen
	// token.
	AgentCredentialTTL time.Duration
}

// NewService constructs a credential service. Non-positive TTLs fall back to
// secure defaults.
func NewService(repo Repository) *Service {
	return &Service{
		repo:               repo,
		now:                time.Now().UTC,
		BootstrapTokenTTL:  15 * time.Minute,
		AgentCredentialTTL: 90 * 24 * time.Hour,
	}
}

// SetClock overrides the time source (used by tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// IssueBootstrap mints a single-use bootstrap credential bound to nodeID.
// The returned plaintext is shown exactly once and never persisted.
func (s *Service) IssueBootstrap(ctx context.Context, nodeID string) (token string, expiresAt time.Time, err error) {
	return s.issue(ctx, nodeID, KindBootstrap, s.BootstrapTokenTTL)
}

// issueAgentCredential mints a long-lived agent credential bound to nodeID.
func (s *Service) issueAgentCredential(ctx context.Context, nodeID string) (token string, expiresAt time.Time, err error) {
	return s.issue(ctx, nodeID, KindAgent, s.AgentCredentialTTL)
}

func (s *Service) issue(ctx context.Context, nodeID, kind string, ttl time.Duration) (string, time.Time, error) {
	if nodeID == "" {
		return "", time.Time{}, fmt.Errorf("credential requires a node identity")
	}
	plaintext, hash, err := NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	credential := &Credential{
		TokenHash: hash,
		NodeID:    nodeID,
		Kind:      kind,
		Status:    StatusActive,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := s.repo.Create(ctx, credential); err != nil {
		return "", time.Time{}, err
	}
	return plaintext, credential.ExpiresAt, nil
}

// RegisterWithBootstrap exchanges a single-use bootstrap credential for a
// long-lived agent credential. This is the Phase 10 secure registration step:
// the control plane verifies that the presented bootstrap token is active,
// unexpired and bound to the node being registered, then invalidates the
// bootstrap credential so it can never be replayed.
func (s *Service) RegisterWithBootstrap(ctx context.Context, bootstrapToken, nodeID string) (agentToken string, expiresAt time.Time, err error) {
	bootstrap, err := s.verify(ctx, bootstrapToken, nodeID)
	if err != nil {
		return "", time.Time{}, err
	}
	if bootstrap.Kind != KindBootstrap {
		return "", time.Time{}, ErrInvalidCredential
	}

	hash := HashToken(bootstrapToken)
	if err := s.repo.UpdateStatus(ctx, hash, StatusUsed, s.now()); err != nil {
		return "", time.Time{}, fmt.Errorf("consuming bootstrap credential: %w", err)
	}

	token, expiresAt, err := s.issueAgentCredential(ctx, nodeID)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// Verify authenticates an agent credential. It returns the stored credential
// when the token is active and unexpired; otherwise one of the package's
// sentinel errors describes why authentication failed.
func (s *Service) Verify(ctx context.Context, token string) (*Credential, error) {
	return s.verify(ctx, token, "")
}

func (s *Service) verify(ctx context.Context, token, expectedNodeID string) (*Credential, error) {
	if token == "" {
		return nil, ErrInvalidCredential
	}
	credential, err := s.repo.GetByTokenHash(ctx, HashToken(token))
	if err != nil {
		// Unknown tokens are indistinguishable from malformed ones.
		return nil, ErrInvalidCredential
	}
	if err := credential.Active(s.now()); err != nil {
		return nil, err
	}
	if expectedNodeID != "" && credential.NodeID != expectedNodeID {
		return nil, ErrWrongNode
	}
	return credential, nil
}

// RecordUse notes successful use of a credential for operational visibility.
// It never fails authentication.
func (s *Service) RecordUse(ctx context.Context, tokenHash string) {
	_ = s.repo.TouchLastUsed(ctx, tokenHash, s.now())
}

// Rotate issues a fresh agent credential for nodeID and revokes every
// previously active agent credential for that node. Rotation is safe to
// repeat: each call simply supersedes the previous generation.
func (s *Service) Rotate(ctx context.Context, nodeID string) (token string, expiresAt time.Time, err error) {
	existing, err := s.repo.ListByNode(ctx, nodeID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("listing credentials for rotation: %w", err)
	}
	now := s.now()
	for _, credential := range existing {
		if credential.Kind == KindAgent && credential.Status == StatusActive {
			if err := s.repo.UpdateStatus(ctx, credential.TokenHash, StatusRevoked, now); err != nil {
				return "", time.Time{}, fmt.Errorf("revoking superseded credential: %w", err)
			}
		}
	}
	return s.issueAgentCredential(ctx, nodeID)
}

// RevokeNode revokes every active credential (bootstrap and agent) issued
// for nodeID. A revoked node can no longer authenticate; this is the kill
// switch used when a node is compromised.
func (s *Service) RevokeNode(ctx context.Context, nodeID string) (revoked int, err error) {
	existing, err := s.repo.ListByNode(ctx, nodeID)
	if err != nil {
		return 0, fmt.Errorf("listing credentials for revocation: %w", err)
	}
	now := s.now()
	count := 0
	for _, credential := range existing {
		if credential.Status != StatusActive {
			continue
		}
		if err := s.repo.UpdateStatus(ctx, credential.TokenHash, StatusRevoked, now); err != nil {
			return count, fmt.Errorf("revoking credential: %w", err)
		}
		count++
	}
	return count, nil
}
