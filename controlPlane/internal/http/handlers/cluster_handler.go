package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"
	"AetherGrid/controlPlane/internal/service"
)

// ClusterHandler handles cluster management operations.
type ClusterHandler struct {
	clusterService *service.ClusterService
	logger         *log.Logger
}

// NewClusterHandler constructs a ClusterService handler.
func NewClusterHandler(clusterService *service.ClusterService, logger *log.Logger) *ClusterHandler {
	return &ClusterHandler{
		clusterService: clusterService,
		logger:         logger,
	}
}

// Create handles POST /clusters. It validates and persists a new cluster
// definition. The cluster does NOT bootstrap immediately; the caller must
// explicitly trigger bootstrap.
func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	var spec domain.ClusterSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cluster, err := h.clusterService.Create(r.Context(), spec)
	if err != nil {
		var validation *service.ValidationError
		if errors.As(err, &validation) {
			h.writeError(w, http.StatusBadRequest, validation.Message)
		} else {
			h.writeServiceError(w, err, "creating cluster")
		}
		return
	}

	h.writeJSON(w, http.StatusCreated, clusterCreateResponse{
		ID:        cluster.ID,
		Name:      cluster.Spec.Name,
		Status:    clusterStatusToResponse(cluster.Status),
		CreatedAt: cluster.CreatedAt,
	})
}

// List handles GET /clusters. It returns all cluster definitions.
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	clusters, err := h.clusterService.List(r.Context())
	if err != nil {
		h.writeServiceError(w, err, "listing clusters")
		return
	}

	result := make([]clusterListResponse, 0, len(clusters))
	for _, c := range clusters {
		result = append(result, clusterListResponse{
			ID:        c.ID,
			Name:      c.Spec.Name,
			Status:    clusterStatusToResponse(c.Status),
			CreatedAt: c.CreatedAt,
		})
	}
	h.writeJSON(w, http.StatusOK, result)
}

// Get handles GET /clusters/:id. It returns the cluster definition and status.
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.URL.Path, "/")
	id = strings.TrimPrefix(id, "/clusters/")
	cluster, err := h.clusterService.Get(r.Context(), id)
	if err != nil {
		var notFound *service.ValidationError
		if errors.As(err, &notFound) && notFound.Message == "cluster not found" {
			h.writeError(w, http.StatusNotFound, "cluster not found")
		} else {
			h.writeServiceError(w, err, "getting cluster")
		}
		return
	}

	h.writeJSON(w, http.StatusOK, clusterGetResponse{
		ID:        cluster.ID,
		Name:      cluster.Spec.Name,
		Status:    clusterStatusToResponse(cluster.Status),
		Spec:      clusterSpecToResponse(cluster.Spec),
		CreatedAt: cluster.CreatedAt,
		UpdatedAt: cluster.UpdatedAt,
	})
}

// Bootstrap handles POST /clusters/:id/bootstrap. It starts an asynchronous
// cluster bootstrap operation. The operation runs in the background; the caller
// can poll GET /operations/:id to track progress.
func (h *ClusterHandler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.URL.Path, "/")
	id = strings.TrimPrefix(id, "/clusters/")

	op, err := h.clusterService.StartBootstrap(r.Context(), id)
	if err != nil {
		var notFound *service.ValidationError
		if errors.As(err, &notFound) && notFound.Message == "cluster not found" {
			h.writeError(w, http.StatusNotFound, "cluster not found")
		} else {
			h.writeServiceError(w, err, "starting cluster bootstrap")
		}
		return
	}

	h.writeJSON(w, http.StatusAccepted, clusterBootstrapResponse{
		ID:        op.ID,
		Type:      string(op.Type),
		Status:    string(op.Status),
		CreatedAt: op.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// Status handles GET /clusters/:id/status. It returns the cluster's current
// status: API reachability, server readiness, worker counts, etc.
func (h *ClusterHandler) Status(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.URL.Path, "/")
	id = strings.TrimPrefix(id, "/clusters/")

	cluster, err := h.clusterService.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "cluster not found")
		} else {
			h.writeServiceError(w, err, "getting cluster status")
		}
		return
	}

	h.writeJSON(w, http.StatusOK, clusterGetResponse{
		ID:                cluster.ID,
		Name:              cluster.Spec.Name,
		Status:            clusterStatusToResponse(cluster.Status),
		KubernetesVersion: cluster.Status.KubernetesVersion,
		ControlPlaneNode:  cluster.Status.ControlPlaneNode,
		WorkerCount:       cluster.Status.ReadyWorkerCount,
		ReadyWorkerCount:  cluster.Status.ReadyWorkerCount,
		APIEndpoint:       cluster.Status.APIEndpoint,
		LastOperation:     cluster.Status.LastOperation,
		LastError:         cluster.Status.LastError,
		LastVerifiedAt:    cluster.Status.LastVerifiedAt,
	})
}

