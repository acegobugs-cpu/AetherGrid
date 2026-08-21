package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"AetherGrid/controlPlane/internal/bootstrapper"
	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"

	"github.com/google/uuid"
)

// ClusterService owns Kubernetes cluster definitions and their lifecycle
// operations. Like InfrastructureService, cluster operations run asynchronously
// with per-cluster locking so two operations can never run concurrently against
// the same cluster.
type ClusterService struct {
	clusterRepo RepositoryWithCluster
	opRepo      repository.ClusterOperationRepository
	bootstrapper bootstrapper.KubernetesBootstrapper
	nodeRepo   repository.NodeRepository
	logger     *log.Logger

	// lock guards locks and cancels.
	lock sync.Mutex
	// locks is a per-cluster mutex so operations are serialized per-cluster.
	locks map[string]*sync.Mutex
	// cancels holds the cancellation function of each in-flight operation.
	cancels map[string]context.CancelFunc
}

// RepositoryWithCluster extends the cluster repository interface with all
// methods needed by the cluster service.
type RepositoryWithCluster interface {
	repository.ClusterRepository
	Create(ctx context.Context, cluster *domain.Cluster) error
	Update(ctx context.Context, cluster *domain.Cluster) error
}

// NewClusterService constructs a ClusterService.
func NewClusterService(
	clusterRepo repository.ClusterRepository,
	opRepo repository.ClusterOperationRepository,
	nodeRepo repository.NodeRepository,
	bs bootstrapper.KubernetesBootstrapper,
	logger *log.Logger,
) *ClusterService {
	return &ClusterService{
		clusterRepo:  clusterRepo.(RepositoryWithCluster),
		opRepo:       opRepo,
		bootstrapper: bs,
		nodeRepo:     nodeRepo,
		logger:       logger,
		locks:        make(map[string]*sync.Mutex),
		cancels:      make(map[string]context.CancelFunc),
	}
}

// Recover repairs state after a control-plane restart.
func (s *ClusterService) Recover(ctx context.Context) error {
	failed, err := s.opRepo.FailInFlight(ctx, "control plane restarted")
	if err != nil {
		return fmt.Errorf("failing in-flight cluster operations: %w", err)
	}
	if failed > 0 && s.logger != nil {
		s.logger.Printf("marked %d interrupted cluster operations as failed", failed)
	}

	clusters, err := s.clusterRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("listing clusters after restart: %w", err)
	}
	for _, c := range clusters {
		switch c.Status.State {
		case domain.ClusterStateBootstrapping, domain.ClusterStateCPReady,
			domain.ClusterStateJoining, domain.ClusterStateVerifying:
			c.Status.State = domain.ClusterStateFailed
			c.Status.LastError = "operation interrupted by control plane restart"
			c.UpdatedAt = time.Now().UTC()
			if err := s.clusterRepo.Update(ctx, c); err != nil {
				return fmt.Errorf("resetting cluster %q: %w", c.ID, err)
			}
		}
	}
	return nil
}

