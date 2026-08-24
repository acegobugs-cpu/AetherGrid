package auth

import (
	"context"
	"fmt"
)

// Role is an authorization role assigned to human principals (static API
// keys). Agent and bootstrap principals are node-scoped machine identities
// and are represented separately by PrincipalType.
type Role string

// Roles, ordered from most to least privileged. Only roles that map onto the
// existing API surface exist: admin can destroy, operator can reconcile and
// provision, viewer is read-only.
const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// ValidRole reports whether the given string names a known role.
func ValidRole(value string) bool {
	switch Role(value) {
	case RoleAdmin, RoleOperator, RoleViewer:
		return true
	}
	return false
}

// atLeast reports whether role grants at least the privileges of required.
// admin > operator > viewer.
func (r Role) atLeast(required Role) bool {
	rank := func(role Role) int {
		switch role {
		case RoleAdmin:
			return 3
		case RoleOperator:
			return 2
		case RoleViewer:
			return 1
		default:
			return 0
		}
	}
	return rank(r) >= rank(required)
}

// PrincipalType distinguishes what kind of identity authenticated a request.
type PrincipalType string

const (
	PrincipalAnonymous PrincipalType = "anonymous"
	PrincipalHuman     PrincipalType = "human"
	PrincipalAgent     PrincipalType = "agent"
	PrincipalBootstrap PrincipalType = "bootstrap"
	PrincipalSystem    PrincipalType = "system"
)

// Principal is the authenticated identity attached to a request context by
// the authentication middleware.
type Principal struct {
	Type      PrincipalType
	NodeID    string // set for agent/bootstrap principals
	Role      Role   // set for human principals
	TokenHash string
}

// ID returns a stable, secret-free identifier for logging and audit trails.
func (p *Principal) ID() string {
	if p == nil {
		return "anonymous"
	}
	switch p.Type {
	case PrincipalAgent:
		return "agent:" + p.NodeID
	case PrincipalBootstrap:
		return "bootstrap:" + p.NodeID
	case PrincipalSystem:
		return "system"
	case PrincipalHuman:
		return "role:" + string(p.Role)
	default:
		return "anonymous"
	}
}

// ActorType maps the principal to an audit actor type.
func (p *Principal) ActorType() string {
	if p == nil {
		return string(PrincipalAnonymous)
	}
	return string(p.Type)
}

// HumanWithRole reports whether the principal is a human identity holding at
// least the required role.
func (p *Principal) HumanWithRole(required Role) bool {
	return p != nil && p.Type == PrincipalHuman && p.Role.atLeast(required)
}

// IsAgentSelf reports whether the principal is an agent identity bound to the
// given node. This enforces "authenticated identity == target identity" for
// all node-scoped operations.
func (p *Principal) IsAgentSelf(nodeID string) bool {
	return p != nil && p.Type == PrincipalAgent && p.NodeID == nodeID && p.NodeID != ""
}

type principalKey struct{}

// WithPrincipal attaches an authenticated principal to the context.
func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFrom returns the principal stored in the context, or nil when the
// request was never authenticated (anonymous).
func PrincipalFrom(ctx context.Context) *Principal {
	principal, _ := ctx.Value(principalKey{}).(*Principal)
	return principal
}

// Describe renders a principal for audit lines without leaking secrets.
func (p *Principal) String() string {
	return fmt.Sprintf("%s", p.ID())
}
