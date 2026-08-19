// Package http wires the HTTP handlers and middleware into a single handler
// for the control plane API.
package http

import (
	"log"
	"net/http"

	"github.com/acegobugs-cpu/AetherGrid/internal/http/handlers"
	"github.com/acegobugs-cpu/AetherGrid/internal/http/middleware"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"
)

// NewRouter assembles the complete HTTP handler for the control plane API.
func NewRouter(
	nodes *service.NodeService,
	heartbeats *service.HeartbeatService,
	reconciler *service.ReconciliationService,
	commands *service.CommandService,
	infrastructures *service.InfrastructureService,
	logger *log.Logger,
) http.Handler {
	nodeHandler := handlers.NewNodeHandler(nodes, heartbeats, logger)
	commandHandler := handlers.NewCommandHandler(commands, logger)
	reconciliationHandler := handlers.NewReconciliationHandler(reconciler, logger)
	kubernetesHandler := handlers.NewKubernetesHandler(nodes, commands, logger)
	infrastructureHandler := handlers.NewInfrastructureHandler(infrastructures, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /nodes", nodeHandler.Create)
	mux.HandleFunc("GET /nodes", nodeHandler.List)
	mux.HandleFunc("GET /nodes/{id}", nodeHandler.Get)
	mux.HandleFunc("DELETE /nodes/{id}", nodeHandler.Delete)

	mux.HandleFunc("POST /nodes/{id}/heartbeat", nodeHandler.Heartbeat)

	mux.HandleFunc("GET /nodes/{id}/state", nodeHandler.State)
	mux.HandleFunc("PUT /nodes/{id}/state", nodeHandler.SetState)
	mux.HandleFunc("GET /nodes/{id}/desired-state", nodeHandler.DesiredState)
	mux.HandleFunc("PUT /nodes/{id}/desired-state", nodeHandler.SetDesiredState)

	mux.HandleFunc("GET /nodes/{id}/kubernetes", kubernetesHandler.State)
	mux.HandleFunc("GET /nodes/{id}/kubernetes/nodes", kubernetesHandler.ListNodes)
	mux.HandleFunc("GET /nodes/{id}/kubernetes/pods", kubernetesHandler.ListPods)

	mux.HandleFunc("POST /nodes/{id}/reconcile", reconciliationHandler.Reconcile)
	mux.HandleFunc("GET /nodes/{id}/reconciliation", reconciliationHandler.State)
	mux.HandleFunc("GET /nodes/{id}/reconciliation/history", reconciliationHandler.History)
	mux.HandleFunc("GET /reconciliation/status", reconciliationHandler.Status)

	mux.HandleFunc("POST /nodes/{id}/commands", commandHandler.Create)
	mux.HandleFunc("GET /nodes/{id}/commands", commandHandler.List)
	mux.HandleFunc("POST /nodes/{id}/commands/{command_id}/result", commandHandler.ReportResult)

	mux.HandleFunc("POST /infrastructure", infrastructureHandler.Create)
	mux.HandleFunc("GET /infrastructure", infrastructureHandler.List)
	mux.HandleFunc("GET /infrastructure/{id}", infrastructureHandler.Get)
	mux.HandleFunc("DELETE /infrastructure/{id}", infrastructureHandler.Delete)
	mux.HandleFunc("GET /infrastructure/{id}/operations", infrastructureHandler.ListOperations)
	mux.HandleFunc("POST /infrastructure/{id}/plan", infrastructureHandler.StartPlan)
	mux.HandleFunc("POST /infrastructure/{id}/apply", infrastructureHandler.StartApply)
	mux.HandleFunc("POST /infrastructure/{id}/destroy", infrastructureHandler.StartDestroy)
	mux.HandleFunc("POST /infrastructure/{id}/bootstrap", infrastructureHandler.StartBootstrap)
	mux.HandleFunc("GET /infrastructure/metrics", infrastructureHandler.Metrics)

	mux.HandleFunc("GET /operations/{id}", infrastructureHandler.GetOperation)
	mux.HandleFunc("POST /operations/{id}/cancel", infrastructureHandler.CancelOperation)

	return middleware.Log(logger, middleware.Recover(logger, mux))
}
