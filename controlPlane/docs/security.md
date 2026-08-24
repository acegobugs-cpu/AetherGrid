# AETHER-GRID Security Model

Phase 10 documentation: threat model, trust boundaries, authentication and
authorization design, credential lifecycle, transport security, secret
handling, audit logging, incident response and known limitations.

---

## 1. Threat Model

### Assets

| Asset | Sensitivity | Where it lives |
| --- | --- | --- |
| Control-plane API | Critical | `controlPlane` HTTP server |
| Node agent credentials (`agr_...`) | Secret (per node) | Issued by control plane; stored hashed in SQLite, plaintext only on the edge node (`node-credential`, mode 0600) |
| Bootstrap credentials | Secret, single-use, short-lived | Same storage; consumed on registration |
| Human API keys | Secret (role-scoped) | Environment config only (`AUTH_STATIC_KEYS`); never in source or DB |
| k3s join token | Secret | In memory + staged transiently on workers under `/run` (0600, removed after join) |
| Kubeconfigs | Secret | Synthesized on demand; validate the API server against the cluster CA; never persisted by the control plane |
| Terraform provider credentials | Critical | Process environment of the Terraform subprocess; never logged, returned via APIs, or injected into agents |
| WireGuard private keys | Secret | Generated per node; delivered to `wg setconf` via stdin; never transmitted through ordinary APIs |
| Desired state / infrastructure state | Internal | SQLite; manipulable only through authorized APIs |
| Audit log | Compliance-critical | Append-only `audit_events` table + structured stdout lines |

### Trust boundaries

```text
Internet / External Client
        │  TLS (production) + Bearer credential
        ▼
Control Plane API   ← authentication, authorization, validation,
        │             rate limiting, body limits, audit
        ├── Node Agents      trust boundary: per-node credential,
        │                    self-scope enforcement (identity == target)
        ├── Kubernetes       trust boundary: least-privilege operator RBAC
        └── Terraform        trust boundary: credentials via process env,
                             domain operations only (no arbitrary commands)
```

Explicit assumptions:

* The network between components is **untrusted**; no component is trusted
  merely for being on the same network segment.
* An edge node can be compromised. Its credential therefore grants only
  that node's own scope and can be revoked instantly.
* The control-plane host is the highest-value target; secrets there are kept
  out of the database and logs.

### Threat categories addressed

* Unauthorized API access → authentication required on every route
* Agent/node impersonation → random 256-bit bearer credentials; identity is
  derived from the credential, never from request bodies or hostnames
* Credential theft → hashed-at-rest storage, TTLs, rotation, revocation
* Replay attacks → single-use bootstrap tokens with expiry; short-lived
  credentials bound to one node
* Privilege escalation → role hierarchy (admin > operator > viewer) plus
  agent self-scoping; no wildcard RBAC for the Kubernetes operator
* Malicious registration → anonymous self-registration refused outside an
  explicitly configured development deployment; production requires a
  bootstrap credential issued during provisioning
* Fake health events driving recovery → health/state reports are accepted
  only from the authenticated owner node; recovery itself runs exclusively
  inside the reconciliation engine from trusted observations
* Command injection → names/IDs validated against conservative charsets;
  Terraform invoked as structured argument arrays (never a shell); k3s join
  commands built from pinned constants
* SQL injection → parameterized queries throughout all repositories
* DoS / resource exhaustion → fixed-window rate limits on sensitive routes,
  1 MiB body cap, server read/write/idle timeouts
* Secret leakage → redaction discipline in handlers, hashes instead of
  tokens at rest, no CORS, machine-only headers

---

## 2. Architectural Decisions

