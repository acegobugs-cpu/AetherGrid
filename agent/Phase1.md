# AETHER-GRID — Phase 1 Implementation

You are implementing **Phase 1 of AETHER-GRID**, an infrastructure automation/control-plane project.

---

# 1. Project Context

AETHER-GRID is a control system for automating the lifecycle of distributed Kubernetes edge nodes.

The long-term architecture will eventually contain:

```text
                         AETHER-GRID
                              │
                    ┌─────────┴─────────┐
                    │    Control Plane  │
                    │                   │
                    │ API               │
                    │ Node Registry     │
                    │ Desired State     │
                    │ Reconciliation    │
                    └─────────┬─────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
         Provisioner       Network       Kubernetes
         Terraform        WireGuard       Operator
              │               │               │
              └───────────────┼───────────────┘
                              ▼
                         Edge Fleet
```

However, **Phase 1 only implements the Control Plane foundation**.

The result should be a working standalone Go service that can:

1. Start
2. Accept node registrations
3. Persist nodes
4. Track node state
5. Receive heartbeats
6. Maintain desired state
7. Compare desired state with actual state
8. Detect differences
9. Expose the state through an HTTP API
10. Survive a restart without losing registered node information
11. Be tested automatically

There is **no real edge node yet**.

There is **no Kubernetes yet**.

There is **no Terraform yet**.

There is **no WireGuard yet**.

There is **no Prometheus yet**.

---

# 2. Phase 1 Scope

Implement these components:

```text
┌─────────────────────────────────────────────┐
│               AETHER-GRID                   │
│                                             │
│  HTTP API                                   │
│      │                                      │
│      ▼                                      │
│  Application Services                       │
│      │                                      │
│      ├── Node Service                       │
│      ├── Heartbeat Service                  │
│      └── State/Reconciliation Service       │
│      │                                      │
│      ▼                                      │
│  Repository Layer                            │
│      │                                      │
│      ▼                                      │
│  SQLite                                     │
│                                             │
└─────────────────────────────────────────────┘
```

Do not implement the future infrastructure components.

---

# 3. Technology Decisions

## 3.1 Language — Go

Use **Go** as the primary language.

### Why

AETHER-GRID is infrastructure/control-plane software.

Go provides:

* Strong concurrency primitives
* Efficient long-running services
* Low runtime overhead
* Excellent Kubernetes ecosystem support
* Good networking libraries
* Simple deployment as a single binary
* Straightforward operational tooling

### Why not Python?

Python is excellent for scripting and automation, but AETHER-GRID is intended to become a continuously running control-plane service with concurrent agents, reconciliation loops, networking, and infrastructure operations.

Go gives us a better fit for this type of long-running systems software.

### Why not C++?

C++ provides excellent performance, but its complexity and memory-management concerns are unnecessary for this project.

AETHER-GRID is not CPU-bound enough to justify that complexity.

### Why not Rust?

Rust provides excellent performance and memory safety, but Go gives us a simpler development model and a particularly strong ecosystem around Kubernetes and cloud-native infrastructure.

Rust remains a technically viable alternative, but it is not the selected implementation language.

---

# 4. HTTP API Decision

Use:

```text
Go standard library net/http
```

Do not use Gin, Echo, Fiber, or another HTTP framework in Phase 1.

## Why?

The Phase 1 API is intentionally small.

The standard library is sufficient for:

* Routing
* HTTP methods
* JSON encoding
* HTTP status codes
* Middleware
* Request handling

Avoid introducing framework-specific abstractions before they provide meaningful value.

The project can adopt a framework later if complexity justifies it.

---

# 5. API Style Decision

Use **REST over HTTP + JSON**.

Do not use GraphQL.

Do not use gRPC yet.

## Why REST?

The Phase 1 API is primarily resource-oriented:

```text
nodes
node/{id}
node/{id}/heartbeat
node/{id}/state
```

REST is simple, observable, easy to test with curl/Postman, and sufficient for the current requirements.

## Why not GraphQL?

The system is not currently a query-heavy client application.

GraphQL would introduce unnecessary schema/resolver complexity.

## Why not gRPC?

gRPC may become useful later for high-frequency agent/control-plane communication.

However, there is no real Edge Agent yet, so introducing gRPC now would solve a future problem prematurely.

---

# 6. Persistence Decision

Use **SQLite** for Phase 1.

Use a repository abstraction so the application is not tightly coupled to SQLite.

Conceptually:

```text
Application
     │
     ▼
NodeRepository
     │
     ▼
SQLite implementation
```

## Why SQLite?

