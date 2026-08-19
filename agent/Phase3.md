# AETHER-GRID — Phase 3: Reconciliation Engine

## Implementation Prompt

You are continuing development of **AETHER-GRID**, an infrastructure automation and distributed edge Kubernetes control system.

Phase 1 established the Control Plane.

Phase 2 established the Edge Node Agent.

Now implement **Phase 3: the Reconciliation Engine**.

The purpose of this phase is to turn AETHER-GRID from a system that merely records state into a system that **continuously observes actual state, compares it against desired state, detects drift, and executes controlled reconciliation actions**.

This phase is foundational.

The implementation must establish a clean reconciliation architecture that later phases can extend to Kubernetes, Terraform, WireGuard, and infrastructure recovery.

---

# 1. Existing Architecture

The current system conceptually looks like:

```text
                         AETHER-GRID
                              │
                     ┌────────┴────────┐
                     │   Control Plane │
                     │                 │
                     │ API             │
                     │ Node Registry   │
                     │ Desired State   │
                     │ Actual State    │
                     └────────┬────────┘
                              │
                              ↕
                       Edge Node Agent
                              │
                              ▼
                         Edge Node
```

Phase 3 adds:

```text
                         AETHER-GRID
                              │
                     ┌────────┴────────┐
                     │   Control Plane │
                     │                 │
                     │ Desired State   │
                     │ Actual State    │
                     │                 │
                     │ Reconciler      │
                     └────────┬────────┘
                              │
                    ┌─────────┴─────────┐
                    │                   │
                    ▼                   ▼
              Observe State       Take Action
                    │                   │
                    └─────────┬─────────┘
                              ▼
                       Desired = Actual
```

---

# 2. Core Objective

Implement a **controller-style reconciliation loop**.

The system must continuously answer:

> "Does the actual state of each node match the desired state?"

If yes:

```text
Desired == Actual
       ↓
   IN_SYNC
```

If no:

```text
Desired != Actual
       ↓
DRIFT_DETECTED
       ↓
Determine corrective action
       ↓
Execute action
       ↓
Observe again
       ↓
Desired == Actual
```

The reconciliation process must be designed around **eventual convergence**.

---

# 3. Important Scope Boundary

Phase 3 is about the **reconciliation architecture**, not real infrastructure automation.

Do NOT implement:

* Kubernetes API integration
* Kubernetes Operators
* Terraform
* WireGuard
* Cloud provider APIs
* Real infrastructure provisioning
* Prometheus
* Grafana
* Production authentication
* Multi-cluster management
* Automatic VM creation
* Real server recovery

Those belong to later phases.

For Phase 3, reconciliation actions operate against the **existing Edge Node Agent / simulated node environment**.

---

# 4. Core Design Principle

Use the Kubernetes-inspired controller model:

```text
Observe
   ↓
Compare
   ↓
Determine
   ↓
Act
   ↓
Observe again
```

The controller should **never assume that the action succeeded**.

After taking an action:

```text
Action
  ↓
Observe actual state
  ↓
Compare again
```

This distinction is critical.

Do not implement:

```text
desired != actual
       ↓
perform action
       ↓
assume success
```

Instead:

```text
desired != actual
       ↓
perform action
       ↓
observe
       ↓
verify
```

---

# 5. Why Reconciliation Instead of Sequential Automation?

The system should NOT behave like a traditional deployment script:

```text
Step 1
Step 2
Step 3
Step 4
Done
```

That model assumes operations happen once and succeed.

Infrastructure is different.

Nodes can:

* disappear
* restart
* become unreachable
* change configuration
* partially complete operations
* reconnect
* fail during recovery

Therefore AETHER-GRID should continuously converge the system toward the desired state.

This is the fundamental reason for the reconciliation architecture.

---

# 6. Desired State Model

Extend the existing desired state model if necessary.

A node should have a desired configuration such as:

```json
{
  "status": "READY",
  "kubernetes_enabled": true,
  "wireguard_enabled": true
}
```

Do not make desired state simply equal to a single status string if the existing architecture can reasonably support structured state.

The long-term model should support:

