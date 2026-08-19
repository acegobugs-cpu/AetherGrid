# AETHER-GRID — Phase 2: Edge Node Agent

## Implementation Prompt

You are continuing development of **AETHER-GRID**, an infrastructure automation and distributed edge Kubernetes control system.

Phase 1 established the central Control Plane.

Now implement **Phase 2: the Edge Node Agent**.

The Edge Node Agent is a lightweight Go service that runs on an individual edge machine and communicates with the AETHER-GRID Control Plane.

The goal of this phase is to create a real agent/control-plane communication model.

Do NOT implement Kubernetes, Terraform, WireGuard, cloud provisioning, Prometheus, or the reconciliation engine yet.

---

# 1. System Context

The architecture currently looks like:

                         AETHER-GRID
                              │
                              ▼
                       Control Plane
                              │
                              │ HTTP
                              ▼
                       Edge Node Agent
                              │
                              ▼
                         Edge Machine

The Control Plane knows:

- Which nodes exist
- Their desired state
- Their last heartbeat
- Their observed state

The Edge Agent runs on the actual machine and reports:

- Who it is
- Whether it is alive
- Its current state
- Basic machine information

The Control Plane may also send commands to the agent.

---

# 2. Phase 2 Objective

Build an independently runnable Go program:

    aether-agent

The agent must be capable of:

1. Starting on an edge machine
2. Loading its configuration
3. Identifying itself
4. Registering with the Control Plane
5. Sending periodic heartbeats
6. Reporting its current state
7. Receiving commands from the Control Plane
8. Executing a small set of safe local actions
9. Gracefully shutting down
10. Recovering from temporary Control Plane failures
11. Logging its activity
12. Being tested independently

The result should be a genuine agent process, not merely another API endpoint inside the Control Plane.

---

# 3. Important Scope Boundary

Do NOT implement these yet:

- Kubernetes
- kubelet management
- Kubernetes API clients
- Kubernetes cluster creation
- Kubernetes Operators
- CRDs
- Terraform
- WireGuard
- Cloud APIs
- VM provisioning
- Prometheus
- Grafana
- Production authentication
- TLS certificate management
- Automatic reconciliation
- Distributed control-plane HA
- Message brokers

Those belong to later phases.

Phase 2 is strictly:

> Control Plane ↔ Edge Agent communication and local node state management.

---

# 4. Technology Decision

Use:

    Go

The Control Plane and Edge Agent should use the same language.

## Why Go?

The agent is a long-running infrastructure process that needs:

- Low resource usage
- Good concurrency
- Strong networking support
- Easy deployment as a single binary
- Good Linux support
- Straightforward cross-compilation

Go is appropriate for lightweight infrastructure agents.

Do not introduce another language.

---

# 5. Agent Architecture

Use a small layered architecture:

    Agent
      │
      ├── Configuration
      │
      ├── Control Plane Client
      │
      ├── State Collector
      │
      ├── Command Handler
      │
      └── Agent Runtime

Conceptually:

                         aether-agent
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        State Collector   CP Client      Command Handler
              │               │               │
              ▼               ▼               ▼
          OS / Node       HTTP API       Local Actions

Keep this architecture simple.

Do not create unnecessary packages.

---

# 6. Agent Project Structure

Prefer a structure such as:

    agent/
    │
    ├── cmd/
    │   └── aether-agent/
    │       └── main.go
    │
    ├── internal/
    │   ├── config/
    │   │   └── config.go
    │   │
    │   ├── client/
    │   │   └── control_plane.go
    │   │
    │   ├── state/
    │   │   └── collector.go
    │   │
    │   ├── command/
    │   │   └── handler.go
    │   │
    │   └── agent/
    │       └── agent.go
    │
    ├── tests/
    │
    ├── go.mod
    ├── go.sum
    └── README.md

If the existing repository is a monorepo, integrate the agent into the existing repository structure rather than creating an unrelated repository.

Do not blindly duplicate existing infrastructure.

---

# 7. Agent Identity

Every agent needs a stable identity.

The agent should have:

    NODE_ID

However, the first registration should be able to obtain an ID from the Control Plane if one does not already exist.

Recommended behavior:

    First startup
        │
        ▼
    Agent has no NODE_ID
        │
        ▼
    Register with Control Plane
        │
        ▼
    Control Plane assigns UUID
        │
        ▼
    Agent persists identity locally

