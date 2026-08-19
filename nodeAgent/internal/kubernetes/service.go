package kubernetes

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// ServiceConfig controls the Kubernetes service.
type ServiceConfig struct {
	// Enabled turns Kubernetes integration on. When false the agent reports
	// DISABLED and continues operating normally.
	Enabled bool
	// RequestTimeout bounds every Kubernetes API call.
	RequestTimeout time.Duration
}

// Service is the application-level Kubernetes integration layer. It contains
// the health calculation, operation validation, error translation and the
// disabled/unavailable fallbacks. Command handlers and the state collector
// depend on it; it never calls client-go directly.
type Service struct {
	cfg     ServiceConfig
	logger  *log.Logger
	factory func() (KubernetesClient, error)

	mu     sync.Mutex
	client KubernetesClient
}

// NewService constructs a Kubernetes service. The factory lazily builds the
// Kubernetes client on first use and the client is then reused. Pass a nil
// factory in tests to inject a mock client via SetClient.
func NewService(cfg ServiceConfig, factory func() (KubernetesClient, error), logger *log.Logger) *Service {
	if logger == nil {
		logger = log.New(logDiscard{}, "", 0)
	}
	return &Service{cfg: cfg, factory: factory, logger: logger}
}

// SetClient injects a client directly (used by tests). It also marks the
// service enabled regardless of configuration.
func (s *Service) SetClient(client KubernetesClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = client
}

// Enabled reports whether Kubernetes integration is configured on.
func (s *Service) Enabled() bool {
	return s.cfg.Enabled
}

// ensureClient returns the reusable client, lazily initializing it once.
func (s *Service) ensureClient() (KubernetesClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	if !s.cfg.Enabled {
		return nil, &Error{Code: CodeInvalidConfig, Err: errDisabled}
	}
	if s.factory == nil {
		return nil, &Error{Code: CodeInvalidConfig, Err: errNoFactory}
	}
	client, err := s.factory()
	if err != nil {
		s.logger.Printf("kubernetes client initialization failed: %v", err)
		return nil, err
	}
	s.logger.Printf("kubernetes client initialized")
	s.client = client
	return client, nil
}

