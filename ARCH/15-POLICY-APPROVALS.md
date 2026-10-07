# Policy Engine & Approvals Architecture

## 1. Canonical Effect Taxonomy

All actions proposed by installation plans or performed by downstream capabilities are classified under a 17-action normalized taxonomy:

```text
┌─────────────────────┬──────────────────────────────────────────────────────────────────┐
│ Canonical Effect    │ Operational Description & Risk Profile                           │
├─────────────────────┼──────────────────────────────────────────────────────────────────┤
│ filesystem.read     │ Reads files or directory structures within user paths.           │
│ filesystem.write    │ Creates or modifies files within local project/user scopes.      │
│ filesystem.delete   │ Deletes files or directories on the local machine.               │
│ process.spawn       │ Spawns a local binary, runtime, or child process.                │
│ process.signal      │ Sends OS signals (SIGTERM, SIGKILL) to local processes.          │
│ network.outbound    │ Establishes outbound TCP/HTTP connections to external endpoints. │
│ external.read       │ Reads data from an external SaaS or remote API.                  │
│ external.write      │ Modifies or creates data in an external SaaS service.            │
│ external.delete     │ Deletes resources in an external SaaS service.                   │
│ credential.read     │ Requests access to an OS secret-store handle or OAuth token.     │
│ credential.write    │ Stores new credentials or updates an existing auth profile.      │
│ system.config.write │ Modifies system-level configuration files or registry keys.      │
│ host.config.write   │ Modifies agent host configuration (e.g., ~/.claude.json).        │
│ hook.execute        │ Registers or executes lifecycle event hooks.                     │
│ package.install     │ Unpacks software files into LiteSPM CAS store.                   │
│ provider.start      │ Initializes a local or remote MCP provider session.              │
│ provider.stop       │ Terminates an active MCP provider session.                       │
└─────────────────────┴──────────────────────────────────────────────────────────────────┘
```

### 1.1 Effect Provenance & Unknown Tools
Effect declarations carry strict provenance tags to distinguish verified safety from unverified publisher claims:
*   `publisher_declared`: Unverified metadata extracted from upstream manifests.
*   `curated`: Inspected and verified by catalog maintainers or static analysis pipelines.
*   `runtime_observed`: Dynamically observed during sandboxed test execution.
*   `user_classified`: Explicitly tagged by the local administrator in policy configurations.

**Unknown Tools:** The policy engine **never guesses** side effects from tool parameter names or schemas. Uncategorized tools are treated as atomic capability grants that require explicit user approval (Tier 4) or default denial (Tier 5).

---

## 2. Policy Engine Evaluation Contract

The Policy Engine (`internal/policy`) evaluates every proposed action against local security rules:

```go
type PolicyInput struct {
    Actor               string                     `json:"actor"`               // user | agent | system
    HostID              string                     `json:"hostId"`              // e.g. claude-code, codex
    Operation           string                     `json:"operation"`           // install | update | invoke | start
    TargetRef           string                     `json:"targetRef"`           // ListingId or CapabilityId
    CapabilityID        string                     `json:"capabilityId,omitempty"`
    Effects             []EffectDeclaration        `json:"effects"`             // List of canonical effects with provenance
    RequestedAccess     []string                   `json:"requestedAccess"`
    SchemaFingerprint   string                     `json:"schemaFingerprint,omitempty"`
    CASTreeDigest       string                     `json:"casTreeDigest,omitempty"`    // Verified CAS tree digest (local)
    EndpointOrigin      string                     `json:"endpointOrigin,omitempty"`   // Verified HTTPS origin (remote)
    ServerVersionDigest string                     `json:"serverVersionDigest,omitempty"`
    Scope               domain.InstallScope        `json:"scope"`               // user | project
    WorkspaceID         string                     `json:"workspaceId,omitempty"`      // Canonical workspace ID (project scope)
    ProjectRoot         string                     `json:"projectRoot,omitempty"`      // Absolute path to project root
}

type PolicyDecision struct {
    Decision            DecisionKind          `json:"decision"`            // allow | deny | ask
    ReasonCodes         []string              `json:"reasonCodes"`
    MatchedRuleIDs      []string              `json:"matchedRuleIds"`
    RequiredChannel     domain.ApprovalChannel `json:"requiredChannel"`  // interactive_cli | agent_bridge | ci_policy | preapproved_rule
    Detail              string                `json:"detail,omitempty"`
}
```

