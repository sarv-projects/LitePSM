# Runtime, Invocation Engine & Receipts

Status: **`DESIGNED`.** This document specifies the capability registry, the real invocation handlers, the invocation engine (deadlines, cancellation, concurrency, backpressure, restart, schema-drift, redaction), provider health and circuit breaking, bounded output, tamper-evident receipts, and cross-provider fallback rules. **No invocation registry, no capability registry, and no receipt store exist** ([STATUS.md](../STATUS.md) §4). Do not present the engine below as implemented.

**Reality today:** `internal/provider` start/stop/probe is `WIRED`, and `internal/discover` now writes real `providers` and `capabilities` rows and invokes a tool synchronously — so `provider.invoke` / Bridge `invoke_capability` **resolve**. `invocation.get` and `invocation.cancel` (and Bridge `get_invocation` / `cancel_invocation`) remain `IMPLEMENTED` but return JSON-RPC `-32601` with an explicit reason: no asynchronous invocation registry exists ([STATUS.md](../STATUS.md) §4). Do not present any of the engine below as implemented.

---

## 1. Capability Registry

A single registry maps a `(capabilityId, schemaFingerprint)` to a runnable provider instance. It is the missing bridge between "a tool the model wants" and "a process that can run it".

```go
type CapabilityRecord struct {
    CapabilityID      string  // e.g. mcp:…:postgres/server/query
    ListingID         string
    InstallID         string
    Kind              string  // tool | mcp-provider | …
    Transport         string  // stdio | streamable-http | sse
    InputSchemaJSON   string
    SchemaFingerprint string  // SHA-256 of canonicalised inputSchema
    ProviderInstance  string  // supervisor handle for the running provider
    Status            string  // see §4
}
```

Invariants:

* A capability is invocable only when a **live** provider instance backs it and the runtime tuple matches an active `CapabilityGrant`: `(capabilityId, schemaFingerprint, casTreeDigest)` for local stdio, or `(capabilityId, schemaFingerprint, endpointOrigin, serverVersionDigest)` for remote ([14 §4.1](14-BRIDGE-PROVIDER-MCP.md), [15](15-POLICY-APPROVALS.md)).
* The registry is populated from the **install path**, not by tests. Populating `providers`/`capabilities` on install is the concrete next step (`m4`, `STATUS.md` §4).
* `search_capabilities` and `describe_capability` read the registry; they must not fabricate rows when it is empty.

---

## 2. Invocation API

Daemon IPC methods and their Bridge tool equivalents:

| IPC method | Bridge tool | Contract |
|---|---|---|
| `provider.invoke` | `invoke_capability` | Submit a capability call; returns an `invocationId` and initial state. |
| `invocation.get` | `get_invocation` | Fetch state/result/receipt for an `invocationId`. |
| `invocation.cancel` | `cancel_invocation` | Request cooperative cancellation by `invocationId`. |
| `capabilities.search` | `search_capabilities` | Search the registry. |
| `capabilities.describe` | `describe_capability` | Describe one capability incl. schema fingerprint. |

```go
type InvokeParams struct {
    CapabilityID      string          `json:"capabilityId"`
    Arguments         json.RawMessage `json:"arguments"`
    DeadlineMs        int             `json:"deadlineMs,omitempty"`   // bounded; server clamps
    IdempotencyKey    string          `json:"idempotencyKey,omitempty"`
}

type InvocationState struct {
    InvocationID string          `json:"invocationId"`   // invocation_<26>
    State        string          `json:"state"`          // queued|running|succeeded|failed|cancelled|deadline_exceeded
    Result       json.RawMessage `json:"result,omitempty"`
    Error        *ErrorEnvelope  `json:"error,omitempty"`
    Receipt      *InvocationReceipt `json:"receipt,omitempty"`
}
```

Rules:

* Every handler validates the capability, the grant, and the schema fingerprint **before** dispatch; a `-32601` remains correct only for an unimplemented method, never for a missing registry row.
* `arguments` are validated against `InputSchemaJSON` before the provider sees them.
* Invocation IDs use the same Crockford base32 scheme as plan IDs ([23 §2](23-SCHEMAS-EXAMPLES.md)).

---

## 3. Invocation Engine

```text
 submit ──▶ validate(capability, grant, schema) ──▶ queue ──▶ dispatch ──▶ provider
                                                            │              │
                                   deadline/cancel ◀────────┘              │
                                                                           ▼
                        receipt + bounded result ◀── redact/normalize ◀── raw output
```

Required behaviours:

* **Deadline.** Every invocation has a bounded deadline (client-supplied, server-clamped to a maximum). Expiry cancels the call and yields `deadline_exceeded` with a receipt.
* **Cancellation.** `invocation.cancel` propagates through the Go context to the provider child process (kill on timeout), and is idempotent.
* **Concurrency.** A per-provider concurrency limit bounds in-flight calls; excess calls queue with backpressure rather than pile up unbounded.
* **Backpressure.** When the queue is full, `provider.invoke` fails fast with a typed `LPSM-PROVIDER-BUSY` rather than growing memory or blocking the daemon.
* **Restart.** A crashed provider is restarted under the supervisor; in-flight invocations fail with a typed error and are **not** transparently retried across a restart unless the call is declared idempotent by the caller (`idempotencyKey`). See §7.
* **Schema drift.** If the provider's live `inputSchema` fingerprint no longer matches the grant, the invocation is denied with `LPSM-PROVIDER-SCHEMA-DRIFT` before dispatch (reusing the existing grant-check logic in [15 §4](15-POLICY-APPROVALS.md)).
* **Redaction.** Provider stdout/stderr and result payloads pass a redaction pass (known secret patterns, connection strings, tokens) before persisting or returning. Redaction failures fail closed.