Phase 1 is a local development and learning environment.

SQLite provides:

* Real persistence
* Zero external database server
* Simple local development
* Transaction support
* Easy testing
* Persistence across application restarts

## Why not PostgreSQL?

PostgreSQL is a stronger choice for a production distributed control plane.

However, Phase 1 does not require:

* Multiple database servers
* High concurrent database workloads
* Replication
* Complex distributed persistence

Introducing PostgreSQL now would add infrastructure complexity unrelated to the current learning objective.

The repository abstraction should make a future PostgreSQL implementation possible.

## Important

Do **not** put SQL queries throughout the application.

Database access must remain inside the repository/data-access layer.

---

# 7. Database Schema

Create a database containing at least a `nodes` table.

The node record should contain enough information to represent:

```text
id
name
status
desired_status
location
ip_address
kubernetes_enabled
wireguard_enabled
last_heartbeat
created_at
updated_at
```

Use appropriate SQLite types.

Use UTC timestamps.

The exact schema may be improved if necessary, but do not introduce unnecessary fields.

---

# 8. Node Identity

Each node must have a unique ID.

Use **UUIDs**.

The control plane should generate the UUID when a node is initially registered unless an explicit externally supplied identity is required by the architecture.

For Phase 1:

```text
POST /nodes
        │
        ▼
Control Plane generates UUID
        │
        ▼
Node persisted
```

## Why UUID?

UUIDs provide globally unique identifiers without requiring a centralized integer sequence.

This becomes useful later when nodes are distributed across different edge locations.

## Why not auto-increment integers?

Integer IDs are simple but are less suitable as globally meaningful distributed node identities.

---

# 9. Node Model

Define a clear domain model.

A node should conceptually look like:

```text
Node
├── ID
├── Name
├── Location
├── IP Address
├── Status
├── Desired Status
├── Kubernetes Enabled
├── WireGuard Enabled
├── Last Heartbeat
├── Created At
└── Updated At
```

Keep domain models separate from HTTP request/response DTOs.

Do not expose database models directly from HTTP handlers.

---

# 10. Node Status

Define explicit node statuses.

At minimum:

```text
PROVISIONING
PROVISIONED
CONNECTING
REGISTERED
CONFIGURING
READY
UNHEALTHY
OFFLINE
RECOVERING
```

Use a strongly typed representation in Go rather than arbitrary strings throughout the application.

For example, conceptually:

```go
type NodeStatus string
```

with constants.

Do not allow random status strings to spread throughout the codebase.

---

# 11. Desired State

Phase 1 introduces the desired-state concept, but does not yet perform infrastructure changes.

A node has:

```text
Desired State
```

and:

```text
Actual State
```

Example:

```text
Desired:
READY

Actual:
OFFLINE
```

The control plane should be able to identify this difference.

Do not yet attempt to repair the node.

Actual infrastructure reconciliation will be expanded in Phase 3.

---

# 12. Reconciliation in Phase 1

Implement only the **foundation of reconciliation**.

The reconciliation service should:

1. Retrieve desired state
2. Retrieve actual state
3. Compare them
4. Produce a reconciliation result

For example:

```text
Desired: READY
Actual:  READY

Result:
IN_SYNC
```

or:

```text
Desired: READY
Actual: OFFLINE

Result:
DRIFT_DETECTED
```

Do not implement automatic recovery yet.

The purpose of Phase 1 is to establish the state model that Phase 3 will later turn into a real reconciliation loop.

---

# 13. Heartbeats

Implement a heartbeat endpoint.

Conceptually:

```text
POST /nodes/{id}/heartbeat
```

When a heartbeat is received:

* Update `last_heartbeat`
* Update `updated_at`
* Record that the node is reachable
* Do not blindly overwrite the desired state
* Return the current node state

The heartbeat mechanism should be designed so that the future Edge Agent can call it.

Do not create the Edge Agent in Phase 1.

---

# 14. Node Registration

Implement:

```text
POST /nodes
```

The request should contain the information required to create a node.

For example:

```json
{
  "name": "edge-01",
  "location": "addis-01",
  "ip_address": "10.0.0.10",
  "kubernetes_enabled": true,
  "wireguard_enabled": true
}
```

The server should:

1. Validate input
2. Generate UUID
3. Set initial state
4. Persist the node
5. Return the created node

Do not allow invalid data into the database.

---

# 15. Required API

Implement at least these endpoints:

```text
POST   /nodes
GET    /nodes
GET    /nodes/{id}
DELETE /nodes/{id}

POST   /nodes/{id}/heartbeat

GET    /nodes/{id}/state
GET    /nodes/{id}/desired-state

POST   /nodes/{id}/reconcile
```

