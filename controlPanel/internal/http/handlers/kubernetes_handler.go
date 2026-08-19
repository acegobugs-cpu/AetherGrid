package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/service"
)

// KubernetesHandler exposes a node's observed Kubernetes state and dispatches
// Kubernetes queries to the node's agent. The control plane never talks to a
// Kubernetes cluster directly; it delegates through the agent.
type KubernetesHandler struct {
	nodes    *service.NodeService
	commands *service.CommandService
	logger   *log.Logger
}

// NewKubernetesHandler constructs a KubernetesHandler with the given services.
func NewKubernetesHandler(
	nodes *service.NodeService,
	commands *service.CommandService,
	logger *log.Logger,
) *KubernetesHandler {
	return &KubernetesHandler{
		nodes:    nodes,
		commands: commands,
		logger:   logger,
	}
}

// State handles GET /nodes/{id}/kubernetes. It returns the node's declared
// Kubernetes desired state and the most recent Kubernetes state reported by
// the agent. Because the agent reports asynchronously, the observed state may
// be stale or absent; clients that need fresh data use the nodes/pods
// endpoints, which dispatch a command to the agent.
func (h *KubernetesHandler) State(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	node, err := h.nodes.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err, "getting kubernetes state")
		return
	}

	var observed *kubernetesStateSummary
	if node.Kubernetes != nil {
		observed = newKubernetesStateSummary(node.Kubernetes)
	}

	writeJSON(w, http.StatusOK, kubernetesStateResponse{
		NodeID:  node.ID,
		Desired: node.DesiredState().Kubernetes,
		State:   observed,
	})
}

// ListNodes handles GET /nodes/{id}/kubernetes/nodes. It dispatches a
// LIST_KUBERNETES_NODES command to the agent; the caller polls the command for
// the result.
func (h *KubernetesHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	h.dispatch(w, r, id, domain.CommandListKubernetesNodes, nil)
}

// ListPods handles GET /nodes/{id}/kubernetes/pods. It dispatches a
// LIST_KUBERNETES_PODS command to the agent, optionally filtered to a
// namespace via the namespace query parameter.
func (h *KubernetesHandler) ListPods(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	var parameters json.RawMessage
	if namespace := strings.TrimSpace(r.URL.Query().Get("namespace")); namespace != "" {
		encoded, _ := json.Marshal(map[string]string{"namespace": namespace})
		parameters = encoded
	}
	h.dispatch(w, r, id, domain.CommandListKubernetesPods, parameters)
}

// dispatch enqueues a Kubernetes query command and answers 202 Accepted so the
// caller can poll /nodes/{id}/commands/{command_id} for the result.
func (h *KubernetesHandler) dispatch(w http.ResponseWriter, r *http.Request, id, commandType string, parameters json.RawMessage) {
	command, err := h.commands.Create(r.Context(), service.CreateCommandInput{
		NodeID:     id,
		Type:       commandType,
		Parameters: parameters,
	})
	if err != nil {
		h.writeServiceError(w, err, "dispatching kubernetes command")
		return
	}

	h.logger.Printf("kubernetes command dispatched: node_id=%s command_id=%s type=%s", command.NodeID, command.ID, command.Type)
	writeJSON(w, http.StatusAccepted, newCommandResponse(command))
}

// writeServiceError maps service/repository errors to consistent HTTP
// responses. Internal errors are logged and never exposed to clients.
func (h *KubernetesHandler) writeServiceError(w http.ResponseWriter, err error, action string) {
	var validation *service.ValidationError
	switch {
	case service.IsNotFound(err):
		writeError(w, http.StatusNotFound, "node not found")
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, validation.Message)
	default:
		h.logger.Printf("%s: %v", action, err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// kubernetesStateSummary is the observed Kubernetes state without the
// reported-at timestamp so the payload stays focused on cluster health.
type kubernetesStateSummary struct {
	Available     bool                    `json:"available"`
	Status        domain.KubernetesStatus `json:"status"`
	Version       string                  `json:"version"`
	NodeCount     int                     `json:"node_count"`
	ReadyNodes    int                     `json:"ready_nodes"`
	NotReadyNodes int                     `json:"not_ready_nodes"`
	Workload      domain.WorkloadSummary  `json:"workload"`
	ReportedAt    string                  `json:"reported_at"`
}

func newKubernetesStateSummary(state *domain.KubernetesActualState) *kubernetesStateSummary {
	if state == nil {
		return nil
	}
	return &kubernetesStateSummary{
		Available:     state.Available,
		Status:        state.Status,
		Version:       state.Version,
		NodeCount:     state.NodeCount,
		ReadyNodes:    state.ReadyNodes,
		NotReadyNodes: state.NotReadyNodes,
		Workload:      state.Workload,
		ReportedAt:    state.ReportedAt.UTC().Format(time.RFC3339Nano),
	}
}

// kubernetesStateResponse is the JSON representation of GET /nodes/{id}/kubernetes.
type kubernetesStateResponse struct {
	NodeID  string                        `json:"node_id"`
	Desired domain.KubernetesDesiredState `json:"desired"`
	State   *kubernetesStateSummary       `json:"state"`
}
