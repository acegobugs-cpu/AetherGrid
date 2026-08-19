// Package handlers implements the HTTP transport for the control plane API.
// Handlers parse requests, validate their shape, call services and convert
// results into responses. Business logic lives in the service layer, not here.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"

	"github.com/google/uuid"
)

// NodeHandler exposes the node lifecycle, heartbeat, state and reconciliation
// endpoints over HTTP.
type NodeHandler struct {
	nodes      *service.NodeService
	heartbeats *service.HeartbeatService
	reconciler *service.ReconciliationService
	logger     *log.Logger
}

// NewNodeHandler constructs a NodeHandler with the given services.
func NewNodeHandler(
	nodes *service.NodeService,
	heartbeats *service.HeartbeatService,
	reconciler *service.ReconciliationService,
	logger *log.Logger,
) *NodeHandler {
	return &NodeHandler{
		nodes:      nodes,
		heartbeats: heartbeats,
		reconciler: reconciler,
		logger:     logger,
	}
}

// Create handles POST /nodes.
func (h *NodeHandler) Create(w http.ResponseWriter, r *http.Request) {
	var request createNodeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	node, err := h.nodes.Create(r.Context(), service.CreateNodeInput{
		Name:              strings.TrimSpace(request.Name),
		Location:          strings.TrimSpace(request.Location),
		IPAddress:         strings.TrimSpace(request.IPAddress),
		KubernetesEnabled: request.KubernetesEnabled,
		WireGuardEnabled:  request.WireGuardEnabled,
	})
	if err != nil {
		h.writeServiceError(w, err, "creating node")
		return
	}

	h.logger.Printf("node registered: id=%s name=%s status=%s", node.ID, node.Name, node.Status)
	writeJSON(w, http.StatusCreated, newNodeResponse(node))
}

// List handles GET /nodes.
func (h *NodeHandler) List(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.nodes.List(r.Context())
	if err != nil {
		h.writeServiceError(w, err, "listing nodes")
		return
	}

	responses := make([]nodeResponse, 0, len(nodes))
	for _, node := range nodes {
		responses = append(responses, newNodeResponse(node))
	}
	writeJSON(w, http.StatusOK, responses)
}

// Get handles GET /nodes/{id}.
func (h *NodeHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	node, err := h.nodes.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "getting node")
		return
	}
	writeJSON(w, http.StatusOK, newNodeResponse(node))
}

// Delete handles DELETE /nodes/{id}.
func (h *NodeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	if err := h.nodes.Delete(r.Context(), id); err != nil {
		h.writeServiceError(w, err, "deleting node")
		return
	}
	h.logger.Printf("node deleted: id=%s", id)
	w.WriteHeader(http.StatusNoContent)
}

// Heartbeat handles POST /nodes/{id}/heartbeat.
func (h *NodeHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	node, err := h.heartbeats.Record(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "recording heartbeat")
		return
	}

	h.logger.Printf("heartbeat received: id=%s", id)
	writeJSON(w, http.StatusOK, newNodeResponse(node))
}

// State handles GET /nodes/{id}/state.
func (h *NodeHandler) State(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	node, err := h.nodes.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "getting node state")
		return
	}
	writeJSON(w, http.StatusOK, stateResponse{
		NodeID:        node.ID,
		Status:        string(node.Status),
		LastHeartbeat: formatTime(node.LastHeartbeat),
	})
}

// DesiredState handles GET /nodes/{id}/desired-state.
func (h *NodeHandler) DesiredState(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	node, err := h.nodes.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "getting desired state")
		return
	}
	writeJSON(w, http.StatusOK, desiredStateResponse{
		NodeID:        node.ID,
		DesiredStatus: string(node.DesiredStatus),
	})
}

// SetDesiredState handles PUT /nodes/{id}/desired-state.
func (h *NodeHandler) SetDesiredState(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	var request setDesiredStateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	status := domain.NodeStatus(strings.TrimSpace(strings.ToUpper(request.Status)))
	if !status.Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status %q", request.Status))
		return
	}

	node, err := h.nodes.SetDesiredStatus(r.Context(), id, status)
	if err != nil {
		h.writeServiceError(w, err, "setting desired state")
		return
	}

	h.logger.Printf("desired state updated: id=%s desired_status=%s", id, node.DesiredStatus)
	writeJSON(w, http.StatusOK, desiredStateResponse{
		NodeID:        node.ID,
		DesiredStatus: string(node.DesiredStatus),
	})
}

// Reconcile handles POST /nodes/{id}/reconcile.
func (h *NodeHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	result, err := h.reconciler.Reconcile(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "reconciling node")
		return
	}

	h.logger.Printf("reconciliation: id=%s desired=%s actual=%s result=%s",
		id, result.DesiredState, result.ActualState, result.Result)
	writeJSON(w, http.StatusOK, result)
}

// writeServiceError maps service/repository errors to consistent HTTP
// responses. Internal errors are logged and never exposed to clients.
func (h *NodeHandler) writeServiceError(w http.ResponseWriter, err error, action string) {
	var validation *service.ValidationError
	switch {
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, validation.Message)
	case service.IsNotFound(err):
		writeError(w, http.StatusNotFound, "node not found")
	case service.IsConflict(err):
		writeError(w, http.StatusConflict, err.Error())
	default:
		h.logger.Printf("%s: %v", action, err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func nodeID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid node id %q", id))
		return "", false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request body: %v", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// Header already written; nothing more to do here.
		return
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func formatTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

// createNodeRequest is the JSON payload for POST /nodes.
type createNodeRequest struct {
	Name              string `json:"name"`
	Location          string `json:"location"`
	IPAddress         string `json:"ip_address"`
	KubernetesEnabled bool   `json:"kubernetes_enabled"`
	WireGuardEnabled  bool   `json:"wireguard_enabled"`
}

// setDesiredStateRequest is the JSON payload for PUT /nodes/{id}/desired-state.
type setDesiredStateRequest struct {
	Status string `json:"status"`
}

// nodeResponse is the JSON representation of a node.
type nodeResponse struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Status            string  `json:"status"`
	DesiredStatus     string  `json:"desired_status"`
	Location          string  `json:"location"`
	IPAddress         string  `json:"ip_address"`
	KubernetesEnabled bool    `json:"kubernetes_enabled"`
	WireGuardEnabled  bool    `json:"wireguard_enabled"`
	LastHeartbeat     *string `json:"last_heartbeat"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

func newNodeResponse(node *domain.Node) nodeResponse {
	return nodeResponse{
		ID:                node.ID,
		Name:              node.Name,
		Status:            string(node.Status),
		DesiredStatus:     string(node.DesiredStatus),
		Location:          node.Location,
		IPAddress:         node.IPAddress,
		KubernetesEnabled: node.KubernetesEnabled,
		WireGuardEnabled:  node.WireGuardEnabled,
		LastHeartbeat:     formatTime(node.LastHeartbeat),
		CreatedAt:         node.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:         node.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// stateResponse is the JSON representation of a node's actual state.
type stateResponse struct {
	NodeID        string  `json:"node_id"`
	Status        string  `json:"status"`
	LastHeartbeat *string `json:"last_heartbeat"`
}

// desiredStateResponse is the JSON representation of a node's desired state.
type desiredStateResponse struct {
	NodeID        string `json:"node_id"`
	DesiredStatus string `json:"desired_status"`
}
