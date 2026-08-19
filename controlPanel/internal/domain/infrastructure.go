package domain

import (
	"errors"
	"strings"
	"time"
)

// Infrastructure validation sentinels. Services map these to HTTP 400
// responses; callers should use errors.Is rather than string matching.
var (
	ErrEmptyName        = errors.New("name is required")
	ErrNameTooLong      = errors.New("name must be at most 63 characters")
	ErrNameInvalidChars = errors.New("name must not contain spaces or slashes")
	ErrNodeCountMin     = errors.New("node_count must be at least 1")
	ErrCPUMin           = errors.New("cpu must be at least 1")
	ErrMemoryMin        = errors.New("memory_mb must be at least 256")
	ErrDiskMin          = errors.New("disk_gb must be at least 1")
	ErrImageRequired    = errors.New("image is required")
	ErrProviderRequired = errors.New("provider is required")
)

// InfrastructurePhase is the lifecycle phase of an infrastructure deployment.
//
//	Pending ──► Planning ──► Applying ──► Ready
//	              │            │
//	              └──── failure ┘
//	                             ▼
//	                          Failed
//	Destroying ──► Destroyed
type InfrastructurePhase string

// Infrastructure lifecycle phases.
const (
	InfraPhasePending    InfrastructurePhase = "PENDING"
	InfraPhasePlanning   InfrastructurePhase = "PLANNING"
	InfraPhaseApplying   InfrastructurePhase = "APPLYING"
	InfraPhaseReady      InfrastructurePhase = "READY"
	InfraPhaseFailed     InfrastructurePhase = "FAILED"
	InfraPhaseDestroying InfrastructurePhase = "DESTROYING"
	InfraPhaseDestroyed  InfrastructurePhase = "DESTROYED"
)

// allInfraPhases is the canonical set of valid infrastructure phases.
var allInfraPhases = []InfrastructurePhase{
	InfraPhasePending,
	InfraPhasePlanning,
	InfraPhaseApplying,
	InfraPhaseReady,
	InfraPhaseFailed,
	InfraPhaseDestroying,
	InfraPhaseDestroyed,
}

// Valid reports whether p is a known infrastructure phase.
func (p InfrastructurePhase) Valid() bool {
	for _, candidate := range allInfraPhases {
		if p == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether the phase is a final state. Infrastructure in a
// terminal phase is not executing an operation.
func (p InfrastructurePhase) Terminal() bool {
	return p == InfraPhaseReady || p == InfraPhaseFailed || p == InfraPhaseDestroyed
}

// InfrastructureSpec is the declarative desired state of an infrastructure
// deployment. It is provider-agnostic and carries no Terraform concepts.
type InfrastructureSpec struct {
	Name     string
	NodeCount int
	CPU       int
	MemoryMB  int
	DiskGB    int
	Image     string
	Provider  string
}

// Validate enforces the rules an InfrastructureSpec must satisfy.
func (s InfrastructureSpec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return ErrEmptyName
	}
	if len(s.Name) > 63 {
		return ErrNameTooLong
	}
	if strings.ContainsAny(s.Name, " /\\") {
		return ErrNameInvalidChars
	}
	if s.NodeCount < 1 {
		return ErrNodeCountMin
	}
	if s.CPU < 1 {
		return ErrCPUMin
	}
	if s.MemoryMB < 256 {
		return ErrMemoryMin
	}
	if s.DiskGB < 1 {
		return ErrDiskMin
	}
	if strings.TrimSpace(s.Image) == "" {
		return ErrImageRequired
	}
	if strings.TrimSpace(s.Provider) == "" {
		return ErrProviderRequired
	}
	return nil
}

// InfrastructureNode describes one provisioned machine.
type InfrastructureNode struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	IP    string `json:"ip"`
	State string `json:"state"`
}

// InfrastructureStatus is the observed state of an infrastructure deployment.
type InfrastructureStatus struct {
	Phase           InfrastructurePhase
	Nodes           []InfrastructureNode
	LastOperation   string
	Error           string
	BootstrapState  BootstrapPhase
}

