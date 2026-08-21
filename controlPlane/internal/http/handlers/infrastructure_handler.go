package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/service"

	"github.com/google/uuid"
)

// InfrastructureHandler exposes the infrastructure provisioning API: lifecycle
// CRUD, asynchronous plan/apply/destroy operations, operation status and
// cancellation, and provisioning metrics.
type InfrastructureHandler struct {
	infrastructures *service.InfrastructureService
	logger          *log.Logger
}

// NewInfrastructureHandler constructs an InfrastructureHandler with the given
// service.
func NewInfrastructureHandler(infrastructures *service.InfrastructureService, logger *log.Logger) *InfrastructureHandler {
	return &InfrastructureHandler{
		infrastructures: infrastructures,
		logger:          logger,
	}
}

// Create handles POST /infrastructure.
func (h *InfrastructureHandler) Create(w http.ResponseWriter, r *http.Request) {
	var request createInfrastructureRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	spec := domain.InfrastructureSpec{
		Name:      strings.TrimSpace(request.Name),
		NodeCount: request.NodeCount,
		CPU:       request.CPU,
		MemoryMB:  request.MemoryMB,
		DiskGB:    request.DiskGB,
		Image:     strings.TrimSpace(request.Image),
		Provider:  strings.TrimSpace(request.Provider),
	}

	infra, err := h.infrastructures.Create(r.Context(), spec)
	if err != nil {
		h.writeServiceError(w, err, "creating infrastructure")
		return
	}

	h.logger.Printf("infrastructure created: id=%s name=%s nodes=%d", infra.ID, infra.Spec.Name, infra.Spec.NodeCount)
	writeJSON(w, http.StatusCreated, newInfrastructureResponse(infra))
}

// List handles GET /infrastructure.
func (h *InfrastructureHandler) List(w http.ResponseWriter, r *http.Request) {
	infrastructures, err := h.infrastructures.List(r.Context())
	if err != nil {
		h.writeServiceError(w, err, "listing infrastructure")
		return
	}

	responses := make([]infrastructureResponse, 0, len(infrastructures))
	for _, infra := range infrastructures {
		responses = append(responses, newInfrastructureResponse(infra))
	}
	writeJSON(w, http.StatusOK, responses)
}

// Get handles GET /infrastructure/{id}.
func (h *InfrastructureHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := infrastructureID(w, r)
	if !ok {
		return
	}

	infra, err := h.infrastructures.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "getting infrastructure")
		return
	}
	writeJSON(w, http.StatusOK, newInfrastructureResponse(infra))
}

// Delete handles DELETE /infrastructure/{id}.
func (h *InfrastructureHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := infrastructureID(w, r)
	if !ok {
		return
	}

	if err := h.infrastructures.Delete(r.Context(), id); err != nil {
		h.writeServiceError(w, err, "deleting infrastructure")
		return
	}
	h.logger.Printf("infrastructure deleted: id=%s", id)
	w.WriteHeader(http.StatusNoContent)
}

// StartPlan handles POST /infrastructure/{id}/plan.
func (h *InfrastructureHandler) StartPlan(w http.ResponseWriter, r *http.Request) {
	h.startOperation(w, r, domain.OperationPlan)
}

// StartApply handles POST /infrastructure/{id}/apply.
func (h *InfrastructureHandler) StartApply(w http.ResponseWriter, r *http.Request) {
	h.startOperation(w, r, domain.OperationApply)
}

// StartDestroy handles POST /infrastructure/{id}/destroy.
func (h *InfrastructureHandler) StartDestroy(w http.ResponseWriter, r *http.Request) {
	h.startOperation(w, r, domain.OperationDestroy)
}

// StartBootstrap handles POST /infrastructure/{id}/bootstrap.
func (h *InfrastructureHandler) StartBootstrap(w http.ResponseWriter, r *http.Request) {
	id, ok := infrastructureID(w, r)
	if !ok {
		return
	}

	op, err := h.infrastructures.StartOperation(r.Context(), id, domain.OperationBootstrap)
	if err != nil {
		h.writeServiceError(w, err, "starting bootstrap operation")
		return
	}

	h.logger.Printf("bootstrap operation started: id=%s infra=%s type=%s", op.ID, op.InfrastructureID, op.Type)
	writeJSON(w, http.StatusAccepted, newOperationResponse(op))
}

// ListOperations handles GET /infrastructure/{id}/operations.
func (h *InfrastructureHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	id, ok := infrastructureID(w, r)
	if !ok {
		return
	}

	operations, err := h.infrastructures.ListOperations(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "listing operations")
		return
	}

	responses := make([]operationResponse, 0, len(operations))
	for _, op := range operations {
		responses = append(responses, newOperationResponse(op))
	}
	writeJSON(w, http.StatusOK, responses)
}

// GetOperation handles GET /operations/{id}.
func (h *InfrastructureHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	op, err := h.infrastructures.GetOperation(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeServiceError(w, err, "getting operation")
		return
	}
	writeJSON(w, http.StatusOK, newOperationResponse(op))
}

// CancelOperation handles POST /operations/{id}/cancel.
func (h *InfrastructureHandler) CancelOperation(w http.ResponseWriter, r *http.Request) {
	op, err := h.infrastructures.CancelOperation(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeServiceError(w, err, "cancelling operation")
		return
	}
	h.logger.Printf("operation cancelled: id=%s", op.ID)
	writeJSON(w, http.StatusOK, newOperationResponse(op))
}