```text
Desired Node State
├── Lifecycle state
├── Kubernetes configuration
├── Networking configuration
└── Other infrastructure configuration
```

However, keep Phase 3 implementation intentionally small.

---

# 7. Actual State Model

Actual state represents what the system currently observes.

Example:

```json
{
  "status": "OFFLINE",
  "kubernetes_enabled": true,
  "wireguard_enabled": false,
  "last_heartbeat": "..."
}
```

The reconciliation engine must never modify actual state simply because desired state says something different.

Actual state comes from observation.

---

# 8. State Comparison

Create a dedicated comparison mechanism.

Conceptually:

```go
type ReconciliationResult struct {
    NodeID        string
    Result        ReconciliationStatus
    DesiredState  NodeState
    ActualState   NodeState
    Differences   []Difference
}
```

Possible result:

```text
IN_SYNC
DRIFT_DETECTED
RECONCILING
RECONCILED
FAILED
```

Keep domain state strongly typed.

Avoid arbitrary strings scattered throughout the application.

---

# 9. Difference Model

The reconciliation engine should explain **why** two states differ.

For example:

```json
{
  "result": "DRIFT_DETECTED",
  "differences": [
    {
      "field": "status",
      "desired": "READY",
      "actual": "OFFLINE"
    },
    {
      "field": "wireguard_enabled",
      "desired": true,
      "actual": false
    }
  ]
}
```

Do not simply return:

```text
"states are different"
```

The controller needs structured differences to determine what action to take.

---

# 10. Reconciliation Actions

Create an abstraction for actions.

Conceptually:

```go
type ReconciliationAction interface {
    Execute(ctx context.Context, node Node) error
}
```

However, choose the exact interface based on the existing codebase.

The action system should eventually support actions such as:

```text
START_NODE
STOP_NODE
CONFIGURE_NODE
CONNECT_NETWORK
DISCONNECT_NETWORK
ENABLE_KUBERNETES
DISABLE_KUBERNETES
```

For Phase 3, only implement actions that can actually be performed safely against the Phase 2 node-agent environment.

Do NOT create fake implementations that claim to provision infrastructure.

If a future operation cannot yet be performed, represent it explicitly as unsupported.

---

# 11. Action Selection

Create a mechanism that maps differences to actions.

Conceptually:

```text
Difference
    │
    ▼
Action Planner
    │
    ▼
Required Actions
```

Example:

```text
Desired:
READY

Actual:
OFFLINE

        ↓

Difference:
status mismatch

        ↓

Planned action:
RECOVER_NODE
```

The planner should be separate from the action executor.

This separation will become important when Kubernetes and Terraform are introduced later.

---

# 12. Action Execution

The executor should:

1. Receive a planned action
2. Execute it
3. Record the result
4. Return control to the reconciliation loop
5. Trigger another observation

Do not assume that an action makes the node healthy.

---

# 13. Reconciliation Loop

Implement a continuous reconciliation loop.

Conceptually:

```text
┌─────────────────────────┐
│                         │
│      Reconcile Node     │
│                         │
└────────────┬────────────┘
             │
             ▼
       Observe State
             │
             ▼
       Compare States
             │
        ┌────┴────┐
        │         │
     IN SYNC    DRIFT
        │         │
        │         ▼
        │    Plan Action
        │         │
        │         ▼
        │    Execute Action
        │         │
        │         ▼
        │      Observe
        │         │
        └─────────┘
```

The loop should be configurable.

For example:

```text
RECONCILIATION_INTERVAL=10s
```

Do not hardcode the interval.

---

# 14. Concurrency

Multiple nodes must be reconciled independently.

For example:

```text
Node A → Reconciliation
Node B → Reconciliation
Node C → Reconciliation
```

These should not require one global lock.

Use Go goroutines where appropriate.

However, do not create unlimited goroutines.

Implement a controlled concurrency strategy.

A reasonable initial design is a worker pool or bounded concurrency.

---

# 15. Why Not One Goroutine Per Node Forever?

A goroutine per node is simple, but an infrastructure control plane may eventually manage hundreds or thousands of nodes.

Unbounded goroutine creation can become problematic.