On subsequent restarts:

    Agent starts
        │
        ▼
    Load existing NODE_ID
        │
        ▼
    Reconnect to Control Plane

This prevents the same physical node from becoming a new node every time the agent restarts.

---

# 8. Why Persist Identity Locally?

Without persistent identity:

    restart agent
        ↓
    new UUID
        ↓
    Control Plane thinks it's a new machine

That would be incorrect.

The identity should survive agent restarts.

---

# 9. Local Agent State

The agent should maintain local state such as:

    Node ID
    Name
    Location
    IP address
    Agent version
    Operating system
    Architecture
    CPU count
    Memory information
    Uptime
    Last successful Control Plane communication
    Current agent status

Do not collect excessive hardware information yet.

Only collect information that is useful for the current architecture.

---

# 10. State Collection

Create a state collector abstraction.

Conceptually:

    type StateCollector interface {
        Collect(ctx context.Context) (NodeState, error)
    }

The implementation should gather information from the local machine.

At minimum:

- OS
- Architecture
- Hostname
- CPU count
- Memory
- Uptime
- Agent status

Use appropriate Go/Linux APIs.

Avoid shelling out to arbitrary commands when the Go standard library or a small well-maintained dependency can provide the information.

---

# 11. Why Not Shell Commands Everywhere?

Do NOT build the agent around:

    exec("hostname")
    exec("free")
    exec("uptime")
    exec("ip")
    ...

This would make the agent:

- OS-dependent
- fragile
- difficult to test
- vulnerable to command failures
- harder to control

Prefer Go APIs.

If a system-specific operation genuinely requires a platform command, isolate it behind an interface.

---

# 12. Agent Configuration

Configuration must be externalized.

At minimum support:

    CONTROL_PLANE_URL
    NODE_NAME
    NODE_LOCATION
    NODE_ID
    HEARTBEAT_INTERVAL
    STATE_REPORT_INTERVAL

Example:

    CONTROL_PLANE_URL=http://localhost:8080
    NODE_NAME=edge-01
    NODE_LOCATION=addis-01
    HEARTBEAT_INTERVAL=10s
    STATE_REPORT_INTERVAL=30s

NODE_ID may be omitted on first startup.

---

# 13. Configuration Defaults

Provide sensible defaults for development.

For example:

    CONTROL_PLANE_URL=http://localhost:8080
    HEARTBEAT_INTERVAL=10s
    STATE_REPORT_INTERVAL=30s

Do not hardcode machine-specific values.

---

# 14. Local Identity Storage

Persist the assigned NODE_ID locally.

For Phase 2, use a simple local file.

For example:

    data/node-id

The exact location should be configurable.

Example:

    AGENT_DATA_DIR=./data

The file should contain only the node identity or another minimal identity record.

Do not introduce SQLite for the agent unless the existing architecture genuinely requires it.

---

# 15. Why File Storage?

The agent needs to persist very little information.

Using SQLite would introduce unnecessary database complexity.

A small local file is:

- Simple
- Durable
- Easy to inspect
- Easy to back up
- Sufficient for Phase 2

---

# 16. Control Plane Client

Create a dedicated client:

    ControlPlaneClient

It should encapsulate all HTTP communication between the agent and Control Plane.

Do not put raw HTTP calls throughout the agent.

Conceptually:

    type ControlPlaneClient interface {
        Register(...)
        Heartbeat(...)
        GetDesiredState(...)
        ReportState(...)
        ExecuteCommand(...)
    }

Adjust the interface to match the actual Phase 1 API.

---

# 17. Registration

At startup:

    Agent
      │
      ▼
    POST /nodes
      │
      ▼
    Control Plane
      │
      ▼
    Node ID
      │
      ▼
    Agent stores ID

The agent should register itself if it does not already have a persistent identity.

If it already has an identity, it should attempt to reconnect to that existing node.

Do not create duplicate nodes.

---

# 18. Registration Payload

The agent should send information such as:

    {
      "name": "edge-01",
      "location": "addis-01",
      "ip_address": "...",
      "kubernetes_enabled": false,
      "wireguard_enabled": false
    }

Do not claim Kubernetes or WireGuard are enabled in Phase 2.

Those capabilities do not exist yet.

Use:

    kubernetes_enabled = false
    wireguard_enabled = false

until later phases implement them.

