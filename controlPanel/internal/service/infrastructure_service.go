package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/provisioning"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"

	"github.com/google/uuid"
)

// ErrOperationInProgress is returned when a second operation is attempted
// against an infrastructure deployment that already has an operation running.
var ErrOperationInProgress = errors.New("another operation is already in progress for this infrastructure")

// InfrastructureService owns infrastructure definitions and their lifecycle
// operations. Operations run asynchronously: each one is executed in its own
// goroutine against a per-infrastructure lock, so two operations can never run
// concurrently against the same deployment while different deployments proceed
// in parallel.
type InfrastructureService struct {
	infraRepo   repository.InfrastructureRepository
	opRepo      repository.InfrastructureOperationRepository
	provisioner provisioning.Provisioner
	metrics     *provisioning.Metrics
	logger      *log.Logger

	// lock guards locks and cancels.
	lock sync.Mutex
	// locks is a per-infrastructure mutex so operations against one deployment
	// are serialized without globally blocking unrelated deployments.
	locks map[string]*sync.Mutex
	// cancels holds the cancellation function of each in-flight operation.
	cancels map[string]context.CancelFunc
}

// NewInfrastructureService constructs an infrastructure service. The
// provisioner is injected so tests can substitute a stub and the service
// remains independent of Terraform.
func NewInfrastructureService(
	infraRepo repository.InfrastructureRepository,
	opRepo repository.InfrastructureOperationRepository,
	provisioner provisioning.Provisioner,
	metrics *provisioning.Metrics,
	logger *log.Logger,
) *InfrastructureService {
	return &InfrastructureService{
		infraRepo:   infraRepo,
		opRepo:      opRepo,
		provisioner: provisioner,
		metrics:     metrics,
		logger:      logger,
		locks:       make(map[string]*sync.Mutex),
		cancels:     make(map[string]context.CancelFunc),
	}
}

// Recover repairs state after a control-plane restart: operations interrupted
// mid-flight are marked failed and deployments that were mid-operation return
// to the pending phase. It must be called once at startup.
func (s *InfrastructureService) Recover(ctx context.Context) error {
	failed, err := s.opRepo.FailInFlight(ctx, "control plane restarted")
	if err != nil {
		return fmt.Errorf("failing in-flight operations: %w", err)
	}
	if failed > 0 && s.logger != nil {
		s.logger.Printf("marked %d interrupted operations as failed", failed)
	}

	infrastructures, err := s.infraRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("listing infrastructures after restart: %w", err)
	}
	for _, infra := range infrastructures {
		switch infra.Status.Phase {
		case domain.InfraPhasePlanning, domain.InfraPhaseApplying, domain.InfraPhaseDestroying:
			infra.Status.Phase = domain.InfraPhasePending
			infra.Status.Error = "operation interrupted by control plane restart"
			infra.UpdatedAt = time.Now().UTC()
			if err := s.infraRepo.Update(ctx, infra); err != nil {
				return fmt.Errorf("resetting infrastructure %q: %w", infra.ID, err)
			}
		}
	}
	return nil
}

