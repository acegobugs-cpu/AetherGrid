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

	"AetherGrid/controlPlane/internal/audit"
	"AetherGrid/controlPlane/internal/auth"
	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/http/middleware"
	"AetherGrid/controlPlane/internal/service"

	"github.com/google/uuid"
)

// NodeHandler exposes the node lifecycle, heartbeat, state, desired-state,
// secure registration and credential lifecycle endpoints over HTTP.
type NodeHandler struct {
	nodes       *service.NodeService
	heartbeats  *service.HeartbeatService
	credentials *auth.Service
	auditor     *audit.Logger
	logger      *log.Logger
}

// NodeHandlerDeps carries the handler's dependencies.
type NodeHandlerDeps struct {
	Nodes       *service.NodeService
	Heartbeats  *service.HeartbeatService
	Credentials *auth.Service
	Auditor     *audit.Logger
	Logger      *log.Logger
}

// NewNodeHandler constructs a NodeHandler with the given services.
func NewNodeHandler(deps NodeHandlerDeps) *NodeHandler {
	return &NodeHandler{
		nodes:       deps.Nodes,
		heartbeats:  deps.Heartbeats,
		credentials: deps.Credentials,
		auditor:     deps.Auditor,
		logger:      deps.Logger,
	}
}

// Create handles POST /nodes. The node record is provisioned by an
// authorized operator (or, in explicitly configured development mode, by the
// agent itself) and a single-use bootstrap credential is issued. The
// bootstrap token is returned exactly once and never persisted in plaintext.
func (h *NodeHandler) Create(w http.ResponseWriter, r *http.Request) {
	var request createNodeRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	if err := validateNodeName(request.Name); err != nil {
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

	response := newNodeResponse(node)

	if h.credentials != nil {
		token, expiresAt, err := h.credentials.IssueBootstrap(r.Context(), node.ID)
		if err != nil {
			h.logger.Printf("issuing bootstrap credential failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		response.BootstrapToken = token
		expires := expiresAt.UTC().Format(time.RFC3339)
		response.BootstrapExpiresAt = &expires

		principal := auth.PrincipalFrom(r.Context())
		h.auditEvent(r, audit.Event{
			Operation: audit.OpCredentialIssued,
			Actor:     principal.ID(),
			ActorType: principal.ActorType(),
			Resource:  "bootstrap_credential:" + node.ID,
			RequestID: middleware.RequestIDFrom(r.Context()),
			Source:    middleware.SourceAddress(r),
		})
	}
	h.auditEvent(r, audit.Event{
		Operation: audit.OpNodeRegistered,
		Actor:     auth.PrincipalFrom(r.Context()).ID(),
		ActorType: auth.PrincipalFrom(r.Context()).ActorType(),
		Resource:  "node:" + node.ID,
		RequestID: middleware.RequestIDFrom(r.Context()),
		Source:    middleware.SourceAddress(r),
	})

	h.logger.Printf("node registered: id=%s name=%s status=%s", node.ID, node.Name, node.Status)
	writeJSON(w, http.StatusCreated, response)
}

// Register handles POST /nodes/{id}/register: the Phase 10 secure
// registration exchange. A bootstrap credential authenticates the request;
// the control plane verifies it is active, unused, unexpired and bound to the
// target node, consumes it, and issues the long-lived agent credential. The
// agent credential is shown exactly once.
func (h *NodeHandler) Register(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	principal := auth.PrincipalFrom(r.Context())
	if principal == nil || (principal.Type != auth.PrincipalBootstrap && principal.Type != auth.PrincipalAgent) {
		writeError(w, http.StatusUnauthorized, "authentication failed")
		return
	}
	if principal.NodeID != id {
		h.auditEvent(r, audit.Event{
			Operation: audit.OpAuthorizationDenied,
			Actor:     principal.ID(),
			ActorType: principal.ActorType(),
			Resource:  "node:" + id,
			Result:    audit.ResultDenied,
			Reason:    "bootstrap credential bound to another node",
			RequestID: middleware.RequestIDFrom(r.Context()),
			Source:    middleware.SourceAddress(r),
		})
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Optional metadata payload; agents may re-declare their attributes.
	var request createNodeRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &request) {
			return
		}
	}

	node, err := h.nodes.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "registering node")
		return
	}

	agentToken, expiresAt, err := h.credentials.RegisterWithBootstrap(r.Context(), middleware.BearerToken(r), id)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrWrongNode):
			h.auditEvent(r, audit.Event{
				Operation: audit.OpAuthorizationDenied,
				Actor:     principal.ID(),
				ActorType: principal.ActorType(),
				Resource:  "node:" + id,
				Result:    audit.ResultDenied,
				Reason:    "credential not valid for this node",
				RequestID: middleware.RequestIDFrom(r.Context()),
				Source:    middleware.SourceAddress(r),
			})
			writeError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, auth.ErrUsedCredential):
			h.auditEvent(r, audit.Event{
				Operation: audit.OpAuthenticationFailed,
				Actor:     principal.ID(),
				ActorType: principal.ActorType(),
				Result:    audit.ResultFailure,
				Reason:    "bootstrap credential replay",
				RequestID: middleware.RequestIDFrom(r.Context()),
				Source:    middleware.SourceAddress(r),
			})
			writeError(w, http.StatusUnauthorized, "authentication failed")
		case errors.Is(err, auth.ErrExpiredCredential):
			h.auditEvent(r, audit.Event{
				Operation: audit.OpAuthenticationFailed,
				Actor:     principal.ID(),
				ActorType: principal.ActorType(),
				Result:    audit.ResultFailure,
				Reason:    "bootstrap credential expired",
				RequestID: middleware.RequestIDFrom(r.Context()),
				Source:    middleware.SourceAddress(r),
			})
			writeError(w, http.StatusUnauthorized, "authentication failed")
		default:
			h.writeServiceError(w, err, "registering node")
		}
		return
	}

	expires := expiresAt.UTC().Format(time.RFC3339)
	h.auditEvent(r, audit.Event{
		Operation: audit.OpNodeRegistered,
		Actor:     "bootstrap:" + id,
		ActorType: string(auth.PrincipalBootstrap),
		Resource:  "node:" + id,
		RequestID: middleware.RequestIDFrom(r.Context()),
		Source:    middleware.SourceAddress(r),
	})
	h.auditEvent(r, audit.Event{
		Operation: audit.OpCredentialIssued,
		Actor:     "bootstrap:" + id,
		ActorType: string(auth.PrincipalBootstrap),
		Resource:  "agent_credential:" + id,
		RequestID: middleware.RequestIDFrom(r.Context()),
		Source:    middleware.SourceAddress(r),
	})

	h.logger.Printf("secure registration complete: id=%s name=%s", node.ID, node.Name)
	writeJSON(w, http.StatusOK, registerResponse{
		NodeID:              node.ID,
		Status:              string(node.Status),
		Credential:          agentToken,
		CredentialExpiresAt: &expires,
	})
}