// Infrastructure is the aggregate root for a declarative infrastructure
// deployment.
type Infrastructure struct {
	ID                string
	Spec              InfrastructureSpec
	Status            InfrastructureStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
	BootstrapState    BootstrapPhase
	BootstrapToken    string
	WireGuardPublicKey string
	PrivateNetworkIP  string
}

// BootstrapPhase is the lifecycle phase of a node bootstrap operation.
//
//	Pending ──► Connecting ──► Preparing ──► Networking
//	            │            │            │
//	            │            │            ├── InstallingAgent
//	            │            │            │
//	            │            │            └── Registering
//	            │            │
//	            │            └── Failed
//	            │
//	            └── Ready
type BootstrapPhase string

// Bootstrap lifecycle phases.
const (
	BootstrapPhasePending      BootstrapPhase = "PENDING"
	BootstrapPhaseConnecting   BootstrapPhase = "CONNECTING"
	BootstrapPhasePreparing    BootstrapPhase = "PREPARING"
	BootstrapPhaseNetworking   BootstrapPhase = "NETWORKING"
	BootstrapPhaseInstalling   BootstrapPhase = "INSTALLING"
	BootstrapPhaseRegistering  BootstrapPhase = "REGISTERING"
	BootstrapPhaseReady        BootstrapPhase = "READY"
	BootstrapPhaseFailed       BootstrapPhase = "FAILED"
)

// allBootstrapPhases is the canonical set of valid bootstrap phases.
var allBootstrapPhases = []BootstrapPhase{
	BootstrapPhasePending,
	BootstrapPhaseConnecting,
	BootstrapPhasePreparing,
	BootstrapPhaseNetworking,
	BootstrapPhaseInstalling,
	BootstrapPhaseRegistering,
	BootstrapPhaseReady,
	BootstrapPhaseFailed,
}

// Valid reports whether p is a known bootstrap phase.
func (p BootstrapPhase) Valid() bool {
	for _, candidate := range allBootstrapPhases {
		if p == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether the phase is a final state. Bootstrap in a
// terminal phase is not executing an operation.
func (p BootstrapPhase) Terminal() bool {
	return p == BootstrapPhaseReady || p == BootstrapPhaseFailed
}

// OperationType identifies the kind of provisioning operation.
type OperationType string

// Provisioning operation types.
const (
	OperationPlan    OperationType = "PLAN"
	OperationApply   OperationType = "APPLY"
	OperationDestroy OperationType = "DESTROY"
	OperationBootstrap OperationType = "BOOTSTRAP"
)

// Valid reports whether t is a known operation type.
func (t OperationType) Valid() bool {
	return t == OperationPlan || t == OperationApply || t == OperationDestroy || t == OperationBootstrap
}

// OperationStatus is the lifecycle status of a provisioning operation.
type OperationStatus string

// Operation lifecycle statuses.
const (
	OpPending   OperationStatus = "PENDING"
	OpRunning   OperationStatus = "RUNNING"
	OpSucceeded OperationStatus = "SUCCEEDED"
	OpFailed    OperationStatus = "FAILED"
	OpCancelled OperationStatus = "CANCELLED"
)

// allOpStatuses is the canonical set of valid operation statuses.
var allOpStatuses = []OperationStatus{
	OpPending,
	OpRunning,
	OpSucceeded,
	OpFailed,
	OpCancelled,
}

// Valid reports whether s is a known operation status.
func (s OperationStatus) Valid() bool {
	for _, candidate := range allOpStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether the operation reached a final state.
func (s OperationStatus) Terminal() bool {
	return s == OpSucceeded || s == OpFailed || s == OpCancelled
}

// ChangeSummary describes what a plan/apply would or did do.
type ChangeSummary struct {
	ToCreate  int
	ToModify  int
	ToDestroy int
}

// Empty reports whether no changes are pending.
func (c ChangeSummary) Empty() bool {
	return c.ToCreate == 0 && c.ToModify == 0 && c.ToDestroy == 0
}

// InfrastructureOperation is a long-running provisioning operation (plan,
// apply or destroy) against one infrastructure deployment.
type InfrastructureOperation struct {
	ID               string
	InfrastructureID string
	Type             OperationType
	Status           OperationStatus
	Changes          ChangeSummary
	StartedAt        *time.Time
	CompletedAt      *time.Time
	Error            string
	CreatedAt        time.Time
}