// Create validates and persists a new infrastructure deployment.
func (s *InfrastructureService) Create(ctx context.Context, spec domain.InfrastructureSpec) (*domain.Infrastructure, error) {
	if err := spec.Validate(); err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	spec.Name = strings.TrimSpace(spec.Name)

	if _, err := s.infraRepo.GetByName(ctx, spec.Name); err == nil {
		return nil, repository.ErrConflict
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	infra := &domain.Infrastructure{
		ID:   uuid.NewString(),
		Spec: spec,
		Status: domain.InfrastructureStatus{
			Phase: domain.InfraPhasePending,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.infraRepo.Create(ctx, infra); err != nil {
		return nil, err
	}
	return infra, nil
}

// Get returns a single infrastructure deployment by ID.
func (s *InfrastructureService) Get(ctx context.Context, id string) (*domain.Infrastructure, error) {
	return s.infraRepo.GetByID(ctx, id)
}

// List returns every infrastructure deployment.
func (s *InfrastructureService) List(ctx context.Context) ([]*domain.Infrastructure, error) {
	return s.infraRepo.GetAll(ctx)
}

// Delete removes an infrastructure deployment. It refuses while an operation
// is still in flight.
func (s *InfrastructureService) Delete(ctx context.Context, id string) error {
	operations, err := s.opRepo.ListOperationsByInfrastructure(ctx, id)
	if err != nil {
		return err
	}
	for _, op := range operations {
		if !op.Status.Terminal() {
			return ErrOperationInProgress
		}
	}
	return s.infraRepo.Delete(ctx, id)
}

// StartOperation begins an asynchronous plan, apply or destroy operation
// against the given deployment. The operation is persisted as pending and
// executed in the background; the caller receives the operation record
// immediately.
func (s *InfrastructureService) StartOperation(ctx context.Context, infraID string, opType domain.OperationType) (*domain.InfrastructureOperation, error) {
	if !opType.Valid() {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid operation type %q", opType)}
	}

	infra, err := s.infraRepo.GetByID(ctx, infraID)
	if err != nil {
		return nil, err
	}

	// Serialize operations per infrastructure: a second concurrent operation
	// against the same deployment is rejected, not queued.
	if !s.tryLock(infraID) {
		return nil, ErrOperationInProgress
	}

	opCtx, cancel := context.WithCancel(context.Background())
	op := &domain.InfrastructureOperation{
		ID:               uuid.NewString(),
		InfrastructureID: infraID,
		Type:             opType,
		Status:           domain.OpPending,
		CreatedAt:        time.Now().UTC(),
	}

	s.lock.Lock()
	s.cancels[op.ID] = cancel
	s.lock.Unlock()

	if err := s.opRepo.CreateOperation(ctx, op); err != nil {
		cancel()
		s.lock.Lock()
		delete(s.cancels, op.ID)
		s.lock.Unlock()
		s.unlock(infraID)
		return nil, err
	}

	go s.runOperation(opCtx, op, infra)
	return op, nil
}

// GetOperation returns a single operation by ID.
func (s *InfrastructureService) GetOperation(ctx context.Context, id string) (*domain.InfrastructureOperation, error) {
	return s.opRepo.GetOperationByID(ctx, id)
}

// ListOperations returns every operation recorded against a deployment,
// newest first.
func (s *InfrastructureService) ListOperations(ctx context.Context, infraID string) ([]*domain.InfrastructureOperation, error) {
	return s.opRepo.ListOperationsByInfrastructure(ctx, infraID)
}

// CancelOperation cancels an in-flight operation. Completed operations cannot
// be cancelled.
func (s *InfrastructureService) CancelOperation(ctx context.Context, opID string) (*domain.InfrastructureOperation, error) {
	op, err := s.opRepo.GetOperationByID(ctx, opID)
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

	// Record cancellation promptly; the background operation also observes the
	// cancelled context and finalizes its own status.
	now := time.Now().UTC()
	op.Status = domain.OpCancelled
	op.CompletedAt = &now
	op.Error = "cancelled by user"
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil {
		return nil, err
	}
	return op, nil
}

// Metrics exposes the infrastructure operation counters for observability.
func (s *InfrastructureService) Metrics() *provisioning.Metrics {
	return s.metrics
}

// runOperation executes one provisioning operation in the background and
// persists every outcome. It owns the per-infrastructure lock for the entire
// duration.
func (s *InfrastructureService) runOperation(ctx context.Context, op *domain.InfrastructureOperation, infra *domain.Infrastructure) {
	finish := s.metrics.OperationStarted()
	defer func() {
		finish(op.Status == domain.OpFailed || op.Status == domain.OpCancelled)
	}()

	now := time.Now().UTC()
	op.Status = domain.OpRunning
	op.StartedAt = &now
	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("updating operation %s to running: %v", op.ID, err)
	}

	priorPhase := infra.Status.Phase
	infra.Status.Phase = operationPhase(op.Type)
	infra.Status.LastOperation = op.ID
	infra.Status.Error = ""
	if err := s.infraRepo.Update(ctx, infra); err != nil && s.logger != nil {
		s.logger.Printf("updating infrastructure %s phase: %v", infra.ID, err)
	}

	var opErr error
	switch op.Type {
	case domain.OperationPlan:
		result, err := s.provisioner.Plan(ctx, infra)
		if err != nil {
			opErr = err
		} else {
			op.Changes = result.Changes
		}
	case domain.OperationApply:
		result, err := s.provisioner.Apply(ctx, infra)
		if err != nil {
			opErr = err
		} else {
			op.Changes = result.Changes
			infra.Status.Nodes = result.Nodes
		}
	case domain.OperationDestroy:
		if err := s.provisioner.Destroy(ctx, infra); err != nil {
			opErr = err
		} else {
			infra.Status.Nodes = nil
		}
	}

	completed := time.Now().UTC()
	op.CompletedAt = &completed

	switch {
	case opErr == nil:
		op.Status = domain.OpSucceeded
		switch op.Type {
		case domain.OperationApply:
			infra.Status.Phase = domain.InfraPhaseReady
		case domain.OperationDestroy:
			infra.Status.Phase = domain.InfraPhaseDestroyed
		case domain.OperationPlan:
			// A plan changes nothing; restore the phase it observed.
			infra.Status.Phase = priorPhase
		}
	case provisioning.IsCancelled(opErr):
		op.Status = domain.OpCancelled
		op.Error = "cancelled"
		infra.Status.Phase = priorPhase
	default:
		op.Status = domain.OpFailed
		op.Error = opErr.Error()
		infra.Status.Phase = domain.InfraPhaseFailed
	}

	infra.Status.LastOperation = op.ID
	infra.Status.Error = ""
	infra.UpdatedAt = time.Now().UTC()

	if err := s.opRepo.UpdateOperation(ctx, op); err != nil && s.logger != nil {
		s.logger.Printf("finalizing operation %s: %v", op.ID, err)
	}
	if err := s.infraRepo.Update(ctx, infra); err != nil && s.logger != nil {
		s.logger.Printf("finalizing infrastructure %s: %v", infra.ID, err)
	}

	s.lock.Lock()
	delete(s.cancels, op.ID)
	s.lock.Unlock()
	s.unlock(infra.ID)
}

// operationPhase maps an operation type to the infrastructure phase shown while
// it runs.
func operationPhase(opType domain.OperationType) domain.InfrastructurePhase {
	switch opType {
	case domain.OperationPlan:
		return domain.InfraPhasePlanning
	case domain.OperationApply:
		return domain.InfraPhaseApplying
	case domain.OperationDestroy:
		return domain.InfraPhaseDestroying
	}
	return domain.InfraPhasePending
}

// tryLock acquires the per-infrastructure lock, returning false when a
// concurrent operation already holds it.
func (s *InfrastructureService) tryLock(infraID string) bool {
	return s.lockFor(infraID).TryLock()
}

func (s *InfrastructureService) unlock(infraID string) {
	s.lockFor(infraID).Unlock()
}

func (s *InfrastructureService) lockFor(infraID string) *sync.Mutex {
	s.lock.Lock()
	defer s.lock.Unlock()
	mu, ok := s.locks[infraID]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[infraID] = mu
	}
	return mu
}