// Metrics handles GET /infrastructure/metrics.
func (h *InfrastructureHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	metrics := h.infrastructures.Metrics()
	writeJSON(w, http.StatusOK, metricsResponse{
		OperationsTotal:     metrics.OperationsTotal.Load(),
		OperationsRunning:   metrics.OperationsRunning.Load(),
		OperationFailures:   metrics.OperationFailures.Load(),
		LastOperationMillis: metrics.LastOperationDuration().Milliseconds(),
	})
}

// startOperation starts an asynchronous plan, apply or destroy operation.
func (h *InfrastructureHandler) startOperation(w http.ResponseWriter, r *http.Request, opType domain.OperationType) {
	id, ok := infrastructureID(w, r)
	if !ok {
		return
	}

	op, err := h.infrastructures.StartOperation(r.Context(), id, opType)
	if err != nil {
		h.writeServiceError(w, err, "starting operation")
		return
	}

	h.logger.Printf("operation started: id=%s infra=%s type=%s", op.ID, op.InfrastructureID, op.Type)
	writeJSON(w, http.StatusAccepted, newOperationResponse(op))
}

// writeServiceError maps infrastructure service errors to HTTP responses.
func (h *InfrastructureHandler) writeServiceError(w http.ResponseWriter, err error, action string) {
	var validation *service.ValidationError
	switch {
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, validation.Message)
	case errors.Is(err, service.ErrOperationInProgress):
		writeError(w, http.StatusConflict, "another operation is already in progress for this infrastructure")
	case service.IsNotFound(err):
		writeError(w, http.StatusNotFound, "not found")
	case service.IsConflict(err):
		writeError(w, http.StatusConflict, err.Error())
	default:
		h.logger.Printf("%s: %v", action, err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func infrastructureID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid infrastructure id %q", id))
		return "", false
	}
	return id, true
}

// createInfrastructureRequest is the JSON payload for POST /infrastructure.
type createInfrastructureRequest struct {
	Name      string `json:"name"`
	NodeCount int    `json:"node_count"`
	CPU       int    `json:"cpu"`
	MemoryMB  int    `json:"memory_mb"`
	DiskGB    int    `json:"disk_gb"`
	Image     string `json:"image"`
	Provider  string `json:"provider"`
}

// infrastructureResponse is the JSON representation of an infrastructure
// deployment.
type infrastructureResponse struct {
	ID            string                      `json:"id"`
	Name          string                      `json:"name"`
	NodeCount     int                         `json:"node_count"`
	CPU           int                         `json:"cpu"`
	MemoryMB      int                         `json:"memory_mb"`
	DiskGB        int                         `json:"disk_gb"`
	Image         string                      `json:"image"`
	Provider      string                      `json:"provider"`
	Phase         string                      `json:"phase"`
	Nodes         []domain.InfrastructureNode `json:"nodes"`
	LastOperation string                      `json:"last_operation"`
	Error         string                      `json:"error"`
	CreatedAt     string                      `json:"created_at"`
	UpdatedAt     string                      `json:"updated_at"`
}

func newInfrastructureResponse(infra *domain.Infrastructure) infrastructureResponse {
	return infrastructureResponse{
		ID:            infra.ID,
		Name:          infra.Spec.Name,
		NodeCount:     infra.Spec.NodeCount,
		CPU:           infra.Spec.CPU,
		MemoryMB:      infra.Spec.MemoryMB,
		DiskGB:        infra.Spec.DiskGB,
		Image:         infra.Spec.Image,
		Provider:      infra.Spec.Provider,
		Phase:         string(infra.Status.Phase),
		Nodes:         infra.Status.Nodes,
		LastOperation: infra.Status.LastOperation,
		Error:         infra.Status.Error,
		CreatedAt:     infra.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     infra.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// operationResponse is the JSON representation of a provisioning operation.
type operationResponse struct {
	ID               string  `json:"id"`
	InfrastructureID string  `json:"infrastructure_id"`
	Type             string  `json:"type"`
	Status           string  `json:"status"`
	ChangesCreate    int     `json:"changes_create"`
	ChangesModify    int     `json:"changes_modify"`
	ChangesDestroy   int     `json:"changes_destroy"`
	StartedAt        *string `json:"started_at"`
	CompletedAt      *string `json:"completed_at"`
	Error            string  `json:"error"`
	CreatedAt        string  `json:"created_at"`
}

func newOperationResponse(op *domain.InfrastructureOperation) operationResponse {
	return operationResponse{
		ID:               op.ID,
		InfrastructureID: op.InfrastructureID,
		Type:             string(op.Type),
		Status:           string(op.Status),
		ChangesCreate:    op.Changes.ToCreate,
		ChangesModify:    op.Changes.ToModify,
		ChangesDestroy:   op.Changes.ToDestroy,
		StartedAt:        formatTime(op.StartedAt),
		CompletedAt:      formatTime(op.CompletedAt),
		Error:            op.Error,
		CreatedAt:        op.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// metricsResponse is the JSON representation of provisioning metrics.
type metricsResponse struct {
	OperationsTotal     int64 `json:"operations_total"`
	OperationsRunning   int64 `json:"operations_running"`
	OperationFailures   int64 `json:"operation_failures"`
	LastOperationMillis int64 `json:"last_operation_millis"`
}