---

# 19. Heartbeats

The agent must periodically send heartbeats.

Example:

    every 10 seconds
        ↓
    POST /nodes/{id}/heartbeat
        ↓
    Control Plane

The heartbeat should contain enough information for the Control Plane to know:

> "This agent is alive and communicating."

---

# 20. Heartbeat Failure Handling

If the Control Plane is temporarily unavailable:

    heartbeat
       ↓
    connection failed
       ↓
    log warning
       ↓
    retry later

Do NOT terminate the agent.

An infrastructure agent should survive temporary Control Plane outages.

---

# 21. Retry Strategy

Use exponential backoff for connection failures.

Example:

    1s
    2s
    4s
    8s
    ...

with a configurable maximum.

Do not retry forever in a tight loop.

The normal heartbeat scheduler should continue operating.

---

# 22. Important Distinction

Do not confuse:

    heartbeat interval

with:

    retry interval

For example:

    heartbeat every 10 seconds

If one heartbeat fails:

    retry according to backoff

Do not permanently change the heartbeat schedule because one request failed.

---

# 23. State Reporting

The agent should periodically report its observed state.

Example:

    every 30 seconds
        ↓
    collect local state
        ↓
    send state to Control Plane

The Control Plane should be able to distinguish:

    heartbeat

from:

    detailed state report

Heartbeat answers:

> "Are you alive?"

State reporting answers:

> "What is your current condition?"

---

# 24. Desired State Retrieval

The agent should be able to retrieve its desired state from the Control Plane.

For example:

    GET /nodes/{id}/desired-state

The agent should not invent desired state locally.

The Control Plane is authoritative for desired state.

---

# 25. Local State vs Desired State

Maintain the distinction:

    Control Plane
        ↓
    Desired State

    Edge Agent
        ↓
    Actual State

Example:

    Control Plane:
        desired = READY

    Agent:
        actual = READY

Result:

    IN_SYNC

Another example:

    Control Plane:
        desired = READY

    Agent:
        actual = OFFLINE

Result:

    DRIFT

The agent should report reality.

It should not modify its reported state merely to match the desired state.

---

# 26. Command System

Implement a basic command mechanism.

The Control Plane should be able to instruct the agent to perform safe local operations.

For Phase 2, commands should be limited.

Implement at least:

    GET_STATUS

and one safe operational command such as:

    RESTART_AGENT

or another action that is genuinely safe and meaningful in the development environment.

Do NOT implement:

    CREATE_VM
    CREATE_KUBERNETES_CLUSTER
    CONFIGURE_WIREGUARD
    APPLY_TERRAFORM

Those belong to later phases.

---

# 27. Why Have Commands Now?

The purpose is to establish:

    Control Plane
         ↓
       Command
         ↓
      Agent
         ↓
       Action
         ↓
       Result

Later phases can replace simple commands with infrastructure operations.

This gives AETHER-GRID the beginnings of a control loop without prematurely implementing infrastructure automation.

---

# 28. Command Model

Commands should be explicit.

Conceptually:

    {
      "id": "...",
      "type": "GET_STATUS",
      "parameters": {}
    }

The agent should return:

    {
      "command_id": "...",
      "status": "SUCCESS",
      "result": {...}
    }

or:

    {
      "command_id": "...",
      "status": "FAILED",
      "error": "..."
    }

Use command IDs to make commands traceable.

---

# 29. Idempotency

Commands that can be retried must be safe.

For example:

    GET_STATUS

is naturally idempotent.

If a command such as:

    RESTART_AGENT

is implemented, carefully handle duplicate requests.

Do not assume the network delivers commands exactly once.

---

# 30. Command Timeout

Commands must have a timeout.

Do not allow a command to block the agent indefinitely.

Use:

    context.WithTimeout(...)

with a configurable command timeout.

---

# 31. Agent Runtime

The agent should have a main runtime responsible for coordinating:

    Registration
    Heartbeat loop
    State reporting loop
    Command handling
    Shutdown

Conceptually:

                         Agent
                           │
             ┌─────────────┼─────────────┐
             ▼             ▼             ▼
        Heartbeat      State Loop    Commands
             │             │             │
             └─────────────┼─────────────┘
                           ▼
                         Client

Use goroutines where appropriate.

---

# 32. Concurrency

The heartbeat and state-reporting loops should run independently.