The exact URL structure can be adjusted if necessary, but the functionality must exist.

---

# 16. Endpoint Behavior

## POST /nodes

Creates a node.

Expected:

```text
201 Created
```

---

## GET /nodes

Returns all registered nodes.

Expected:

```text
200 OK
```

---

## GET /nodes/{id}

Returns one node.

If it does not exist:

```text
404 Not Found
```

---

## DELETE /nodes/{id}

Deletes a node.

If it does not exist:

```text
404 Not Found
```

---

## POST /nodes/{id}/heartbeat

Updates the node's heartbeat.

If the node does not exist:

```text
404 Not Found
```

---

## GET /nodes/{id}/state

Returns actual state.

---

## GET /nodes/{id}/desired-state

Returns desired state.

---

## POST /nodes/{id}/reconcile

Runs a reconciliation comparison.

For Phase 1 this should **not automatically repair anything**.

It should return something conceptually like:

```json
{
  "node_id": "...",
  "desired_state": "READY",
  "actual_state": "OFFLINE",
  "result": "DRIFT_DETECTED"
}
```

---

# 17. HTTP Error Handling

Implement consistent JSON errors.

For example:

```json
{
  "error": "node not found"
}
```

Use appropriate HTTP status codes:

```text
400 Bad Request
404 Not Found
409 Conflict
500 Internal Server Error
```

Do not return raw database errors directly to clients.

Internal errors should be logged while the API returns a safe error response.

---

# 18. Input Validation

Validate:

* Required fields
* Empty names
* Invalid IP addresses
* Invalid UUIDs
* Invalid status values
* Invalid boolean/configuration values

Do not rely exclusively on database constraints.

Validation should occur at the application boundary.

---

# 19. Architecture / Package Structure

Use a clean but pragmatic Go structure.

A recommended starting point:

```text
aether-grid/
│
├── cmd/
│   └── aether-grid/
│       └── main.go
│
├── internal/
│   ├── domain/
│   │   └── node.go
│   │
│   ├── service/
│   │   ├── node_service.go
│   │   ├── heartbeat_service.go
│   │   └── reconciliation_service.go
│   │
│   ├── repository/
│   │   ├── node_repository.go
│   │   └── sqlite/
│   │       └── node_repository.go
│   │
│   ├── http/
│   │   ├── handlers/
│   │   ├── middleware/
│   │   └── router.go
│   │
│   └── config/
│       └── config.go
│
├── migrations/
│
├── tests/
│
├── go.mod
├── go.sum
├── README.md
└── .gitignore
```

You may improve this structure if there is a strong technical reason.

Do not create excessive packages merely for abstraction's sake.

---

# 20. Architectural Layering

Follow this dependency direction:

```text
HTTP
 ↓
Service
 ↓
Repository
 ↓
Database
```

Domain models should not depend on HTTP or SQLite.

The service layer should contain business logic.

Handlers should primarily:

* Parse requests
* Validate request shape
* Call services
* Convert results to HTTP responses

Do not put business logic inside HTTP handlers.

---

# 21. Repository Interface

Create a repository interface.

Conceptually:

```go
type NodeRepository interface {
    Create(...)
    GetByID(...)
    GetAll(...)
    Delete(...)
    Update(...)
}
```

The exact method signatures are up to the implementation.

The important requirement is that services depend on the interface rather than directly on SQLite.

This will make testing easier and allow a future PostgreSQL implementation.

---

# 22. Service Layer

Create services for:

### Node Service

Responsible for:

* Creating nodes
* Getting nodes
* Listing nodes
* Deleting nodes
* Updating node state

### Heartbeat Service

Responsible for:

* Processing heartbeats
* Updating heartbeat timestamps
* Updating appropriate actual-state information

### Reconciliation Service

Responsible for:

* Loading desired state
* Loading actual state
* Comparing them
* Returning reconciliation results

Keep these responsibilities separate.

---

# 23. Configuration

Configuration must not be hardcoded.

At minimum support:

```text
SERVER_HOST
SERVER_PORT
DATABASE_PATH
```

Provide sensible local defaults.

For example:

```text
HOST=0.0.0.0
PORT=8080
DATABASE_PATH=./data/aether-grid.db
```

Use environment variables rather than hardcoded production configuration.

Do not introduce a complex configuration framework.

---

# 24. Logging

Use Go's standard logging facilities initially.

Log:

* Server startup
* Database initialization
* Node registration
* Heartbeats
* Reconciliation attempts
* Errors
* Shutdown