Therefore the architecture should allow reconciliation concurrency to be controlled.

For Phase 3, a small worker pool is acceptable.

Make the worker count configurable:

```text
RECONCILIATION_WORKERS=4
```

---

# 16. Retry Strategy

Reconciliation actions can fail.

Implement controlled retries.

Example:

```text
Attempt 1
   ↓
Failed
   ↓
Wait
   ↓
Attempt 2
   ↓
Failed
   ↓
Wait
   ↓
Attempt 3
   ↓
Success
```

Use exponential backoff with a maximum delay.

Conceptually:

```text
1s
2s
4s
8s
...
```

with a configurable maximum.

Do not retry indefinitely without recording failure state.

---

# 17. Why Exponential Backoff?

Without backoff:

```text
failure
↓
retry immediately
↓
failure
↓
retry immediately
↓
failure
↓
...
```

This can create:

* CPU waste
* API pressure
* network pressure
* log flooding
* cascading failures

Backoff gives failing infrastructure time to recover.

---

# 18. Failure State

A reconciliation failure must be represented explicitly.

Example:

```text
RECONCILIATION_FAILED
```

The result should include:

```text
node
action
attempt
error
timestamp
```

Do not silently swallow errors.

---

# 19. Idempotency

Every reconciliation action must be designed to be safe to retry.

For example:

```text
Node already READY
       ↓
"Make READY" action
       ↓
No harmful duplicate operation
```

The controller must assume:

> An action may execute more than once.

This is essential for distributed systems.

---

# 20. Heartbeat Integration

Use the Phase 2 heartbeat mechanism as part of actual-state observation.

For example:

```text
last heartbeat < threshold
        ↓
Node considered OFFLINE
```

Make the threshold configurable.

Example:

```text
NODE_HEARTBEAT_TIMEOUT=30s
```

Do not immediately mark a node offline because of one missed heartbeat.

Use a timeout window.

---

# 21. Node Health Determination

Create a clear mechanism for determining health.

Conceptually:

```text
last heartbeat
      │
      ▼
Is heartbeat recent?
      │
 ┌────┴────┐
 YES       NO
  │         │
  ▼         ▼
HEALTHY   OFFLINE
```

The exact health model can become more sophisticated later.

For Phase 3, heartbeat freshness is sufficient.

---

# 22. Reconciliation Triggering

Support at least two reconciliation triggers.

### Periodic reconciliation

The controller periodically evaluates nodes.

```text
Every N seconds
      ↓
Reconcile fleet
```

### Event-driven reconciliation

Certain events should trigger reconciliation immediately.

Examples:

```text
Node registered
Node heartbeat received
Node state changed
Desired state changed
```

Do not wait for the next periodic interval when an immediate reconciliation is appropriate.

---

# 23. Why Both Periodic and Event-Driven?

Event-driven reconciliation gives fast response.

Periodic reconciliation provides resilience.

If an event is lost:

```text
Event missed
   ↓
Periodic loop
   ↓
State eventually detected
```

This creates a more robust controller.

---

# 24. Work Queue

If appropriate for the existing architecture, implement a reconciliation work queue.

Conceptually:

```text
Node Event
    │
    ▼
Work Queue
    │
    ▼
Worker
    │
    ▼
Reconcile Node
```

The queue should prevent uncontrolled duplicate work.

If the same node receives multiple events:

```text
edge-01
edge-01
edge-01
edge-01
```

the controller should avoid unnecessarily executing four simultaneous reconciliations.

---

# 25. Per-Node Reconciliation Serialization

A single node should not be reconciled concurrently by multiple workers.

For example, prevent:

```text
Worker 1 → edge-01 reconciliation
Worker 2 → edge-01 reconciliation
```

at the same time.

Different nodes may reconcile concurrently:

```text
Worker 1 → edge-01
Worker 2 → edge-02
Worker 3 → edge-03
```

This prevents conflicting actions on the same node.

---

# 26. Reconciliation State

Track useful metadata.

At minimum:

```text
last_reconciliation
last_successful_reconciliation
last_reconciliation_error
reconciliation_attempts
```

If the existing database model needs to be extended, create a migration.