For example:

    Goroutine 1 → heartbeat
    Goroutine 2 → state reporting
    Goroutine 3 → command processing

Do not let a slow state collection block heartbeats.

Do not let a failed heartbeat terminate state reporting.

---

# 33. Goroutine Lifecycle

Every goroutine must respect:

    context.Context

When the agent shuts down:

    cancel context
        ↓
    stop loops
        ↓
    wait for goroutines
        ↓
    exit

Use a synchronization mechanism such as:

    sync.WaitGroup

or an equivalent clean approach.

---

# 34. Graceful Shutdown

Handle:

    SIGINT
    SIGTERM

The agent should:

1. Stop scheduling new work
2. Cancel active operations
3. Wait for loops to exit
4. Close HTTP resources
5. Exit cleanly

Do not abruptly terminate the process.

---

# 35. HTTP Client

Use Go's standard:

    net/http

Do not add Gin, Echo, Fiber, or another server framework.

The agent does not need to expose a large public HTTP API in Phase 2.

The important HTTP communication is:

    Agent → Control Plane

---

# 36. Agent Local API

Optionally expose a very small local endpoint:

    GET /health

or:

    GET /status

If implemented, bind it only to:

    localhost

Do not expose administrative agent APIs publicly.

This is for local debugging only.

---

# 37. Security Boundary

Do not implement complete authentication yet.

However, structure the client so authentication can be added later.

For example:

    Authorization header

should be attachable by the ControlPlaneClient.

Do not hardcode credentials.

Do not commit secrets.

---

# 38. Error Handling

The agent must distinguish:

    Registration failure
    Heartbeat failure
    State collection failure
    State reporting failure
    Command failure
    Invalid response
    Control Plane unavailable

Do not treat every error as fatal.

The general rule should be:

    Temporary network failure
        → retry

    Invalid configuration
        → fail startup

    Corrupt local identity
        → report clearly

    Invalid command
        → reject command

---

# 39. Control Plane Reconnection

The agent must reconnect automatically after Control Plane downtime.

Scenario:

    Control Plane running
          ↓
    Agent connected
          ↓
    Control Plane stops
          ↓
    Heartbeats fail
          ↓
    Agent remains alive
          ↓
    Control Plane starts again
          ↓
    Agent reconnects
          ↓
    Heartbeats resume

This is a mandatory test scenario.

---

# 40. Duplicate Registration Protection

If the agent already has:

    NODE_ID = abc

and the Control Plane knows:

    abc = edge-01

the agent should not create another node.

If the Control Plane reports that the node identity is unknown, handle that explicitly.

Possible behavior:

    identity unknown
        ↓
    re-registration required
        ↓
    obtain new identity
        ↓
    persist it

Document the chosen behavior.

---

# 41. Local State Collector

Implement a state collector that returns something conceptually similar to:

    {
      "status": "READY",
      "hostname": "edge-01",
      "os": "linux",
      "architecture": "amd64",
      "cpu_count": 4,
      "memory_bytes": ...,
      "uptime_seconds": ...
    }

The exact structure should integrate cleanly with the Phase 1 Node/State model.

Do not invent a completely separate state model if Phase 1 already defines one.

---

# 42. Status Determination

The agent should determine its own local status.

For Phase 2, keep this simple.

Possible statuses:

    STARTING
    READY
    DEGRADED
    STOPPING

For example:

    Agent started
        ↓
    STARTING

    Registration successful
        ↓
    READY

    Critical local subsystem unavailable
        ↓
    DEGRADED

    Shutdown
        ↓
    STOPPING

Do not use Kubernetes lifecycle statuses yet.

---

# 43. Why Separate Agent Status From Node Infrastructure Status?

The agent's own health is not necessarily the same as the node's infrastructure state.

For example:

    Agent = READY
    Kubernetes = NOT_INSTALLED

That distinction will become important later.

Do not collapse all system state into one field.

---

# 44. Testing Strategy

Implement thorough tests.

## Unit Tests

Test:

- Configuration loading
- Configuration validation
- Identity persistence
- State collection
- Status determination
- Command validation
- Retry/backoff behavior

---

# 45. Control Plane Client Tests

Use a mock HTTP server.

Test:

    Register()
    Heartbeat()
    GetDesiredState()
    ReportState()

Verify:

- HTTP method
- URL
- JSON body
- Headers
- Response parsing
- Error handling