// Create validates and persists a new cluster definition. It does NOT bootstrap
// the cluster; that is an explicit follow-up operation.
func (s *ClusterService) Create(ctx context.Context, spec domain.ClusterSpec) (*domain.Cluster, error) {
	if err := spec.Validate(); err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}

	if _, err := s.clusterRepo.GetByName(ctx, spec.Name); err == nil {
		return nil, repository.ErrConflict
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	// Validate that all referenced nodes exist
	for _, nodeID := range append([]string{spec.ControlPlaneNode}, spec.WorkerNodes...) {
		node, err := s.nodeRepo.GetByID(ctx, nodeID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, fmt.Errorf("node %q not found: %w", nodeID, err)
			}
			return nil, err
		}
		if node.Status != domain.StatusReady {
			return nil, fmt.Errorf("node %q is not Ready (status: %s)", nodeID, node.Status)
		}
	}

	now := time.Now().UTC()
	cluster := &domain.Cluster{
		ID: uuid.NewString(),
		Spec: domain.ClusterSpec{
			Name:              spec.Name,
			K3sVersion:        spec.K3sVersion,
			ControlPlaneNode:  spec.ControlPlaneNode,
			WorkerNodes:       spec.WorkerNodes,
			WorkerConcurrency: spec.WorkerConcurrency,
		},
		Status: domain.ClusterStatus{
			State:           domain.ClusterStatePending,
			WorkerNodes:     make([]domain.ClusterNode, len(spec.WorkerNodes)),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	for i, nodeID := range spec.WorkerNodes {
		cluster.Status.WorkerNodes[i] = domain.ClusterNode{
			NodeID: nodeID,
			Role:   domain.RoleWorker,
		}
	}

	cluster.Status.ControlPlaneNode = spec.ControlPlaneNode

	if err := s.clusterRepo.Create(ctx, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// Get returns a single cluster by ID.
func (s *ClusterService) Get(ctx context.Context, id string) (*domain.Cluster, error) {
	return s.clusterRepo.GetByID(ctx, id)
}

// List returns every cluster.
func (s *ClusterService) List(ctx context.Context) ([]*domain.Cluster, error) {
	return s.clusterRepo.GetAll(ctx)
}

// Delete removes a cluster. It refuses while an operation is in flight.
func (s *ClusterService) Delete(ctx context.Context, id string) error {
	operations, err := s.opRepo.ListOperationsByCluster(ctx, id)
	if err != nil {
		return err
	}
	for _, op := range operations {
		if !op.Status.Terminal() {
			return ErrOperationInProgress
		}
	}
	return s.clusterRepo.Delete(ctx, id)
}

// StartBootstrap begins an asynchronous cluster bootstrap operation.
func (s *ClusterService) StartBootstrap(ctx context.Context, clusterID string) (*domain.ClusterOperation, error) {
	cluster, err := s.clusterRepo.GetByID(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	if cluster.Status.State.Terminal() && cluster.Status.State != domain.ClusterStateFailed {
		return nil, domain.ErrClusterOperationInProgress
	}

	// Serialize operations per cluster.
	if !s.tryLock(clusterID) {
		return nil, ErrOperationInProgress
	}

	opCtx, cancel := context.WithCancel(context.Background())
	op := &domain.ClusterOperation{
		ID:        uuid.NewString(),
		ClusterID: clusterID,
		Type:      domain.ClusterOpBootstrap,
		Status:    domain.ClusterOpPending,
		CreatedAt: time.Now().UTC(),
	}

	s.lock.Lock()
	s.cancels[op.ID] = cancel
	s.lock.Unlock()

	if err := s.opRepo.CreateOperation(ctx, op); err != nil {
		cancel()
		s.lock.Lock()
		delete(s.cancels, op.ID)
		s.lock.Unlock()
		s.unlock(clusterID)
		return nil, err
	}

	go s.runBootstrap(opCtx, op, cluster)
	return op, nil
}

// GetOperation returns a single cluster operation by ID.
func (s *ClusterService) GetOperation(ctx context.Context, id string) (*domain.ClusterOperation, error) {
	return s.opRepo.GetClusterOperationByID(ctx, id)
}

// ListOperations returns every operation for a cluster.
func (s *ClusterService) ListOperations(ctx context.Context, clusterID string) ([]*domain.ClusterOperation, error) {
	return s.opRepo.ListOperationsByCluster(ctx, clusterID)
}

// CancelOperation cancels an in-flight operation.
func (s *ClusterService) CancelOperation(ctx context.Context, opID string) (*domain.ClusterOperation, error) {
	op, err := s.opRepo.GetClusterOperationByID(ctx, opID)
	if err != nil {
		return nil, err
	}
	if op.Status.Terminal() {
		return nil, &ValidationError{Message: fmt.Sprintf("operation %s is already %s", opID, op.Status)}
	}

	s.lock.Lock()
	cancel := s.cancels[opID]
	s.lock.Unlock()
	if cancel != nil {
		cancel()
	}

	now := time.Now().UTC()
	op.Status = domain.ClusterOpCancelled
	op.CompletedAt = &now
	op.Error = "cancelled by user"
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil {
		return nil, err
	}
	return op, nil
}

// runBootstrap executes the cluster bootstrap operation in the background.
func (s *ClusterService) runBootstrap(ctx context.Context, op *domain.ClusterOperation, cluster *domain.Cluster) {
	now := time.Now().UTC()
	op.Status = domain.ClusterOpRunning
	op.StartedAt = &now
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("updating cluster operation %s to running: %v", op.ID, err)
	}

	var opErr error

	// 1. Validate cluster
	cluster.Status.State = domain.ClusterStateBootstrapping
	cluster.Status.LastOperation = op.ID
	cluster.UpdatedAt = time.Now().UTC()
	if err := s.clusterRepo.Update(ctx, cluster); err != nil && s.logger != nil {
		s.logger.Printf("updating cluster %s state: %v", cluster.ID, err)
	}

	// 2-5: Install and initialize control-plane
	opErr = s.bootstrapControlPlane(ctx, op, cluster)
	if opErr != nil {
		s.finishClusterOperation(ctx, op, cluster, opErr)
		return
	}

	// 6-11: Bootstrap and join workers
	opErr = s.bootstrapWorkers(ctx, op, cluster)
	if opErr != nil {
		s.finishClusterOperation(ctx, op, cluster, opErr)
		return
	}

	// 12-13: Verify and register
	verifyResult, vErr := s.bootstrapper.VerifyCluster(ctx, cluster.ID)
	if vErr != nil {
		opErr = fmt.Errorf("verify cluster: %w", vErr)
		s.finishClusterOperation(ctx, op, cluster, opErr)
		return
	}
	if !verifyResult.Succeeded {
		opErr = errors.New(verifyResult.Error)
		s.finishClusterOperation(ctx, op, cluster, opErr)
		return
	}

	// 14: Mark cluster Ready
	cluster.Status.State = domain.ClusterStateReady
	cluster.Status.LastError = ""
	status, _ := s.bootstrapper.GetClusterStatus(ctx, cluster.ID)
	if status != nil {
		cluster.Status.KubernetesVersion = status.ClusterVersion
		cluster.Status.APIEndpoint = cluster.Spec.ControlPlaneNode
		cluster.Status.WorkerNodes = s.updateWorkerNodes(cluster, status)
		cluster.Status.ReadyWorkerCount = status.ReadyWorkerCount
	}
	cluster.UpdatedAt = time.Now().UTC()
	if err := s.clusterRepo.Update(ctx, cluster); err != nil && s.logger != nil {
		s.logger.Printf("updating cluster %s to ready: %v", cluster.ID, err)
	}
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("finalizing cluster operation %s: %v", op.ID, err)
	}

	now = time.Now().UTC()
	op.Status = domain.ClusterOpSucceeded
	op.CompletedAt = &now
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("updating cluster operation %s to succeeded: %v", op.ID, err)
	}

	s.lock.Lock()
	delete(s.cancels, op.ID)
	s.lock.Unlock()
	s.unlock(cluster.ID)
}

// bootstrapControlPlane installs and initializes the k3s server.
func (s *ClusterService) bootstrapControlPlane(ctx context.Context, op *domain.ClusterOperation, cluster *domain.Cluster) error {
	cpNodeID := cluster.Spec.ControlPlaneNode

	op.CurrentStep = string(domain.K8sBootstrapStepValidateCluster)
	op.SucceededSteps = nil
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("updating operation step: %v", err)
	}

	// Validate control-plane node
	installResult, err := s.bootstrapper.InstallServer(ctx, cluster.ID, cpNodeID)
	if err != nil {
		return fmt.Errorf("install server: %w", err)
	}
	if !installResult.Succeeded {
		return errors.New(installResult.Error)
	}
	op.SucceededSteps = append(op.SucceededSteps, string(domain.K8sBootstrapStepInstallServer))
	op.CurrentStep = string(domain.K8sBootstrapStepInitializeServer)
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("updating operation step: %v", err)
	}

	// Initialize server and get join info
	joinInfo, err := s.bootstrapper.InitializeServer(ctx, cluster.ID, cpNodeID)
	if err != nil {
		return fmt.Errorf("initialize server: %w", err)
	}
	if joinInfo == nil {
		return domain.ErrInvalidJoinInfo
	}
	// Verify token hash
	if joinInfo.TokenHash == "" {
		return domain.ErrInvalidJoinInfo
	}

	op.SucceededSteps = append(op.SucceededSteps, string(domain.K8sBootstrapStepInitializeServer))
	op.CurrentStep = string(domain.K8sBootstrapStepVerifyServerReady)
	cluster.Status.State = domain.ClusterStateCPReady
	cluster.UpdatedAt = time.Now().UTC()
	if err := s.clusterRepo.Update(ctx, cluster); err != nil && s.logger != nil {
		s.logger.Printf("updating cluster state: %v", err)
	}

	// Store the join info in the operation for worker bootstrap
	op.CurrentStep = string(domain.K8sBootstrapStepRetrieveJoinInfo)
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("updating operation step: %v", err)
	}

	// Store join info securely in the operation Error field (not ideal but
	// avoids adding a secrets field; in production this would use a proper
	// secrets store). Actually, we should store this in the bootstrapper.
	// The k3s bootstrapper already stores it internally.
	_ = joinInfo

	return nil
}