type clusterCreateResponse struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Status    clusterStatusResponse `json:"status"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type clusterGetResponse struct {
	ID                string                `json:"id"`
	Name              string                `json:"name"`
	Status            clusterStatusResponse `json:"status"`
	Spec              clusterSpecResponse   `json:"spec,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	KubernetesVersion string                `json:"kubernetes_version,omitempty"`
	ControlPlaneNode  string                `json:"control_plane_node,omitempty"`
	WorkerCount       int                   `json:"worker_count,omitempty"`
	ReadyWorkerCount  int                   `json:"ready_worker_count,omitempty"`
	APIEndpoint       string                `json:"api_endpoint,omitempty"`
	LastOperation     string                `json:"last_operation,omitempty"`
	LastError         string                `json:"last_error,omitempty"`
	LastVerifiedAt    *time.Time            `json:"last_verified_at,omitempty"`
}

type clusterListResponse struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Status    clusterStatusResponse `json:"status"`
	CreatedAt time.Time             `json:"created_at"`
}

// clusterStatusResponse represents the JSON cluster status response.
type clusterStatusResponse struct {
	State               domain.ClusterLifecycleState `json:"state"`
	KubernetesVersion   string                       `json:"kubernetes_version,omitempty"`
	ControlPlaneNode    string                       `json:"control_plane_node,omitempty"`
	WorkerCount         int                          `json:"worker_count,omitempty"`
	ReadyWorkerCount    int                          `json:"ready_worker_count,omitempty"`
	NotReadyWorkerCount int                          `json:"not_ready_worker_count,omitempty"`
	APIEndpoint         string                       `json:"api_endpoint,omitempty"`
	LastOperation       string                       `json:"last_operation,omitempty"`
	LastError           string                       `json:"last_error,omitempty"`
	LastVerifiedAt      *time.Time                   `json:"last_verified_at,omitempty"`
}

type clusterSpecResponse struct {
	K3sVersion string `json:"k3s_version"`
}

// clusterSpecToResponse converts a domain ClusterSpec to the JSON response.
func clusterSpecToResponse(spec domain.ClusterSpec) clusterSpecResponse {
	return clusterSpecResponse{
		K3sVersion: spec.K3sVersion,
	}
}

type clusterBootstrapResponse struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func clusterStatusToResponse(status domain.ClusterStatus) clusterStatusResponse {
	return clusterStatusResponse{
		State:               status.State,
		KubernetesVersion:   status.KubernetesVersion,
		ControlPlaneNode:    status.ControlPlaneNode,
		WorkerCount:         len(status.WorkerNodes),
		ReadyWorkerCount:    status.ReadyWorkerCount,
		NotReadyWorkerCount: status.ReadyWorkerCount,
		APIEndpoint:         status.APIEndpoint,
		LastOperation:       status.LastOperation,
		LastError:           status.LastError,
		LastVerifiedAt:      status.LastVerifiedAt,
	}
}

// writeServiceError maps service/repository errors to consistent HTTP responses.
func (h *ClusterHandler) writeServiceError(w http.ResponseWriter, err error, action string) {
	var validation *service.ValidationError
	switch {
	case errors.Is(err, repository.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "cluster not found")
	case errors.As(err, &validation):
		h.writeError(w, http.StatusBadRequest, validation.Message)
	default:
		h.logger.Printf("%s: %v", action, err)
		h.writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// writeJSON writes a JSON response.
func (h *ClusterHandler) writeJSON(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

// writeError writes a simple JSON error response.
func (h *ClusterHandler) writeError(w http.ResponseWriter, code int, message string) {
	h.writeJSON(w, code, map[string]string{"error": message})
}