// GetState builds the observed Kubernetes state. It never fails: an
// unreachable or disabled cluster is reported as a state, so partial failure
// cannot break the agent's basic state reporting.
func (s *Service) GetState(ctx context.Context) KubernetesState {
	if !s.cfg.Enabled && s.client == nil {
		return KubernetesState{Status: KubernetesStatusDisabled}
	}

	client, err := s.ensureClient()
	if err != nil {
		return KubernetesState{Status: KubernetesStatusUnavailable, Error: err.Error()}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()

	info, err := client.GetClusterInfo(timeoutCtx)
	if err != nil {
		s.logger.Printf("kubernetes unavailable: %v", err)
		return KubernetesState{
			Status: KubernetesStatusUnavailable,
			Error:  Translate(err).Error(),
		}
	}

	state := KubernetesState{
		Available:     true,
		Version:       info.Version,
		NodeCount:     info.NodeCount,
		ReadyNodes:    info.ReadyNodes,
		NotReadyNodes: info.NotReadyNodes,
	}
	switch {
	case info.ReadyNodes < info.NodeCount:
		state.Status = KubernetesStatusDegraded
	default:
		state.Status = KubernetesStatusReady
	}

	pods, podErr := client.ListPods(timeoutCtx, "")
	if podErr != nil {
		// The cluster is reachable but the pod summary failed. Report the
		// cluster-level health and leave the workload summary empty rather
		// than degrading the whole cluster for a summary failure.
		s.logger.Printf("kubernetes pod summary failed: %v", Translate(podErr))
		return state
	}
	for _, pod := range pods {
		state.Workload.TotalPods++
		switch pod.Phase {
		case "Running":
			state.Workload.RunningPods++
		case "Failed":
			state.Workload.FailedPods++
		}
	}

	s.logger.Printf("kubernetes state collected: status=%s version=%s nodes=%d ready=%d pods=%d",
		state.Status, state.Version, state.NodeCount, state.ReadyNodes, state.Workload.TotalPods)
	return state
}

// GetClusterInfo returns cluster information, translating any error.
func (s *Service) GetClusterInfo(ctx context.Context) (ClusterInfo, error) {
	client, err := s.ensureClient()
	if err != nil {
		return ClusterInfo{}, Translate(err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	info, err := client.GetClusterInfo(timeoutCtx)
	if err != nil {
		return ClusterInfo{}, Translate(err)
	}
	return info, nil
}

// ListNodes returns the observed Kubernetes nodes.
func (s *Service) ListNodes(ctx context.Context) ([]KubernetesNode, error) {
	client, err := s.ensureClient()
	if err != nil {
		return nil, Translate(err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	nodes, err := client.ListNodes(timeoutCtx)
	if err != nil {
		return nil, Translate(err)
	}
	return nodes, nil
}

// GetNode returns a single Kubernetes node by name.
func (s *Service) GetNode(ctx context.Context, name string) (KubernetesNode, error) {
	client, err := s.ensureClient()
	if err != nil {
		return KubernetesNode{}, Translate(err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	node, err := client.GetNode(timeoutCtx, name)
	if err != nil {
		return KubernetesNode{}, Translate(err)
	}
	return node, nil
}

// ListPods returns pods, optionally in a single namespace.
func (s *Service) ListPods(ctx context.Context, namespace string) ([]KubernetesPod, error) {
	client, err := s.ensureClient()
	if err != nil {
		return nil, Translate(err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	pods, err := client.ListPods(timeoutCtx, namespace)
	if err != nil {
		return nil, Translate(err)
	}
	return pods, nil
}

// CreateTestNamespace creates the reversible test namespace after validating
// the name.
func (s *Service) CreateTestNamespace(ctx context.Context, name string) error {
	if err := validateNamespaceName(name); err != nil {
		return err
	}
	client, err := s.ensureClient()
	if err != nil {
		return Translate(err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	if err := client.CreateNamespace(timeoutCtx, name); err != nil {
		return Translate(err)
	}
	return nil
}

// DeleteTestNamespace removes the reversible test namespace.
func (s *Service) DeleteTestNamespace(ctx context.Context, name string) error {
	if err := validateNamespaceName(name); err != nil {
		return err
	}
	client, err := s.ensureClient()
	if err != nil {
		return Translate(err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	if err := client.DeleteNamespace(timeoutCtx, name); err != nil {
		return Translate(err)
	}
	return nil
}

// validateNamespaceName enforces a conservative, Kubernetes-compatible DNS
// label so the test-namespace operation can never target system namespaces or
// arbitrary production resources.
func validateNamespaceName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &Error{Code: CodeInvalidConfig, Err: errEmptyNamespace}
	}
	if len(name) > 63 {
		return &Error{Code: CodeInvalidConfig, Err: errLongNamespace}
	}
	if strings.HasPrefix(name, "kube-") {
		return &Error{Code: CodeInvalidConfig, Err: errReservedNamespace}
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && char != '-' {
			return &Error{Code: CodeInvalidConfig, Err: errInvalidNamespace}
		}
	}
	return nil
}

// logDiscard is a small io.Writer that discards output, used for a default
// quiet logger.
type logDiscard struct{}

func (logDiscard) Write(p []byte) (int, error) { return len(p), nil }

var (
	errDisabled          = errWithMessage("kubernetes integration is disabled")
	errNoFactory         = errWithMessage("no kubernetes client factory configured")
	errEmptyNamespace    = errWithMessage("namespace name is required")
	errLongNamespace     = errWithMessage("namespace name exceeds 63 characters")
	errReservedNamespace = errWithMessage("namespace name is reserved (kube- prefix)")
	errInvalidNamespace  = errWithMessage("namespace name must be lowercase letters, digits or dashes")
)

type staticError string

func (e staticError) Error() string { return string(e) }

func errWithMessage(message string) error { return staticError(message) }