Do not make tests depend on a real Control Plane.

---

# 46. Agent Runtime Tests

Test that:

- Heartbeat loop starts
- State loop starts
- Context cancellation stops them
- One failing loop does not kill the others
- Shutdown waits for goroutines

Avoid tests that sleep for long periods.

Use injectable clocks/tickers where appropriate.

---

# 47. Integration Test

Create an integration test using a test HTTP server representing the Control Plane.

Scenario:

    1. Start mock Control Plane
    2. Start agent
    3. Agent registers
    4. Agent receives node ID
    5. Agent persists ID
    6. Heartbeat arrives
    7. State report arrives
    8. Desired state is retrieved
    9. Command is sent
    10. Agent executes command
    11. Agent reports command result
    12. Stop Control Plane
    13. Verify agent remains alive
    14. Restart Control Plane
    15. Verify agent reconnects

This is the most important Phase 2 test.

---

# 48. Persistence Test

Test:

    First run
        ↓
    Control Plane assigns ID
        ↓
    Agent stores ID
        ↓
    Agent stops
        ↓
    Agent starts again
        ↓
    Same ID is used

The second startup must not create a duplicate node.

---

# 49. Failure Tests

Test:

- Control Plane unavailable
- Registration failure
- Heartbeat failure
- State reporting failure
- Invalid response
- Malformed command
- Command timeout
- Corrupt identity file
- Context cancellation

The agent should fail predictably.

---

# 50. Race Detection

Run:

    go test -race ./...

Fix any race conditions introduced by the implementation.

The agent contains multiple goroutines, so race testing is particularly important.

---

# 51. Logging

Use Go's standard logging.

Log events such as:

    agent starting
    configuration loaded
    identity loaded
    registration successful
    heartbeat failed
    heartbeat recovered
    state report failed
    command received
    command completed
    control plane unavailable
    control plane recovered
    agent shutting down

Include:

    node_id

where available.

Do not log secrets.

---

# 52. Dependency Philosophy

Keep dependencies minimal.

Prefer:

    Go standard library

for:

- HTTP
- JSON
- Context
- Logging
- Signals
- Concurrency
- File operations

Only introduce external dependencies when there is a clear technical benefit.

---

# 53. Alternatives

Document the following decisions.

## Go

Chosen because the agent is infrastructure software requiring low overhead and concurrency.

## HTTP/JSON

Chosen because the Phase 1 Control Plane already exposes a REST API.

### Not gRPC

gRPC could be useful later for efficient agent communication, but HTTP/JSON keeps Phase 2 simple and easy to debug.

### Not WebSockets

The agent currently needs periodic request/response communication rather than a permanent bidirectional stream.

### Not MQTT

MQTT is useful for IoT-style messaging, but AETHER-GRID is infrastructure/control-plane software and does not currently require a message broker.

---

# 54. No Message Broker

Do not add:

    Kafka
    RabbitMQ
    NATS
    Redis

The agent communicates directly with the Control Plane.

A message broker can be evaluated later if the architecture evolves toward large-scale asynchronous control.

---

# 55. No Persistent Agent Database

Do not add SQLite/PostgreSQL to the agent.

The agent only needs to persist its identity in Phase 2.

Local runtime state can remain in memory.

---

# 56. No Docker Requirement

The agent should be runnable directly as:

    go run ./cmd/aether-agent

or:

    ./aether-agent

Docker support is optional.

If a Dockerfile is added, it must not become a requirement for development.

---

# 57. Linux Focus

AETHER-GRID is primarily an edge infrastructure system.

Target:

    Linux

The implementation may compile on other platforms where practical, but do not compromise the Linux implementation for artificial cross-platform abstraction.

---

# 58. Agent Configuration Example

Provide an example configuration:

    CONTROL_PLANE_URL=http://localhost:8080
    NODE_NAME=edge-01
    NODE_LOCATION=addis-01
    AGENT_DATA_DIR=./data
    HEARTBEAT_INTERVAL=10s
    STATE_REPORT_INTERVAL=30s
    COMMAND_TIMEOUT=30s

Document every variable.

---

# 59. README Requirements

Create/update the agent README with:

## Overview

Explain what the Edge Agent does.

## Architecture

Include:

    Control Plane
          │
          │ HTTP
          ▼
    Edge Agent
          │
          ▼
    Linux Node