// bootstrapWorkers installs k3s agents and joins workers to the cluster.
func (s *ClusterService) bootstrapWorkers(ctx context.Context, op *domain.ClusterOperation, cluster *domain.Cluster) error {
	cluster.Status.State = domain.ClusterStateJoining
	cluster.UpdatedAt = time.Now().UTC()
	if err := s.clusterRepo.Update(ctx, cluster); err != nil && s.logger != nil {
		s.logger.Printf("updating cluster state to joining: %v", err)
	}

	// Get the join info from the bootstrapper by re-initializing (idempotent)
	cpNodeID := cluster.Spec.ControlPlaneNode
	joinInfo, err := s.bootstrapper.InitializeServer(ctx, cluster.ID, cpNodeID)
	if err != nil {
		return fmt.Errorf("retrieving join info for workers: %w", err)
	}
	if joinInfo == nil {
		return domain.ErrInvalidJoinInfo
	}

	concurrency := cluster.Spec.WorkerConcurrency
	if concurrency < 1 {
		concurrency = 3
	}

	op.CurrentStep = string(domain.K8sBootstrapStepBootstrapWorkers)
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("updating operation step: %v", err)
	}

	// Bootstrap workers in parallel with controlled concurrency
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var workerErrors []error

	for _, workerNodeID := range cluster.Spec.WorkerNodes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Install agent
			installResult, err := s.bootstrapper.InstallAgent(ctx, cluster.ID, nodeID)
			if err != nil || !installResult.Succeeded {
				mu.Lock()
				if err != nil {
					workerErrors = append(workerErrors, fmt.Errorf("install agent for %s: %w", nodeID, err))
				} else {
					workerErrors = append(workerErrors, fmt.Errorf("install agent for %s: %s", nodeID, installResult.Error))
				}
				mu.Unlock()
				return
			}

			// Join worker
			joinResult, err := s.bootstrapper.JoinWorker(ctx, cluster.ID, nodeID, joinInfo)
			if err != nil || !joinResult.Succeeded {
				mu.Lock()
				if err != nil {
					workerErrors = append(workerErrors, fmt.Errorf("join worker %s: %w", nodeID, err))
				} else {
					workerErrors = append(workerErrors, fmt.Errorf("join worker %s: %s", nodeID, joinResult.Error))
				}
				mu.Unlock()
				return
			}
		}(workerNodeID)

		// Wait for the previous batch to have a slot available
		select {
		case sem <- struct{}{}:
			<-sem
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	// Wait for all workers to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}

	if len(workerErrors) > 0 {
		cluster.Status.State = domain.ClusterStateDegraded
		cluster.UpdatedAt = time.Now().UTC()
		// Collect unique error messages
		errMsg := fmt.Sprintf("%d workers failed to bootstrap", len(workerErrors))
		cluster.Status.LastError = errMsg
		if err := s.clusterRepo.Update(ctx, cluster); err != nil && s.logger != nil {
			s.logger.Printf("updating cluster state to degraded: %v", err)
		}
		return fmt.Errorf("%s: %v", errMsg, workerErrors[0])
	}

	op.SucceededSteps = append(op.SucceededSteps, string(domain.K8sBootstrapStepJoinWorkers))

	// Update worker status
	status, err := s.bootstrapper.GetClusterStatus(ctx, cluster.ID)
	if err == nil && status != nil {
		cluster.Status.WorkerNodes = s.updateWorkerNodes(cluster, status)
		cluster.Status.ReadyWorkerCount = status.ReadyWorkerCount
	}
	cluster.UpdatedAt = time.Now().UTC()
	if err := s.clusterRepo.Update(ctx, cluster); err != nil && s.logger != nil {
		s.logger.Printf("updating worker nodes: %v", err)
	}

	return nil
}

