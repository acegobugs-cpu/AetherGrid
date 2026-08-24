// Package http wires the HTTP handlers and middleware into a single handler
// for the control plane API.
//
// Phase 10 security model: every route declares an access policy, every
// request is authenticated by the auth middleware (anonymous by default and
// rejected by policy), sensitive routes are rate limited, all requests carry
// correlation IDs, and security-relevant outcomes produce audit events.
package http

import (
	"log"
	"net/http"
	"time"

	"AetherGrid/controlPlane/internal/audit"
	"AetherGrid/controlPlane/internal/auth"
	"AetherGrid/controlPlane/internal/http/handlers"
	"AetherGrid/controlPlane/internal/http/middleware"
	"AetherGrid/controlPlane/internal/service"
)

// Security carries the Phase 10 security components the router needs.
type Security struct {
	// Credentials issues and verifies node credentials.
	Credentials *auth.Service
	// StaticKeys authenticates human API keys. May be nil in tests.
	StaticKeys *auth.StaticKeyStore
	// Auditor records security events.
	Auditor *audit.Logger
	// OpenRegistration allows anonymous node self-registration. It is only
	// valid in development deployments; production validation refuses it.
	OpenRegistration bool
	// TLSEnabled enables HSTS and is reported by startup validation.
	TLSEnabled bool
	// Rate limits per minute for sensitive route classes. Zero selects
	// defaults (30/min registration-class, 120/min mutating).
	RegisterRatePerMinute int
	MutationRatePerMinute int
}

// Services bundles the service-layer dependencies of the router.
type Services struct {
	Nodes           *service.NodeService
	Heartbeats      *service.HeartbeatService
	Reconciler      *service.ReconciliationService
	Commands        *service.CommandService
	Infrastructures *service.InfrastructureService
	Clusters        *service.ClusterService
}