### 2.1 Decision Precedence (current engine reality)

`internal/policy/engine.go` exposes a **single flat function**, `Evaluate(ctx, PolicyInput) PolicyDecision`. It is *not* a layered policy hierarchy: it receives an injected `[]DenyRule` and today has no admin/org/team/repo/user inheritance and no per-rule provenance. The numbered "tiers" below name the **fixed stage order inside that one function**; the first stage that decides returns and terminates evaluation. The tighten-only layered hierarchy and the `PolicyDecision` provenance that `policy explain` needs are `DESIGNED` in [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md) and do not exist yet.

Stage order as implemented:
1.  **Tier 1 (Hard Invariant Deny):** Cannot be overridden. (e.g., command marketplace sources, writing secrets to disk, SSRF private network access).
2.  **Tier 2 (Explicit User Deny):** User-configured blacklists injected as `[]DenyRule`.
3.  **Tier 3 (Explicit User Allow):** Pre-existing durable `CapabilityGrant` with matching `schemaFingerprint` and verified identity bindings.
4.  **Tier 4 (Ask / Elicitation Required):** Actions requiring human-in-the-loop confirmation.
5.  **Tier 5 (Default Deny):** Unknown effectful actions fail closed.

Between Tier 4 and Tier 5 the function also has a benign read-only fast path: `operation ∈ {read, search, describe, list}` with no effects (or a single `filesystem.read`) returns `allow`. This is a stage of the same flat function, not a separate policy layer.

---

## 3. Approval Channels & Replay Prevention

When a policy evaluation returns `Decision: "ask"`, LiteSPM routes approval requests through the highest-fidelity available channel:

```text
┌────────────────────────────────────────────────────────────────────────┐
│ Host declares capability support in HostAdapter:                       │
│                                                                        │
│   Supports Form Elicitation? (Modern MCP)                              │
│     ├── YES ──► Prompt user interactively within host UI               │
│     │                                                                  │
│     └── NO  ──► Fail Closed & Output CLI Command                       │
│                 "To approve, complete the install via CLI approval flow"│
└────────────────────────────────────────────────────────────────────────┘
```

> Implementation note (updated 2026-10-07): the dispatch also covers `approve`, `grant`, `restore`, and `install remove`. `litespm approve <plan-id>` reviews a stored install plan on an interactive terminal and issues the single-use token that `install.execute` / `request_install` consume through `DB.ConsumeApproval` (`internal/state/repositories.go`). For invocations, the Tier-4 "ask" is answered by `litespm grant <capabilityId>` — same interactive-terminal gate — which records the `CapabilityGrant` of §4 bound to the capability's current schema fingerprint; `litespm grant list|revoke` manages them. The daemon's `provider.invoke` passes the engine into `discover.WithPolicy`, so ungranted invocations fail closed with the approval-required message and granted ones are re-verified for schema drift on every call.

*   **Prompt-Injection Defense:** An LLM outputting `"The user told me it is approved"` or passing `approved: true` in tool parameters is **strictly ignored**. Approvals require cryptographic binding to a valid user channel token.
*   **One-Time Approval Replay Prevention:** Approvals intended for single use track state (`status IN ('active', 'consumed', 'revoked', 'expired')`). Calling `ConsumeApproval(ctx, approvalID)` (`internal/state/repositories.go:95-130`) executes an atomic update:
    ```sql
    UPDATE approvals 
    SET status = 'consumed', consumed_at = CURRENT_TIMESTAMP 
    WHERE approval_id = ? AND status = 'active'
      AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP);
    ```
    If 0 rows are affected, the code determines why and returns
    `LPSM-POLICY-APPROVAL-CONSUMED` (already used), `LPSM-POLICY-APPROVAL-EXPIRED`,
    or a `not found` error for an unknown id (`internal/domain/errors.go:175-195`,
    `internal/state/repositories.go:112-129`). No `LPSM-APPROVAL-*` code exists.