// finishClusterOperation marks an operation as failed and updates cluster state.
func (s *ClusterService) finishClusterOperation(ctx context.Context, op *domain.ClusterOperation, cluster *domain.Cluster, opErr error) {
	now := time.Now().UTC()
	op.Status = domain.ClusterOpFailed
	op.CompletedAt = &now
	op.Error = opErr.Error()
	cluster.Status.State = domain.ClusterStateFailed
	cluster.Status.LastError = opErr.Error()
	cluster.UpdatedAt = time.Now().UTC()

	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("finalizing cluster operation %s: %v", op.ID, err)
	}
	if err := s.clusterRepo.Update(ctx, cluster); err != nil && s.logger != nil {
		s.logger.Printf("finalizing cluster %s: %v", cluster.ID, err)
	}

	s.lock.Lock()
	delete(s.cancels, op.ID)
	s.lock.Unlock()
	s.unlock(cluster.ID)
}

// updateWorkerNodes syncs the cluster's worker node list with Kubernetes state.
func (s *ClusterService) updateWorkerNodes(cluster *domain.Cluster, status *bootstrapper.ClusterStatusInfo) []domain.ClusterNode {
	workers := make([]domain.ClusterNode, 0, len(cluster.Spec.WorkerNodes))
	for i, nodeID := range cluster.Spec.WorkerNodes {
		var role domain.ClusterRole = domain.RoleWorker
		if i < len(cluster.Status.WorkerNodes) {
			role = cluster.Status.WorkerNodes[i].Role
		}
		workers = append(workers, domain.ClusterNode{
			NodeID:    nodeID,
			Role:      role,
			Ready:     i < status.ReadyWorkerCount,
		})
	}
	return workers
}

// tryLock acquires the per-cluster lock, returning false when a concurrent
// operation already holds it.
func (s *ClusterService) tryLock(clusterID string) bool {
	return s.lockFor(clusterID).TryLock()
}

func (s *ClusterService) unlock(clusterID string) {
	s.lockFor(clusterID).Unlock()
}

func (s *ClusterService) lockFor(clusterID string) *sync.Mutex {
	s.lock.Lock()
	defer s.lock.Unlock()
	mu, ok := s.locks[clusterID]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[clusterID] = mu
	}
	return mu
}