// NewRouter assembles the complete HTTP handler for the control plane API.
func NewRouter(services Services, security Security, logger *log.Logger) http.Handler {
	nodeHandler := handlers.NewNodeHandler(handlers.NodeHandlerDeps{
		Nodes:       services.Nodes,
		Heartbeats:  services.Heartbeats,
		Credentials: security.Credentials,
		Auditor:     security.Auditor,
		Logger:      logger,
	})
	commandHandler := handlers.NewCommandHandler(services.Commands, logger)
	reconciliationHandler := handlers.NewReconciliationHandler(services.Reconciler, security.Auditor, logger)
	kubernetesHandler := handlers.NewKubernetesHandler(services.Nodes, services.Commands, logger)
	infrastructureHandler := handlers.NewInfrastructureHandler(services.Infrastructures, security.Auditor, logger)
	clusterHandler := handlers.NewClusterHandler(services.Clusters, logger)

	mux := http.NewServeMux()
	authz := middleware.NewAuthorize(security.Auditor)

	// guard combines authorization, optional rate limiting and handler into
	// one registration step so no route can accidentally skip its policy.
	guard := func(pattern string, policy middleware.Policy, limiter *middleware.RateLimiter, handler http.HandlerFunc) {
		wrapped := authz.Require(policy, handler)
		if limiter != nil {
			wrapped = middleware.RateLimited(limiter, wrapped)
		}
		mux.HandleFunc(pattern, wrapped)
	}

	var (
		viewer   = middleware.Viewer()
		operator = middleware.Operator()
		admin    = middleware.Admin()
		self     = middleware.AgentSelf()
		selfOrOp = middleware.AgentOrOperator()
	)

	// Sensitive-route rate limiters. Registration endpoints are stricter:
	// they are the only place an unauthenticated caller can obtain a
	// credential, so abuse there must be expensive.
	registerLimiter := middleware.NewRateLimiter(security.RegisterRatePerMinute, time.Minute)
	mutatingLimiter := middleware.NewRateLimiter(security.MutationRatePerMinute, time.Minute)

	createNodePolicy := admin
	if security.OpenRegistration {
		// Development-only escape hatch: explicit configuration enables
		// anonymous self-registration (still rate limited). Production
		// startup validation refuses this mode.
		createNodePolicy = middleware.Public()
	}
	guard("POST /nodes", createNodePolicy, registerLimiter, func(w http.ResponseWriter, r *http.Request) {
		nodeHandler.Create(w, r)
	})

	guard("GET /nodes", viewer, nil, nodeHandler.List)
	guard("GET /nodes/{id}", viewer, nil, nodeHandler.Get)
	guard("DELETE /nodes/{id}", admin, mutatingLimiter, nodeHandler.Delete)

	guard("POST /nodes/{id}/heartbeat", self, mutatingLimiter, nodeHandler.Heartbeat)

	guard("GET /nodes/{id}/state", viewer, nil, nodeHandler.State)
	guard("PUT /nodes/{id}/state", self, mutatingLimiter, nodeHandler.SetState)
	guard("GET /nodes/{id}/desired-state", viewer, nil, nodeHandler.DesiredState)
	guard("PUT /nodes/{id}/desired-state", operator, mutatingLimiter, nodeHandler.SetDesiredState)

	guard("GET /nodes/{id}/kubernetes", viewer, nil, kubernetesHandler.State)
	guard("GET /nodes/{id}/kubernetes/nodes", viewer, nil, kubernetesHandler.ListNodes)
	guard("GET /nodes/{id}/kubernetes/pods", viewer, nil, kubernetesHandler.ListPods)

	// Secure registration: a bootstrap credential authenticates the exchange;
	// the bootstrap principal is bound to exactly one node.
	guard("POST /nodes/{id}/register", middleware.Bootstrap(), registerLimiter, nodeHandler.Register)

	// Credential lifecycle: agents rotate their own credentials, admins may
	// rotate or revoke any node's credentials.
	guard("POST /nodes/{id}/credentials/rotate",
		middleware.Policy{MinRole: auth.RoleAdmin, AllowAgent: true, AgentSelfOnly: true},
		mutatingLimiter, nodeHandler.RotateCredentials)
	guard("DELETE /nodes/{id}/credentials", admin, mutatingLimiter, nodeHandler.RevokeCredentials)

	guard("POST /nodes/{id}/reconcile", operator, mutatingLimiter, reconciliationHandler.Reconcile)
	guard("GET /nodes/{id}/reconciliation", viewer, nil, reconciliationHandler.State)
	guard("GET /nodes/{id}/reconciliation/history", viewer, nil, reconciliationHandler.History)
	guard("GET /reconciliation/status", viewer, nil, reconciliationHandler.Status)

	// Phase 9 cluster health and recovery endpoints.
	guard("GET /clusters/{id}/health", viewer, nil, reconciliationHandler.ClusterHealth)
	guard("GET /clusters/{id}/reconciliation", viewer, nil, reconciliationHandler.ClusterReconciliation)
	guard("GET /clusters/{id}/recovery", viewer, nil, reconciliationHandler.ClusterRecovery)
	guard("POST /clusters/{id}/reconcile", operator, mutatingLimiter, reconciliationHandler.ClusterReconcile)
	guard("POST /clusters/{id}/recovery/reset", operator, mutatingLimiter, reconciliationHandler.ResetNodeRecovery)

	guard("POST /nodes/{id}/commands", operator, mutatingLimiter, commandHandler.Create)
	guard("GET /nodes/{id}/commands", selfOrOp, nil, commandHandler.List)
	guard("POST /nodes/{id}/commands/{command_id}/result", self, mutatingLimiter, commandHandler.ReportResult)

	guard("POST /infrastructure", admin, mutatingLimiter, infrastructureHandler.Create)
	guard("GET /infrastructure", viewer, nil, infrastructureHandler.List)
	guard("GET /infrastructure/{id}", viewer, nil, infrastructureHandler.Get)
	guard("DELETE /infrastructure/{id}", admin, mutatingLimiter, infrastructureHandler.Delete)
	guard("GET /infrastructure/{id}/operations", viewer, nil, infrastructureHandler.ListOperations)
	guard("POST /infrastructure/{id}/plan", operator, mutatingLimiter, infrastructureHandler.StartPlan)
	guard("POST /infrastructure/{id}/apply", operator, mutatingLimiter, infrastructureHandler.StartApply)
	guard("POST /infrastructure/{id}/destroy", admin, mutatingLimiter, infrastructureHandler.StartDestroy)
	guard("POST /infrastructure/{id}/bootstrap", operator, mutatingLimiter, infrastructureHandler.StartBootstrap)
	guard("GET /clusters", viewer, nil, infrastructureHandler.List)
	guard("GET /clusters/{id}", viewer, nil, clusterHandler.Get)
	guard("POST /clusters", operator, mutatingLimiter, clusterHandler.Create)
	guard("POST /clusters/{id}/bootstrap", operator, mutatingLimiter, clusterHandler.Bootstrap)
	guard("GET /clusters/{id}/status", viewer, nil, clusterHandler.Status)

	guard("GET /operations/{id}", viewer, nil, infrastructureHandler.GetOperation)
	guard("POST /operations/{id}/cancel", operator, mutatingLimiter, infrastructureHandler.CancelOperation)

	return assemble(logger, security, mux)
}

// assemble layers the middleware chain around the routed mux. Context values
// flow inward, so the access log sits directly above the mux where it can
// observe both the correlation ID and the authenticated principal.
func assemble(logger *log.Logger, security Security, mux *http.ServeMux) http.Handler {
	handler := middleware.Log(logger, mux)
	handler = middleware.Authenticate(security.Credentials, security.StaticKeys, security.Auditor)(handler)
	handler = middleware.RequestID(handler)
	handler = middleware.LimitBody(middleware.DefaultMaxBodyBytes, handler)
	handler = middleware.SecureHeaders(security.TLSEnabled, handler)
	return middleware.Recover(logger, handler)
}