Do not destroy existing data.

---

# 27. Reconciliation History

Create a lightweight reconciliation record.

For example:

```text
reconciliation_events
```

Fields may include:

```text
id
node_id
started_at
completed_at
result
action
error
attempt
```

Do not turn this into a massive event-sourcing architecture.

The purpose is observability and debugging.

---

# 28. API Extensions

Extend the existing API.

Add:

```text
POST /nodes/{id}/reconcile
GET  /nodes/{id}/reconciliation
GET  /nodes/{id}/reconciliation/history
```

Optional:

```text
GET /reconciliation/status
```

The API should expose enough information to understand what the controller is doing.

---

# 29. Manual Reconciliation

The existing manual endpoint:

```text
POST /nodes/{id}/reconcile
```

should trigger reconciliation immediately.

This should be useful for debugging.

Example:

```bash
curl -X POST \
  http://localhost:8080/nodes/{id}/reconcile
```

Return a structured result.

---

# 30. Automatic Reconciliation

The controller should automatically reconcile nodes according to the configured interval.

Example:

```text
RECONCILIATION_INTERVAL=10s
```

The application should start the reconciliation subsystem during startup.

The application should shut it down gracefully.

---

# 31. Graceful Shutdown

When the control plane receives:

```text
SIGINT
SIGTERM
```

it should:

1. Stop accepting new work
2. Stop scheduling reconciliation
3. Allow currently running operations to finish where reasonable
4. Close the work queue
5. Close database resources
6. Shut down HTTP server
7. Exit cleanly

Use Go `context.Context` for cancellation.

---

# 32. Context Propagation

Use `context.Context` throughout:

```text
HTTP request
    ↓
Service
    ↓
Reconciler
    ↓
Action
    ↓
Repository
```

Do not create arbitrary background contexts inside functions that already receive a context.

Respect cancellation.

---

# 33. Configuration

Add configuration for:

```text
RECONCILIATION_INTERVAL
RECONCILIATION_WORKERS
NODE_HEARTBEAT_TIMEOUT
RECONCILIATION_MAX_RETRIES
RECONCILIATION_MAX_BACKOFF
```

Use sensible development defaults.

Do not introduce a complex configuration framework.

---

# 34. Logging

Log important reconciliation events.

Example:

```text
INFO  reconciliation started node=edge-01
INFO  drift detected node=edge-01
INFO  action planned node=edge-01 action=RECOVER_NODE
INFO  action completed node=edge-01
INFO  reconciliation completed node=edge-01 result=RECONCILED
```

On failure:

```text
ERROR reconciliation failed node=edge-01 action=RECOVER_NODE attempt=2 error="..."
```

Avoid excessive logs on every successful periodic reconciliation.

---

# 35. Metrics

Do NOT integrate Prometheus yet.

However, structure the reconciliation service so metrics can be added later.

For example, keep explicit counters internally or expose clean instrumentation points.

Future metrics will include:

```text
reconciliation_total
reconciliation_success_total
reconciliation_failure_total
reconciliation_duration_seconds
reconciliation_drift_total
```

Do not add a Prometheus dependency in Phase 3.

---

# 36. Testing Strategy

Testing is critical.

Implement the following.

## Unit Tests

Test:

* State comparison
* Difference detection
* Action planning
* Retry logic
* Backoff calculation
* Health determination
* Desired/actual state transitions

---

## Reconciliation Tests

Test:

### Test 1 — Already synchronized

```text
Desired = READY
Actual  = READY

Expected:
IN_SYNC
```

### Test 2 — Drift detected

```text
Desired = READY
Actual  = OFFLINE

Expected:
DRIFT_DETECTED
```

### Test 3 — Successful recovery

```text
Desired = READY
Actual  = OFFLINE

Action succeeds

Expected:
RECONCILED
```

### Test 4 — Action failure

```text
Desired = READY
Actual = OFFLINE

Action fails

Expected:
RECONCILIATION_FAILED
```

### Test 5 — Retry

```text
Attempt 1 → failure
Attempt 2 → failure
Attempt 3 → success

Expected:
RECONCILED
```

---