---

## 4. Provider Health & Circuit Breaker

Health states (superset of the current probe output):

```text
STARTING ──▶ READY ──▶ DEGRADED ──▶ UNHEALTHY ──▶ BACKOFF ──▶ STOPPED
    │           ▲           │             │            │
    └───────────┴───────────┴─────────────┴────────────┘
                  (recovery / successful probe)
```

| State | Meaning |
|---|---|
| `STARTING` | Process launched, handshake not complete. |
| `READY` | Handshake complete; probes healthy. |
| `DEGRADED` | Some probes failing or latency above threshold; calls still attempted. |
| `UNHEALTHY` | Consecutive failures exceed threshold; calls refused. |
| `BACKOFF` | Restart delayed by exponential backoff. |
| `STOPPED` | Intentionally stopped; not invocable. |

The circuit breaker trips on consecutive failures/timeouts into `UNHEALTHY`, then `BACKOFF`, and closes again only after a successful probe. Health is derived from observed probe results — never fabricated ([AGENTS.md](../AGENTS.md) Tab 4). `doctor` reports the observed state, not a claim.

---

## 5. Bounded Output

Provider output is untrusted and potentially unbounded.

* stdout/stderr are captured into a **bounded ring buffer**; overflow truncates with an explicit marker and a byte count, never a silent drop.
* **Provider stderr is never piped into model context.** It may inform error envelopes and the receipt, after redaction.
* Output is rate-limited, ANSI-stripped, binary-detected, and UTF-8-validated before any consumer sees it.
* Result size is bounded; an oversized result is truncated with `truncated: true` and the original digest, rather than returned whole.

These rules are the missing half of the existing bounded-buffer work ([31 §9](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md)).

---

## 6. Tamper-Evident Invocation Receipts

Every completed invocation produces a receipt bound to the authorization and the runtime state.

```go
type InvocationReceipt struct {
    InvocationID      string    `json:"invocationId"`
    CapabilityID      string    `json:"capabilityId"`
    PackageDigest     string    `json:"packageDigest"`      // CAS tree digest
    SchemaFingerprint string    `json:"schemaFingerprint"`
    ProviderInstance  string    `json:"providerInstance"`
    PolicyDecisionID  string    `json:"policyDecisionId"`
    ApprovalID        string    `json:"approvalId,omitempty"`
    Outcome           string    `json:"outcome"`            // succeeded|failed|cancelled|deadline_exceeded
    StartedAt         time.Time `json:"startedAt"`
    EndedAt           time.Time `json:"endedAt"`
    InputDigest       string    `json:"inputDigest"`        // digest of canonicalised arguments
    OutputDigest      string    `json:"outputDigest"`
    PrevReceiptHash   string    `json:"prevReceiptHash"`    // hash-chain link
    ReceiptHash       string    `json:"receiptHash"`
}
```

Tamper-evidence:

* `ReceiptHash` is a digest over the canonical-JSON projection of all fields except itself.
* `PrevReceiptHash` chains each receipt to the previous one for the same provider instance, so deletion/reordering is detectable.
* Receipts are appended to the audit log ([20](20-ERRORS-AUDIT-DOCTOR.md)); outputs are represented by digest, not necessarily retained.
* `audit --ci` ([36](36-ENTERPRISE-POLICY-AND-AUDIT.md)) verifies the chain.

---

## 7. Cross-Provider Fallback

Governed by the binding decision in [15 §5](15-POLICY-APPROVALS.md):

* **Same-provider restart/retry is allowed** within the deadline and circuit-breaker state; a retry consumes the same grant and policy decision.
* **Blind cross-provider semantic fallback is forbidden for writes.** No automatic substitution among providers for any effectful capability.
* **Read-only equivalence declarations only.** A cross-provider substitute requires a read-only capability plus an explicit, individually approvable equivalence declaration asserting equivalent schemas and semantics. Similarity of names/descriptions is never sufficient.

---

## 8. Acceptance Test Sketch

1. Installing a capability populates `providers` and `capabilities` from non-test code; `search_capabilities` returns it and `describe_capability` returns its fingerprint.
2. `invoke_capability` against a fake MCP server returns a result and a receipt whose `ReceiptHash` verifies; `get_invocation` returns the same receipt.
3. A call exceeding its deadline returns `deadline_exceeded`; `cancel_invocation` stops an in-flight call and is idempotent.
4. A schema change at the provider blocks invocation with `LPSM-PROVIDER-SCHEMA-DRIFT` before dispatch.
5. Provider stderr is absent from result/model context and appears only, redacted, in the error/receipt.
6. Circuit breaker trips after N consecutive failures and closes after a successful probe.
7. A write call that fails is **not** retried against a different provider; a read-only call with a declaration may be.

---

## 9. Related Documents

* Provider supervisor, launch specs, drift binding: [14 — Bridge, Provider Supervisor & MCP](14-BRIDGE-PROVIDER-MCP.md).
* Policy, grants, fallback decision: [15 — Policy & Approvals](15-POLICY-APPROVALS.md).
* IPC transport and `$/cancelRequest`: [11 — Local Runtime & IPC](11-LOCAL-RUNTIME-IPC.md).
* Receipts in the audit log: [20 — Errors, Audit & Doctor](20-ERRORS-AUDIT-DOCTOR.md).
* Audit replay and chain verification: [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md).
* Runtime adapters and materialisation: [17 §5](17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md).