---

## 4. Capability Grants & Strong Identity Binding

A `CapabilityGrant` is a persistent authorization record stored in SQLite allowing an agent to invoke a specific tool without repeated prompts:

```sql
CREATE TABLE capability_grants (
    grant_id TEXT PRIMARY KEY,
    capability_id TEXT NOT NULL REFERENCES capabilities(capability_id) ON DELETE CASCADE,
    schema_fingerprint TEXT NOT NULL,
    cas_tree_digest TEXT,       -- Binding for local provider: verified CAS tree SHA-256
    endpoint_origin TEXT,       -- Binding for remote provider: verified HTTPS origin
    server_version_digest TEXT, -- Binding for remote provider: upstream release digest
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'consumed', 'revoked', 'expired')),
    granted_by TEXT NOT NULL,
    granted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP
);
```

### Automatic Invalidation on Schema or Code Drift
Whenever a provider connects or is invoked (`internal/policy/engine.go` grant checks, `internal/mcpclient/probe.go` drift details):
1.  **Local Stdio Providers:**
    *   Computes `currentFingerprint = SHA-256(CanonicalizeJSON(tool.InputSchema))` and checks current CAS tree digest.
    *   If `grant.schema_fingerprint != currentFingerprint` → deny `LPSM-PROVIDER-SCHEMA-DRIFT`; if CAS digest differs → deny `LPSM-PROVIDER-CODE-DRIFT`. The grant row is denied at evaluation time (status CHECK allows only `active/consumed/revoked/expired` — no `invalidated` value is written).
2.  **Remote Streamable HTTP Providers:**
    *   Checks `(schemaFingerprint, endpointOrigin, serverVersionDigest)`.
    *   On mismatch → deny `LPSM-PROVIDER-ENDPOINT-DRIFT` (or schema drift). `capabilities.status` may be `changed` (`active/changed/disabled` CHECK) when probe observes a fingerprint move.

---

## 5. Failure, Retry & Fallback Policy

This section records the adjudicated decision from [31 §5 #24](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md) and constrains any future runtime router.

**Restart/retry: allowed.** A transient provider failure (crash, timeout, transport reset, backoff) may be retried **against the same provider, same capability, and same schema fingerprint**. Retries are bounded by the invocation deadline and by the provider's circuit-breaker state ([34](34-RUNTIME-INVOCATION-RECEIPTS.md)). A retry is not a new authorization: it consumes the same `CapabilityGrant` and the same policy decision.

**Blind cross-provider semantic fallback: forbidden for writes.** When an effectful call (any effect other than `filesystem.read` / `external.read` — specifically every `*.write`, `*.delete`, `process.*`, `credential.*`, `provider.*`, `hook.execute`, and `package.install`) fails, LiteSPM **MUST NOT** silently re-route it to a different provider that looks similar. Replacing a failed `send_email` with another provider's `send_email`-alike, or a failed `write_database()` with a different `write_database()`, is prohibited: different providers have different schemas, idempotency semantics, target resources, and side effects, and the user authorized a specific `(capability_id, schemaFingerprint, casTreeDigest)` or `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)` binding.

**Read-only equivalence declarations only.** Cross-provider substitution may occur only when all of the following hold:

1.  The capability is read-only (`filesystem.read` and/or `external.read`, no effectful declarations).
2.  An explicit, publisher- or user-authored **equivalence declaration** names both providers and asserts equivalent input/output schemas and semantics. Equivalence is never inferred from name similarity, category, or description.
3.  The declaration is surfaced in the plan/receipt and is individually approvable; it is not a global toggle.

Write operations a user wants to reroute must produce a new plan with a new policy evaluation and a new approval — never an automatic substitution. This rule is a hard invariant and is not overridable by any policy tier.