| Decision | Choice | Reason | Alternatives rejected because |
| --- | --- | --- | --- |
| Machine authentication | Per-node opaque bearer credentials (SHA-256 hashed at rest) | Fits the existing HTTP architecture; simple lifecycle; instant revocation | mTLS: strong but heavy certificate lifecycle management across an edge fleet; deferred until a PKI exists. JWT: adds key management without removing the revocation problem. OAuth2/OIDC: designed for human federation, excessive here. |
| Human authentication | Static API keys mapped to roles via env config | Operators already deploy via config; zero new infrastructure | OIDC: no identity provider exists in the target environment today |
| Transport security | HTTPS enforced in production (`AETHERGRID_ENV=production` refuses to start otherwise); loopback HTTP allowed in development | Secure defaults without breaking local development; explicit dev overrides | WireGuard-only trust: conflates network membership with application identity |
| Node identity | Cryptographic credential bound to one node ID | Prevents spoofing; possession of a name grants nothing | Hostname/IP/node-name identity: trivially forged |
| Authorization | Role hierarchy + per-route policy table + agent self-scope | Explicit, auditable, matches existing API surface | Policy engine (OPA): unnecessary complexity at this scale |
| Secret management | Env-based injection for humans/Terraform; hashed storage for issued credentials | Clean abstraction; external managers can be added later behind the same seams | Vault et al.: heavyweight for current needs |
| WireGuard keys | Private keys generated per node and applied over stdin; public key registered with control plane | Private key never crosses argv/logs | CP-generated private keys shipped to nodes: expands exposure |
| Recovery authorization | Manual reconcile/reset require operator+; automatic recovery consumes only internally observed, authenticated state | Protects Phase 9 autonomy from manipulation | Public trigger endpoints: enables forced-provisioning attacks |
| Credential rotation | Agent-initiated endpoint + admin override; old generation revoked atomically | Safe, idempotent, non-disruptive | Automatic background rotation: deferred; foundation shipped |
| Audit logging | Structured stdout + append-only SQLite table | Queryable, tamper-evident enough for this scale | Full SIEM: out of scope |

---

## 3. Authentication

Every request must present `Authorization: Bearer <credential>`. Two
principal families exist:

* **Human principals** — static API keys from `AUTH_STATIC_KEYS`
  (`token:role,...`). Roles: `admin`, `operator`, `viewer`. Keys are compared
  via SHA-256 digests with constant-time comparison.
* **Machine principals** — credentials issued by the control plane:
  * *bootstrap*: single-use, short-lived (default 15 min), bound to one node,
    valid only on `POST /nodes/{id}/register`.
  * *agent*: long-lived (default 90 days), scoped to its node.

Unknown, malformed, expired, used and revoked tokens all fail closed with
401 and produce an `AuthenticationFailed` audit event. Token values never
appear in logs; only SHA-256 hashes are persisted.

## 4. Secure Registration & Credential Lifecycle

```text
Unregistered
    │  operator provisions: POST /nodes  (admin; anon allowed only when
    │                                      AUTH_OPEN_REGISTRATION=true in dev)
    ▼
Bootstrap credential issued (single-use, ≤15 min, shown once)
    │  POST /nodes/{id}/register  with bootstrap bearer
    ▼
Registered — agent credential issued (shown once), bootstrap consumed
    │
    ├─ Rotate: POST /nodes/{id}/credentials/rotate (agent-self or admin)
    │          old generation revoked immediately
    ├─ Revoke: DELETE /nodes/{id}/credentials (admin) — kill switch
    └─ Delete: DELETE /nodes/{id} also revokes everything
```

Agents fail closed: a rejected credential stops the agent rather than
silently re-registering. Identity churn requires fresh provisioning.

## 5. Authorization Matrix

| Operation | Viewer | Operator | Admin | Agent |
| --- | :-: | :-: | :-: | :-: |
| View nodes/clusters/reconciliation | ✓ | ✓ | ✓ | own node |
| Update heartbeat / report state | – | – | – | own node only |
| Read desired state / commands | ✓ | ✓ | ✓ | own node |
| Dispatch commands, PUT desired-state | – | ✓ | ✓ | – |
| Trigger reconcile / recovery reset | – | ✓ | ✓ | – |
| Infrastructure plan/apply/bootstrap | – | ✓ | ✓ | – |
| Create infrastructure, destroy anything, delete nodes | – | – | ✓ | – |
| Provision node record + bootstrap token | – | – | ✓ | – |
| Revoke any node's credentials | – | – | ✓ | – |
| Rotate own credential | – | – | – | ✓ |

Enforcement lives in the router policy table (`controlPlane/internal/http/router.go`)
and the middleware package. New endpoints default to deny-everything.

## 6. Transport Security & TLS

* Production (`AETHERGRID_ENV=production`) refuses to start unless
  `TLS_CERT_FILE` and `TLS_KEY_FILE` are present, readable, and paired.
* HSTS is emitted only when TLS is active; `X-Content-Type-Options`,
  `Referrer-Policy: no-referrer` and `Cache-Control: no-store` always apply.
  No CORS surface exists: the API is machine-only.