## Configuration

Explain environment variables.

## Running

Show:

    go run ./cmd/aether-agent

## Example Logs

Show registration and heartbeat behavior.

## Testing

Show:

    go test ./...

and:

    go test -race ./...

---

# 60. Architecture Decision Record

Document:

| Decision | Choice | Reason |
|---|---|---|
| Language | Go | Infrastructure agent suitability |
| Protocol | HTTP/JSON | Matches Phase 1 API |
| Identity | UUID | Stable distributed identity |
| Identity storage | Local file | Minimal persistent data |
| State | In-memory | Runtime state does not require DB |
| Heartbeats | Periodic | Simple liveness detection |
| Retry | Exponential backoff | Resilient network failures |
| Concurrency | Goroutines + context | Independent agent loops |
| Persistence | Identity only | Avoid unnecessary local DB |
| OS target | Linux | Edge infrastructure focus |

Also explain why alternatives were not chosen.

---

# 61. Definition of Done

Phase 2 is complete only when:

- [ ] Edge Agent is a separate executable
- [ ] Agent configuration works
- [ ] Agent identity is persistent
- [ ] Agent registers with Control Plane
- [ ] Duplicate registration is prevented
- [ ] Agent sends heartbeats
- [ ] Agent reports local state
- [ ] Agent retrieves desired state
- [ ] Agent can receive a command
- [ ] Agent can execute at least one safe command
- [ ] Command results are reported
- [ ] Network failures are retried
- [ ] Exponential backoff works
- [ ] Control Plane reconnection works
- [ ] Agent survives temporary Control Plane outages
- [ ] Agent shuts down gracefully
- [ ] Goroutines terminate correctly
- [ ] Context cancellation works
- [ ] State collector works
- [ ] Configuration validation works
- [ ] Unit tests pass
- [ ] HTTP client tests pass
- [ ] Integration test passes
- [ ] Persistence test passes
- [ ] Failure tests pass
- [ ] Race detector passes
- [ ] gofmt passes
- [ ] go vet passes
- [ ] README is updated
- [ ] Architecture decisions are documented

---

# 62. Required Verification Scenario

Demonstrate this exact workflow.

Start the Control Plane.

Then start:

    aether-agent

The expected sequence is:

    Agent starts
        ↓
    Loads configuration
        ↓
    No local identity
        ↓
    Registers with Control Plane
        ↓
    Receives UUID
        ↓
    Persists UUID
        ↓
    Sends heartbeat
        ↓
    Reports local state
        ↓
    Retrieves desired state
        ↓
    Continues running

Then stop the Control Plane.

Expected:

    Heartbeat fails
        ↓
    Warning logged
        ↓
    Agent remains alive
        ↓
    Retry/backoff

Then restart the Control Plane.

Expected:

    Agent reconnects
        ↓
    Heartbeats succeed
        ↓
    State reports resume

Then stop the agent.

Expected:

    Context cancellation
        ↓
    Loops stop
        ↓
    Resources close
        ↓
    Process exits cleanly

Then start the agent again.

Expected:

    Existing NODE_ID loaded
        ↓
    No duplicate registration
        ↓
    Agent reconnects to existing node

---

# 63. Final Implementation Instructions

Before changing the repository:

1. Inspect the existing Phase 1 implementation.
2. Identify the exact API contracts already implemented.
3. Do not invent incompatible endpoints.
4. Reuse existing Node and State models where appropriate.
5. Preserve existing Control Plane functionality.
6. Add the Edge Agent as a separate executable/component.
7. Do not implement Phase 3.
8. Do not implement Kubernetes, Terraform, WireGuard, or Prometheus.

After implementation:

1. Run gofmt.
2. Run go vet.
3. Run unit tests.
4. Run integration tests.
5. Run race tests.
6. Start the Control Plane.
7. Start the Agent.
8. Verify registration.
9. Verify persistent identity.
10. Verify heartbeats.
11. Verify state reporting.
12. Stop the Control Plane.
13. Verify agent survives.
14. Restart Control Plane.
15. Verify agent reconnects.
16. Restart Agent.
17. Verify the same identity is reused.

Report:

- Files created/modified
- Architecture implemented
- API contracts used
- Configuration options
- Tests performed
- Test results
- Example logs
- Known limitations
- What Phase 3 will build

Do not implement Phase 3.