package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"AetherGrid/controlPlane/internal/audit"
	"AetherGrid/controlPlane/internal/auth"
	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/http/middleware"
	"AetherGrid/controlPlane/internal/service"

	"github.com/google/uuid"
)

// ReconciliationHandler exposes the reconciliation endpoints over HTTP.
type ReconciliationHandler struct {
	reconciler *service.ReconciliationService
	auditor    *audit.Logger
	logger     *log.Logger
}

// NewReconciliationHandler constructs a ReconciliationHandler.
func NewReconciliationHandler(reconciler *service.ReconciliationService, auditor *audit.Logger, logger *log.Logger) *ReconciliationHandler {
	return &ReconciliationHandler{reconciler: reconciler, auditor: auditor, logger: logger}
}

// Reconcile handles POST /nodes/{id}/reconcile. It runs one full reconciliation
// cycle synchronously and returns the structured result.
func (h *ReconciliationHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}
	h.auditTrigger(r, "node:"+id)

	result, err := h.reconciler.Reconcile(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "reconciling node")
		return
	}

	h.logger.Printf("reconciliation: id=%s result=%s action=%s attempt=%d",
		id, result.Result, result.Action, result.Attempt)
	writeJSON(w, http.StatusOK, result)
}

// State handles GET /nodes/{id}/reconciliation.
func (h *ReconciliationHandler) State(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	node, err := h.reconciler.ReconciliationState(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "getting reconciliation state")
		return
	}
	writeJSON(w, http.StatusOK, reconciliationStateResponse{
		NodeID:                       node.ID,
		LastReconciliation:           formatTime(node.LastReconciliation),
		LastSuccessfulReconciliation: formatTime(node.LastSuccessfulReconciliation),
		Result:                       string(node.LastReconciliationResult),
		Action:                       node.LastReconciliationAction,
		Error:                        node.LastReconciliationError,
		LastReconciliationDeadline:   formatTime(node.LastReconciliationDeadline),
		Attempts:                     node.ReconciliationAttempts,
	})
}

// History handles GET /nodes/{id}/reconciliation/history.
func (h *ReconciliationHandler) History(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	events, err := h.reconciler.History(r.Context(), id, limit)
	if err != nil {
		h.writeServiceError(w, err, "listing reconciliation history")
		return
	}
	if events == nil {
		events = []*domain.ReconciliationEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

// Status handles GET /reconciliation/status.
func (h *ReconciliationHandler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.reconciler.Status())
}

// ClusterHealth handles GET /clusters/{id}/health (Phase 9 #98): the
// aggregate desired-vs-actual health of a managed cluster.
func (h *ReconciliationHandler) ClusterHealth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	health, err := h.reconciler.ClusterHealth(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "cluster health")
		return
	}
	writeJSON(w, http.StatusOK, health)
}

// ClusterReconciliation handles GET /clusters/{id}/reconciliation: the
// per-member reconciliation state of the cluster.
func (h *ReconciliationHandler) ClusterReconciliation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	members, err := h.reconciler.ClusterRecovery(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "cluster reconciliation")
		return
	}
	writeJSON(w, http.StatusOK, members)
}

// ClusterRecovery handles GET /clusters/{id}/recovery (Phase 9 #98).
func (h *ReconciliationHandler) ClusterRecovery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	members, err := h.reconciler.ClusterRecovery(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "cluster recovery")
		return
	}
	writeJSON(w, http.StatusOK, members)
}

// ClusterReconcile handles POST /clusters/{id}/reconcile (Phase 9 #99):
// manual reconciliation of every member. It goes through the same
// observe/compare/plan/execute/verify path and enforces the same policies,
// locks and confirmation thresholds as automatic reconciliation.
func (h *ReconciliationHandler) ClusterReconcile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.auditTrigger(r, "cluster:"+id)
	results, err := h.reconciler.ReconcileCluster(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "cluster reconcile")
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// ResetNodeRecovery handles POST /clusters/{id}/recovery/reset (Phase 9 #100):
// an authorized operator clears the circuit breaker so reconciliation may
// evaluate the failure again. It never executes recovery itself.
func (h *ReconciliationHandler) ResetNodeRecovery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(body.NodeID)); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid node id %q", body.NodeID))
		return
	}
	h.auditTrigger(r, "recovery:"+strings.TrimSpace(body.NodeID))
	if err := h.reconciler.ResetNodeRecovery(r.Context(), strings.TrimSpace(body.NodeID)); err != nil {
		h.writeServiceError(w, err, "recovery reset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset", "node_id": body.NodeID})
}

// auditTrigger records who triggered a reconciliation or recovery operation.
func (h *ReconciliationHandler) auditTrigger(r *http.Request, resource string) {
	principal := auth.PrincipalFrom(r.Context())
	if h.auditor != nil {
		h.auditor.Record(r.Context(), audit.Event{
			Operation: audit.OpReconcileTriggered,
			Actor:     principal.ID(),
			ActorType: principal.ActorType(),
			Resource:  resource,
			RequestID: middleware.RequestIDFrom(r.Context()),
			Source:    middleware.SourceAddress(r),
		})
		return
	}
	h.logger.Printf("AUDIT operation=%s actor=%s actor_type=%s resource=%q result=%s request_id=%s source=%s",
		audit.OpReconcileTriggered, principal.ID(), principal.ActorType(), resource,
		audit.ResultSuccess, middleware.RequestIDFrom(r.Context()), middleware.SourceAddress(r))
}

// writeServiceError maps service/repository errors to consistent HTTP
// responses. Internal errors are logged and never exposed to clients.
func (h *ReconciliationHandler) writeServiceError(w http.ResponseWriter, err error, action string) {
	switch {
	case service.IsNotFound(err):
		writeError(w, http.StatusNotFound, "node not found")
	case service.IsConflict(err):
		writeError(w, http.StatusConflict, err.Error())
	default:
		h.logger.Printf("%s: %v", action, err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// reconciliationStateResponse is the JSON representation of a node's
// reconciliation metadata.
type reconciliationStateResponse struct {
	NodeID                       string  `json:"node_id"`
	LastReconciliation           *string `json:"last_reconciliation"`
	LastSuccessfulReconciliation *string `json:"last_successful_reconciliation"`
	Result                       string  `json:"result"`
	Action                       string  `json:"action,omitempty"`
	Error                        string  `json:"error,omitempty"`
	LastReconciliationDeadline   *string `json:"last_reconciliation_deadline,omitempty"`
	Attempts                     int     `json:"attempts"`
}