* Certificate lifecycle (issuance/expiry/renewal/revocation) is delegated to
  the deployment environment (e.g. cert-manager); startup validates file
  presence only. Rotation = replace files + restart.
* Agents refuse plaintext HTTP to non-loopback addresses unless
  `AGENT_ALLOW_INSECURE_TRANSPORT=true` (development only).

## 7. Kubernetes, Terraform and WireGuard Hardening

* **Operator**: ClusterRole enumerates exactly the resources it manages
  (AetherClusters and their Deployments). The previously unused `events`
  permission was removed. Runtime container: distroless non-root,
  `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`,
  all capabilities dropped, seccomp `RuntimeDefault`.
* **Terraform**: invoked via argument arrays without a shell; inherits a
  curated environment; state files live under a 0700 working directory and
  are gitignored; plan/apply errors surfaced to clients are truncated and
  contain no credential material.
* **k3s bootstrap**: join token delivered over SSH stdin into `/run`
  (0600) and exported inside the remote root shell — it never appears in
  process listings; kubeconfigs now pin the cluster CA
  (`CertificateAuthorityData`) instead of skipping TLS verification.
* **WireGuard**: private keys applied via `wg setconf` reading stdin, so
  they are absent from argv; peer removal is part of node teardown.

## 8. Input Validation, Rate Limiting, Resource Limits

* Node IDs must be UUIDs; names accept `[A-Za-z0-9._-]{1,63}`; IPs parse as
  IPs; JSON decoding rejects unknown fields; bodies larger than 1 MiB are
  refused with 413.
* Fixed-window rate limits: registration-class routes 30/min and mutating
  routes 120/min per identity/IP by default (tunable via
  `REGISTER_RATE_PER_MINUTE` / `MUTATION_RATE_PER_MINUTE`). Anonymous abuse
  of open-registration development mode is capped aggressively.
* Server enforces read/write/idle timeouts; the reconciliation engine's
  Phase 9 concurrency limits continue to bound recovery work.

## 9. Audit Logging

Audit answers *"who caused or authorized this action?"* and is deliberately
separate from application logs. Events include timestamp, actor
(`role:admin`, `agent:<uuid>`, `bootstrap:<uuid>`), actor type, operation,
resource, result, request ID, source address and reason. Recorded operations
include `AuthenticationFailed`, `AuthorizationDenied`, `NodeRegistered`,
`CredentialIssued`, `CredentialRotated`, `CredentialRevoked`, `NodeDeleted`,
`ReconciliationTriggered`, `InfrastructureProvisioned`,
`InfrastructureDestroyed`. Events are emitted as structured stdout lines and
appended to the `audit_events` table. Secrets are never recorded.

## 10. Security Incident Procedure (Compromised Node)

```text
 1. Identify compromised node            (audit trail, heartbeats)
 2. DELETE /nodes/{id}/credentials       (admin; revokes all credentials)
 3. Remove WireGuard peer                (network membership revoked)
 4. Remove the node from the cluster     (k3s removal path / operator)
 5. DELETE /nodes/{id}                   (record gone; cannot re-register —
                                          provisioning is required to return)
 6. Rebuild/re-provision replacement infrastructure
 7. Rotate affected human keys / cluster join material if suspected
 8. Review audit_events for actions performed by the stolen credential
 9. Verify cluster state and reconciliation results
10. Restore desired state; monitor for re-registration attempts
```

## 11. Dependency & Static Analysis

Baseline tooling: `go mod tidy`, `go vet ./...`, `go test ./...`,
`go test -race ./...`. Run `govulncheck ./...` (golang.org/x/vuln) per
release to catch known CVEs in dependencies. Container images should be
scanned with Trivy or Grype in CI when image publishing is automated.

## 12. Known Limitations & Remaining Work

* mTLS is not implemented; bearer credentials over TLS are the current
  trust anchor. Introduce a PKI before inter-component mTLS.
* Rate limiting is in-memory per instance; a shared limiter is needed if the
  control plane ever scales horizontally.
* Automatic credential rotation is not implemented; agents must call the
  rotate endpoint (or admins rotate for them) before the 90-day TTL.
* The audit store is local SQLite; ship it to remote log storage for
  tamper resistance in real deployments.
* Open registration exists solely as an explicit development aid and is
  validated off in production.