Do not introduce a large logging framework unless necessary.

Logs should contain enough context to identify the affected node where applicable.

---

# 25. Database Migrations

Do not create the database schema dynamically through scattered application code.

Use migrations.

Create an initial migration such as:

```text
migrations/
└── 001_create_nodes.sql
```

The application should initialize/apply the required migrations when starting.

Keep migrations version-controlled.

---

# 26. Testing Strategy

Testing is a first-class requirement.

Implement:

## Unit tests

Test:

* Node validation
* Node service
* Heartbeat logic
* State comparison
* Reconciliation result generation

Use mocked/in-memory repository implementations where appropriate.

---

## Repository tests

Test the SQLite repository against a temporary database.

Test:

* Create
* Get
* List
* Update
* Delete
* Missing node behavior

---

## HTTP tests

Use Go's HTTP testing utilities.

Test:

```text
POST /nodes
GET /nodes
GET /nodes/{id}
DELETE /nodes/{id}
POST /nodes/{id}/heartbeat
GET /nodes/{id}/state
GET /nodes/{id}/desired-state
POST /nodes/{id}/reconcile
```

Test both success and failure cases.

---

# 27. Critical Integration Test

Implement an end-to-end test covering:

```text
1. Start application
2. Create node
3. Verify node persisted
4. Retrieve node
5. Send heartbeat
6. Verify heartbeat timestamp changed
7. Set/read desired state
8. Compare desired vs actual state
9. Detect drift
10. Restart/reopen database
11. Verify node still exists
12. Delete node
13. Verify 404
```

This test should prove that Phase 1 actually works as a complete system.

---

# 28. Idempotency

Infrastructure systems must be safe to retry.

Ensure operations are designed with idempotency in mind.

For example:

Repeated heartbeats:

```text
heartbeat
heartbeat
heartbeat
heartbeat
```

must not create duplicate nodes.

Repeated state updates should converge on the same state.

Do not create duplicate records as a side effect of repeated operations.

---

# 29. Concurrency

The eventual system will receive heartbeats and state updates concurrently.

Write the repository/service code so concurrent requests do not corrupt state.

Use database transactions where appropriate.

Do not add unnecessary global locks.

Prefer the database and proper application-level synchronization where required.

---

# 30. What NOT to Implement

Strictly do NOT implement these yet:

### Kubernetes

No Kubernetes client.

No Kubernetes Operator.

No CRDs.

No cluster provisioning.

### Terraform

No Terraform execution.

No cloud provisioning.

### WireGuard

No WireGuard configuration.

No VPN management.

### Prometheus

No Prometheus integration yet.

### Edge Agent

Do not implement the actual agent.

Phase 2 will build the agent.

### Automatic Recovery

Do not automatically repair nodes.

Phase 3 will implement real reconciliation actions.

### Authentication

Do not build a complete authentication system yet.

The API can initially be local/trusted development infrastructure.

Security hardening comes later.

---

# 31. Dependency Philosophy

Keep dependencies minimal.

Prefer Go's standard library when it is sufficient.

Every external dependency must have a clear reason.

Before adding a dependency, ask:

> "Does this dependency solve a meaningful problem that the standard library or a small amount of code cannot reasonably solve?"

Do not add frameworks merely because they are popular.

---

# 32. Code Quality Requirements

The implementation should:

* Follow idiomatic Go
* Use clear naming
* Handle errors explicitly
* Avoid unnecessary abstractions
* Keep functions reasonably small
* Keep business logic testable
* Avoid global mutable state
* Use contexts where appropriate
* Close resources correctly
* Avoid leaking database connections
* Document non-obvious design decisions

Run:

```bash
go fmt ./...
go vet ./...
go test ./...
```

The project must pass all three.

If available in the environment, also run:

```bash
go test -race ./...
```

and fix race conditions that are introduced by the implementation.

---

# 33. README Requirements

Create/update the README with:

## Project Overview

Explain AETHER-GRID in simple terms.

## Phase 1 Scope

Explain what currently exists.

## Architecture

Include a diagram.

## Running Locally

Show:

```bash
go run ./cmd/aether-grid
```

and any database initialization requirements.

## API Examples

Provide curl examples for:

* Creating a node
* Listing nodes
* Getting a node
* Sending a heartbeat
* Checking state
* Running reconciliation

## Testing

Show:

```bash
go test ./...
```

---

# 34. Design Decisions That Must Be Documented

Create a short architecture/design document explaining these decisions:

| Decision       | Selected                | Why                                                    |
| -------------- | ----------------------- | ------------------------------------------------------ |
| Language       | Go                      | Infrastructure/control-plane suitability               |
| HTTP           | `net/http`              | Small API; no framework required                       |
| API style      | REST/JSON               | Simple resource-oriented API                           |
| Database       | SQLite                  | Persistent local state without external infrastructure |
| DB abstraction | Repository interface    | Decouple business logic from storage                   |
| IDs            | UUID                    | Distributed-friendly identity                          |
| Architecture   | Layered                 | Separate transport/business/data concerns              |
| State model    | Desired vs actual       | Foundation for reconciliation                          |
| Logging        | Standard library        | Minimal dependencies                                   |
| Configuration  | Environment variables   | Simple deployment model                                |
| Migrations     | Versioned SQL           | Reproducible schema                                    |
| Testing        | Go testing + HTTP tests | Native ecosystem support                               |

Also document why the major alternatives were not selected.

---

# 35. Expected Result

At the end of this task, I should have a repository that I can clone and run locally.

Running:

```bash
go run ./cmd/aether-grid
```

should start the AETHER-GRID control plane.

I should then be able to do something like:

```bash
curl -X POST http://localhost:8080/nodes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "edge-01",
    "location": "addis-01",
    "ip_address": "10.0.0.10",
    "kubernetes_enabled": true,
    "wireguard_enabled": true
  }'
```

Then:

```bash
curl http://localhost:8080/nodes
```

should show the registered node.

I should be able to send:

```bash
curl -X POST http://localhost:8080/nodes/{id}/heartbeat
```

and see the node's heartbeat information update.

I should be able to query:

```bash
curl http://localhost:8080/nodes/{id}/state
```

and:

```bash
curl http://localhost:8080/nodes/{id}/desired-state
```

Then:

```bash
curl -X POST http://localhost:8080/nodes/{id}/reconcile
```

should tell me whether the desired and actual states are synchronized or different.

Finally, if I stop the application and start it again, the registered node should still exist because its state is persisted in SQLite.

---

# 36. Definition of Done

Phase 1 is complete only when:

* [ ] Go project builds successfully
* [ ] Control plane starts successfully
* [ ] SQLite database initializes
* [ ] Migrations execute correctly
* [ ] Nodes can be created
* [ ] Nodes can be retrieved
* [ ] Nodes can be listed
* [ ] Nodes can be deleted
* [ ] Heartbeats work
* [ ] Desired state exists
* [ ] Actual state exists
* [ ] Desired/actual state comparison works
* [ ] Drift can be detected
* [ ] API returns appropriate HTTP status codes
* [ ] Errors are returned consistently
* [ ] Input validation works
* [ ] Repository is abstracted behind an interface
* [ ] Unit tests pass
* [ ] Repository tests pass
* [ ] HTTP tests pass
* [ ] Integration test passes
* [ ] `go fmt ./...` passes
* [ ] `go vet ./...` passes
* [ ] `go test ./...` passes
* [ ] Race tests pass if applicable
* [ ] README is complete
* [ ] Architecture decisions are documented

---

# 37. Important Implementation Instruction

Do not merely generate code that satisfies the endpoint list.

Think about the **future architecture**.

Phase 1 is the foundation for:

```text
Phase 1
Control Plane
     ↓
Phase 2
Edge Agent
     ↓
Phase 3
Reconciliation
     ↓
Phase 4
Kubernetes
     ↓
Phase 5
Operator
     ↓
Phase 6
Terraform
     ↓
Phase 7
WireGuard
     ↓
Phase 8
Observability
```

The Phase 1 implementation should therefore establish clean boundaries that future components can integrate with without rewriting the core system.

At the same time, **do not over-engineer for hypothetical future requirements**.

Build the smallest clean implementation that satisfies this specification.

---

# 38. Final Instruction

Before modifying the repository:

1. Inspect the existing repository structure.
2. Determine whether a Go project already exists.
3. Preserve useful existing work if present.
4. Do not overwrite unrelated files.
5. Identify any conflicts between the existing code and this specification.
6. Resolve straightforward conflicts in favor of this specification.
7. If a conflict would require a major architectural decision not covered here, stop and explain it before making destructive changes.

Then implement Phase 1 completely.

After implementation:

1. Run all tests.
2. Run formatting.
3. Run static analysis.
4. Run the application locally if possible.
5. Exercise the main API flow.
6. Fix any issues found.
7. Report exactly what was implemented.
8. Report the final project structure.
9. Report the commands used to verify the implementation.
10. Report any remaining limitations.

**Do not start Phase 2.**