# 37. Concurrency Tests

Verify:

```text
Node A reconciles
Node B reconciles
Node C reconciles
```

can happen concurrently.

Also verify:

```text
Node A
Node A
Node A
```

does not produce concurrent reconciliation executions for the same node.

Run:

```bash
go test -race ./...
```

and resolve races introduced by this implementation.

---

# 38. Failure Tests

Simulate:

* Node disappears
* Heartbeat stops
* Action fails
* Database temporarily returns an error
* Reconciliation context is cancelled
* Control plane shuts down during reconciliation

The system must fail predictably.

---

# 39. Integration Test

Build a complete scenario:

```text
1. Start control plane
2. Register edge-01
3. Set desired state = READY
4. Node reports READY
5. Reconciliation → IN_SYNC
6. Stop heartbeats
7. Wait beyond heartbeat timeout
8. Node becomes OFFLINE
9. Reconciliation detects drift
10. Recovery action is attempted
11. Node becomes reachable again
12. Heartbeat resumes
13. Reconciliation runs again
14. State becomes IN_SYNC
```

The exact recovery action may use the Phase 2 agent's capabilities.

Do not fake infrastructure recovery.

If the current Edge Agent cannot perform a recovery operation, implement the smallest legitimate simulation at the agent layer and clearly document it.

---

# 40. Important Distinction: Simulation vs Fake Success

Do not implement:

```go
func recoverNode(...) error {
    return nil
}
```

just to make the test pass.

That is not acceptable.

If Phase 2 does not yet support a real recovery operation, explicitly model the limitation.

For example:

```text
RECOVERY_UNSUPPORTED
```

or implement a legitimate local action such as:

```text
request agent to restart its simulated workload
```

The system should never claim an operation succeeded when it did not.

---

# 41. Repository Changes

Extend the existing repository interfaces instead of bypassing them.

If reconciliation history is introduced, add a separate repository abstraction.

For example:

```go
type ReconciliationRepository interface {
    Create(...)
    GetLatest(...)
    ListByNode(...)
}
```

Do not place SQL directly inside the reconciler.

Maintain:

```text
HTTP
 ↓
Service
 ↓
Reconciler
 ↓
Repository
 ↓
Database
```

---

# 42. Architecture

The resulting architecture should approximately be:

```text
                         HTTP API
                            │
                            ▼
                       Application
                         Services
                            │
                            ▼
                    Reconciliation Engine
                            │
               ┌────────────┼────────────┐
               │            │            │
               ▼            ▼            ▼
           Observer      Planner      Executor
               │            │            │
               └────────────┼────────────┘
                            │
                            ▼
                        Edge Agent
                            │
                            ▼
                         Node State
```

The exact implementation can combine Observer/Planner/Executor where appropriate.

Do not create artificial abstractions solely to match the diagram.

---

# 43. Observer

Create a clear mechanism for retrieving actual state.

Conceptually:

```go
type StateObserver interface {
    Observe(ctx context.Context, nodeID string) (NodeState, error)
}
```

The observer should be replaceable later.

Future implementations may observe:

* Edge Agent
* Kubernetes
* Terraform
* Cloud APIs
* Operating system

For Phase 3, use the available Phase 2 mechanism.

---

# 44. Planner

Create a mechanism that determines what should happen.

Conceptually:

```go
type ActionPlanner interface {
    Plan(desired NodeState, actual NodeState) ([]Action, error)
}
```

The planner must not execute actions.

This separation is important.

---

# 45. Executor

Create a mechanism for executing actions.

Conceptually:

```go
type ActionExecutor interface {
    Execute(ctx context.Context, action Action) error
}
```

The executor must not decide what actions are necessary.

---

# 46. Why Separate Observer, Planner, and Executor?

Because later phases will introduce new infrastructure.

Today:

```text
Observer → Edge Agent
Executor → Edge Agent
```

Later:

```text
Observer → Kubernetes
Executor → Kubernetes
```

Later:

```text
Observer → Terraform
Executor → Terraform
```

This lets the reconciliation engine remain stable while infrastructure integrations evolve.

Do not tightly couple the reconciler to Kubernetes or Terraform.