// RotateCredentials handles POST /nodes/{id}/credentials/rotate. Agents may
// rotate their own credentials; administrators may rotate any node's.
// Rotation issues a fresh agent credential and revokes all previous agent
// credentials for the node.
func (h *NodeHandler) RotateCredentials(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}
	if _, err := h.nodes.Get(r.Context(), id); err != nil {
		h.writeServiceError(w, err, "rotating credentials")
		return
	}

	token, expiresAt, err := h.credentials.Rotate(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "rotating credentials")
		return
	}

	principal := auth.PrincipalFrom(r.Context())
	expires := expiresAt.UTC().Format(time.RFC3339)
	h.auditEvent(r, audit.Event{
		Operation: audit.OpCredentialRotated,
		Actor:     principal.ID(),
		ActorType: principal.ActorType(),
		Resource:  "agent_credential:" + id,
		RequestID: middleware.RequestIDFrom(r.Context()),
		Source:    middleware.SourceAddress(r),
	})
	writeJSON(w, http.StatusOK, registerResponse{
		NodeID:              id,
		Credential:          token,
		CredentialExpiresAt: &expires,
	})
}

// RevokeCredentials handles DELETE /nodes/{id}/credentials: the kill switch
// for a compromised node. Every active bootstrap and agent credential for the
// node becomes invalid immediately.
func (h *NodeHandler) RevokeCredentials(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}
	if _, err := h.nodes.Get(r.Context(), id); err != nil {
		h.writeServiceError(w, err, "revoking credentials")
		return
	}

	revoked, err := h.credentials.RevokeNode(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "revoking credentials")
		return
	}

	principal := auth.PrincipalFrom(r.Context())
	h.auditEvent(r, audit.Event{
		Operation: audit.OpCredentialRevoked,
		Actor:     principal.ID(),
		ActorType: principal.ActorType(),
		Resource:  "all_credentials:" + id,
		RequestID: middleware.RequestIDFrom(r.Context()),
		Source:    middleware.SourceAddress(r),
		Reason:    fmt.Sprintf("%d credential(s) revoked", revoked),
	})
	h.logger.Printf("credentials revoked: id=%s count=%d", id, revoked)
	writeJSON(w, http.StatusOK, map[string]any{"status": "revoked", "node_id": id, "revoked": revoked})
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

