package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"
)

// ReconciliationHandler exposes the reconciliation endpoints over HTTP.
type ReconciliationHandler struct {
	reconciler *service.ReconciliationService
	logger     *log.Logger
}

// NewReconciliationHandler constructs a ReconciliationHandler.
func NewReconciliationHandler(reconciler *service.ReconciliationService, logger *log.Logger) *ReconciliationHandler {
	return &ReconciliationHandler{reconciler: reconciler, logger: logger}
}

// Reconcile handles POST /nodes/{id}/reconcile. It runs one full reconciliation
// cycle synchronously and returns the structured result.
func (h *ReconciliationHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

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