---

# 47. Action Safety

Actions must declare whether they are safe to retry.

Conceptually:

```text
Action
├── Type
├── Node ID
├── Parameters
└── Retry Policy
```

For Phase 3, all implemented actions should be idempotent or explicitly marked as non-retryable.

Do not blindly retry arbitrary side effects.

---

# 48. State Machine

If necessary, formalize node lifecycle transitions.

Example:

```text
OFFLINE
   │
   ▼
RECOVERING
   │
   ▼
READY
```

Avoid allowing impossible transitions such as:

```text
PROVISIONING → DELETED → READY
```

without an explicit lifecycle operation.

Use validation around transitions.

---

# 49. Database Consistency

When updating:

```text
actual state
last heartbeat
reconciliation state
```

use appropriate transactions where multiple pieces of state must remain consistent.

Do not introduce distributed transactions.

SQLite transactions are sufficient for Phase 3.

---

# 50. No Event Sourcing

Do NOT redesign the system as event sourcing.

The reconciliation history table is only an operational history.

Current state remains the authoritative state.

---

# 51. No Message Broker

Do NOT introduce Kafka, RabbitMQ, NATS, Redis Streams, or another message broker.

The Phase 3 work queue should be internal to the control plane.

A broker would add infrastructure that is unnecessary at this stage.

A future distributed control plane could revisit this decision.

---

# 52. No Distributed Leader Election

Do not implement leader election yet.

Phase 3 assumes a single active control-plane instance.

High availability is outside the current scope.

---

# 53. Why Single Control Plane?

The purpose of Phase 3 is to understand reconciliation, not distributed control-plane consensus.

Adding multiple control-plane instances would introduce:

* Leader election
* Split-brain concerns
* Distributed locking
* Duplicate reconciliation
* Consensus/state synchronization

Those problems should be addressed only if the architecture later requires HA.

---

# 54. API Behavior During Reconciliation

If reconciliation is asynchronous, the API should not block for long-running operations.

For example:

```text
POST /nodes/{id}/reconcile
        │
        ▼
Queue reconciliation
        │
        ▼
202 Accepted
```

Then:

```text
GET /nodes/{id}/reconciliation
```

can report progress.

If the existing implementation is synchronous and actions are guaranteed to be short-lived, a synchronous endpoint is acceptable for manual reconciliation.

Prefer asynchronous behavior for the automatic controller.

---

# 55. Race Conditions to Consider

Pay particular attention to:

```text
Heartbeat arrives
        │
        ├── Reconciliation reads node
        │
        └── State changes
```

and:

```text
Desired state changes
        │
        ├── Reconciliation starts
        │
        └── Old desired state is used
```

The reconciliation operation should operate on a coherent state snapshot.

Do not accidentally overwrite newer state with stale data.

---

# 56. Reconciliation Generation / Version

If useful, add a state version or generation number.

Example:

```text
desired_generation = 7
actual_generation  = 6
```

This allows future controllers to detect stale reconciliation work.

Do not overcomplicate this if the current architecture does not need it.

---

# 57. Documentation

Update the README with a new section:

```text
## Reconciliation
```

Explain:

* Desired state
* Actual state
* Drift
* Controller loop
* Retry behavior
* Failure handling
* Event-driven reconciliation
* Periodic reconciliation

Include a diagram.

Also document the relevant environment variables.

---

# 58. Architecture Decision Record

Create a document describing the major Phase 3 decisions.

At minimum:

| Decision             | Choice              | Reason                              |
| -------------------- | ------------------- | ----------------------------------- |
| Reconciliation model | Controller loop     | Continuous convergence              |
| State model          | Desired vs actual   | Declarative infrastructure          |
| Triggering           | Event + periodic    | Fast response + eventual recovery   |
| Concurrency          | Bounded workers     | Prevent resource exhaustion         |
| Retry                | Exponential backoff | Avoid retry storms                  |
| Actions              | Idempotent          | Safe retries                        |
| Observer             | Abstract interface  | Future infrastructure integrations  |
| Planner              | Separate component  | Separate decision from execution    |
| Executor             | Separate component  | Replaceable infrastructure actions  |
| Queue                | In-memory           | Sufficient for single control plane |
| Broker               | None                | Avoid premature infrastructure      |
| HA                   | None                | Outside Phase 3 scope               |