// Delete handles DELETE /nodes/{id}. Deleting a node also revokes every
// credential issued for it so a removed node can never re-authenticate.
func (h *NodeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	if h.credentials != nil {
		if _, err := h.credentials.RevokeNode(r.Context(), id); err != nil {
			h.writeServiceError(w, err, "revoking node credentials")
			return
		}
	}

	if err := h.nodes.Delete(r.Context(), id); err != nil {
		h.writeServiceError(w, err, "deleting node")
		return
	}

	principal := auth.PrincipalFrom(r.Context())
	h.auditEvent(r, audit.Event{
		Operation: audit.OpCredentialRevoked,
		Actor:     principal.ID(),
		ActorType: principal.ActorType(),
		Resource:  "all_credentials:" + id,
		RequestID: middleware.RequestIDFrom(r.Context()),
		Source:    middleware.SourceAddress(r),
		Reason:    "node deleted",
	})
	h.auditEvent(r, audit.Event{
		Operation: audit.OpNodeDeleted,
		Actor:     principal.ID(),
		ActorType: principal.ActorType(),
		Resource:  "node:" + id,
		RequestID: middleware.RequestIDFrom(r.Context()),
		Source:    middleware.SourceAddress(r),
	})
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

// SetState handles PUT /nodes/{id}/state. It is the endpoint edge agents use
// to report their observed actual state.
func (h *NodeHandler) SetState(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	var request setStateRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	status := domain.NodeStatus(strings.TrimSpace(strings.ToUpper(request.Status)))
	if !status.Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status %q", request.Status))
		return
	}

	node, err := h.nodes.UpdateStatus(r.Context(), id, status, request.IPAddress, request.Kubernetes)
	if err != nil {
		h.writeServiceError(w, err, "recording node state")
		return
	}

	h.logger.Printf("state reported: id=%s status=%s ip=%s kubernetes=%v", id, node.Status, node.IPAddress, kubernetesSummary(node.Kubernetes))
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
		DesiredState:  node.DesiredState(),
	})
}

// SetDesiredState handles PUT /nodes/{id}/desired-state.
func (h *NodeHandler) SetDesiredState(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	var request setDesiredStateRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	status := domain.NodeStatus(strings.TrimSpace(strings.ToUpper(request.Status)))
	if status != "" && !status.Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status %q", request.Status))
		return
	}
	if request.Kubernetes != nil && request.Kubernetes.MinimumReadyNodes < 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid kubernetes.minimum_ready_nodes %d", request.Kubernetes.MinimumReadyNodes))
		return
	}

	// Merge the declared fields over the current desired state so a partial
	// update never clobbers fields the client did not provide.
	current, err := h.nodes.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "setting desired state")
		return
	}
	desired := current.DesiredState()
	if status != "" {
		desired.Status = status
	}
	if request.Kubernetes != nil {
		desired.Kubernetes = *request.Kubernetes
	}

	node, err := h.nodes.SetDesiredState(r.Context(), id, desired)
	if err != nil {
		h.writeServiceError(w, err, "setting desired state")
		return
	}

	h.logger.Printf("desired state updated: id=%s desired_status=%s kubernetes_enabled=%v", id, node.DesiredStatus, node.KubernetesEnabled)
	writeJSON(w, http.StatusOK, desiredStateResponse{
		NodeID:        node.ID,
		DesiredStatus: string(node.DesiredStatus),
		DesiredState:  node.DesiredState(),
	})
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

// auditEvent records an audit event when auditing is configured. It is a
// no-op in minimal test setups without an auditor.
func (h *NodeHandler) auditEvent(r *http.Request, event audit.Event) {
	if h.auditor != nil {
		h.auditor.Record(r.Context(), event)
	}
}

// validateNodeName enforces a conservative name charset so hostile strings
// cannot reach logs, metrics labels or Terraform/HCL generation.
func validateNodeName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name is required")
	}
	if len(name) > 63 {
		return errors.New("name must be at most 63 characters")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("invalid name %q: only letters, digits, '-', '_' and '.' are allowed", name)
		}
	}
	return nil
}

func nodeID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid node id %q", id))
		return "", false
	}
	return id, true
}

// decodeJSON parses the request body into destination, writing the
// appropriate error response itself when parsing fails. It returns false when
// the handler must stop.
func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	return true
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

// kubernetesSummary is a terse one-line description of a node's observed
// Kubernetes state, used for log lines and never containing credentials.
func kubernetesSummary(state *domain.KubernetesActualState) string {
	if state == nil {
		return "none"
	}
	return fmt.Sprintf("%s available=%v nodes=%d ready=%d", state.Status, state.Available, state.NodeCount, state.ReadyNodes)
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
// Declared fields are merged over the current desired state so partial updates
// are safe.
type setDesiredStateRequest struct {
	Status     string                         `json:"status"`
	Kubernetes *domain.KubernetesDesiredState `json:"kubernetes"`
}

// setStateRequest is the JSON payload for PUT /nodes/{id}/state.
type setStateRequest struct {
	Status     string                        `json:"status"`
	IPAddress  string                        `json:"ip_address"`
	Kubernetes *domain.KubernetesActualState `json:"kubernetes"`
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

	// BootstrapToken is the single-use registration credential. It is set
	// only in the POST /nodes response that issued it and is never persisted
	// in plaintext or returned anywhere else.
	BootstrapToken string `json:"bootstrap_token,omitempty"`
	// BootstrapExpiresAt is when the bootstrap credential expires.
	BootstrapExpiresAt *string `json:"bootstrap_expires_at,omitempty"`
}

// registerResponse is returned by POST /nodes/{id}/register and the
// credential rotation endpoint. Credential is shown exactly once.
type registerResponse struct {
	NodeID              string  `json:"node_id"`
	Status              string  `json:"status,omitempty"`
	Credential          string  `json:"credential"`
	CredentialExpiresAt *string `json:"credential_expires_at,omitempty"`
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
	NodeID        string              `json:"node_id"`
	DesiredStatus string              `json:"desired_status"`
	DesiredState  domain.DesiredState `json:"desired_state"`
}
