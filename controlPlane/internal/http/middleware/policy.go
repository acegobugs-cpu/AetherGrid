package middleware

import (
	"net/http"

	"AetherGrid/controlPlane/internal/auth"
)

// Policy expresses one route's access requirements. The zero value denies
// everything except explicitly public routes, so new endpoints are secure by
// default.
//
// Human principals need at least MinRole. Agent principals are admitted only
// when AllowAgent is set and, when AgentSelfOnly is set, only for their own
// node (the {id} path parameter must equal the authenticated identity).
// Bootstrap principals exist solely to exchange themselves for an agent
// credential on the registration route.
type Policy struct {
	MinRole        auth.Role
	AllowAgent     bool
	AgentSelfOnly  bool
	AllowBootstrap bool
	Public         bool
}

// Allows returns an empty string when the request may proceed, otherwise a
// short reason describing the denial (recorded in audit events, safe to
// expose as it contains no secret material).
func (p Policy) Allows(principal *auth.Principal, r *http.Request) string {
	if p.Public {
		return ""
	}
	if principal == nil || principal.Type == auth.PrincipalAnonymous {
		return "authentication required"
	}

	switch {
	case principal.HumanWithRole(p.MinRole):
		return ""

	case principal.Type == auth.PrincipalAgent:
		if !p.AllowAgent {
			return "agents are not permitted on this endpoint"
		}
		if p.AgentSelfOnly && !principal.IsAgentSelf(r.PathValue("id")) {
			return "agents may only access their own node"
		}
		return ""

	case principal.Type == auth.PrincipalBootstrap:
		if !p.AllowBootstrap {
			return "bootstrap credentials may only be used for registration"
		}
		return ""

	default:
		return "insufficient role"
	}
}

// Convenience policies matching the permission matrix.

// Public permits any caller (used with explicit dev-mode registration only).
func Public() Policy { return Policy{Public: true} }

// Viewer permits read-only human access.
func Viewer() Policy { return Policy{MinRole: auth.RoleViewer} }

// Operator permits human reconciliation/provisioning operations.
func Operator() Policy { return Policy{MinRole: auth.RoleOperator} }

// Admin permits destructive operations.
func Admin() Policy { return Policy{MinRole: auth.RoleAdmin} }

// AgentSelf admits agent identities scoped to their own node only.
func AgentSelf() Policy {
	return Policy{MinRole: auth.RoleAdmin, AllowAgent: true, AgentSelfOnly: true}
}

// AgentOrOperator admits operators/administrators plus self-scoped agents
// (e.g. listing a node's pending commands: the agent lists its own, humans
// may list anyone's).
func AgentOrOperator() Policy {
	return Policy{MinRole: auth.RoleOperator, AllowAgent: true, AgentSelfOnly: true}
}

// Bootstrap admits bootstrap principals exchanging credentials.
func Bootstrap() Policy { return Policy{AllowBootstrap: true} }