For each major alternative, explain why it was not chosen.

---

# 59. Alternatives Explicitly Rejected

Document these:

## Message Broker

Not used because Phase 3 is a single-process control plane and does not require distributed event delivery.

## Redis

Not used because an in-memory queue is sufficient.

## Kafka

Not used because event streaming is unnecessary for the current scale and would introduce significant operational complexity.

## Kubernetes Informers

Not used because Kubernetes integration belongs to Phase 4/5.

## Temporal

Not used because AETHER-GRID is implementing its own controller/reconciliation model for learning and architectural understanding.

## Full Event Sourcing

Not used because current state plus lightweight history is sufficient.

## Distributed Leader Election

Not used because Phase 3 uses a single control-plane instance.

---

# 60. Definition of Done

Phase 3 is complete only when:

* [ ] Desired state exists
* [ ] Actual state exists
* [ ] State comparison is structured
* [ ] Differences are represented explicitly
* [ ] Reconciliation engine exists
* [ ] Observer abstraction exists
* [ ] Planner abstraction exists
* [ ] Executor abstraction exists
* [ ] Reconciliation actions are implemented where supported
* [ ] Actions are idempotent
* [ ] Automatic reconciliation works
* [ ] Manual reconciliation works
* [ ] Event-driven reconciliation works
* [ ] Periodic reconciliation works
* [ ] Heartbeat timeout detection works
* [ ] Retry logic works
* [ ] Exponential backoff works
* [ ] Reconciliation failures are recorded
* [ ] Reconciliation history is persisted
* [ ] Same node cannot reconcile concurrently
* [ ] Different nodes can reconcile concurrently
* [ ] Graceful shutdown works
* [ ] Context cancellation works
* [ ] Unit tests pass
* [ ] Integration tests pass
* [ ] Failure tests pass
* [ ] Race detector passes
* [ ] `go fmt ./...` passes
* [ ] `go vet ./...` passes
* [ ] `go test ./...` passes
* [ ] README is updated
* [ ] Architecture decisions are documented

---

# 61. Verification Scenario

After implementation, demonstrate this exact scenario.

Start with:

```text
Node: edge-01

Desired:
READY

Actual:
READY
```

Result:

```text
IN_SYNC
```

Then simulate the node becoming unreachable.

```text
Desired:
READY

Actual:
OFFLINE
```

Result:

```text
DRIFT_DETECTED
```

The controller should:

```text
Detect
  ↓
Plan
  ↓
Execute
  ↓
Observe
  ↓
Verify
```

If recovery succeeds:

```text
Desired:
READY

Actual:
READY

Result:
IN_SYNC
```

If recovery fails:

```text
Result:
RECONCILIATION_FAILED
```

with:

* action
* attempt count
* error
* timestamps

visible through the API/history.

---

# 62. Final Implementation Instruction

Before modifying the repository:

1. Inspect the existing Phase 1 and Phase 2 implementation.
2. Understand the current domain models.
3. Do not rewrite working components unnecessarily.
4. Preserve existing APIs unless there is a strong architectural reason to change them.
5. Add database migrations instead of modifying existing tables destructively.
6. Reuse existing interfaces where appropriate.
7. Implement Phase 3 incrementally inside the existing architecture.
8. Do not implement Phase 4+ functionality.

After implementation:

1. Run formatting.
2. Run static analysis.
3. Run unit tests.
4. Run integration tests.
5. Run race tests.
6. Start the control plane.
7. Demonstrate manual reconciliation.
8. Demonstrate automatic reconciliation.
9. Demonstrate a simulated node failure.
10. Demonstrate retry behavior.
11. Demonstrate recovery.
12. Verify persistence after restart.

Report:

* What was implemented
* Files created/modified
* Architecture changes
* Database migrations
* API changes
* Configuration changes
* Tests performed
* Test results
* Known limitations
* What remains for Phase 4

**Do not implement Phase 4.**
