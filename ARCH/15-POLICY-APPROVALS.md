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
│ package.install     │ Unpacks software files into LitePSM CAS store.                   │
│ provider.start      │ Initializes a local or remote MCP provider session.              │
│ provider.stop       │ Terminates an active MCP provider session.                       │
└─────────────────────┴──────────────────────────────────────────────────────────────────┘
```

---

## 2. Policy Engine Evaluation Contract

The Policy Engine (`internal/policy`) evaluates every proposed action against local security rules:

```go
type PolicyInput struct {
    Actor               string                `json:"actor"`               // user | agent | system
    HostID              string                `json:"hostId"`              // e.g. claude-code, codex
    Operation           string                `json:"operation"`           // install | update | invoke
    TargetRef           string                `json:"targetRef"`           // ListingId or CapabilityId
    Effects             []string              `json:"effects"`             // List of canonical effects
    RequestedAccess     []string              `json:"requestedAccess"`
    SchemaFingerprint   string                `json:"schemaFingerprint,omitempty"`
    Scope               string                `json:"scope"`               // user | project
}

type PolicyDecision struct {
    Decision            DecisionKind          `json:"decision"`            // allow | deny | ask
    ReasonCodes         []string              `json:"reasonCodes"`
    MatchedRuleIDs      []string              `json:"matchedRuleIds"`
    RequiredChannel     ApprovalChannel       `json:"requiredChannel"`     // cli-tty | mcp-elicitation | native-host
}
```

### 2.1 Decision Precedence Rules
Rules are evaluated in strict priority order. The first matching tier terminates evaluation:
1.  **Tier 1 (Hard Invariant Deny):** Cannot be overridden. (e.g., command marketplace sources, writing secrets to disk, SSRF private network access).
2.  **Tier 2 (Explicit User Deny):** User-configured blacklists in `config.toml`.
3.  **Tier 3 (Explicit User Allow):** Pre-existing durable `CapabilityGrant` with matching `schemaFingerprint`.
4.  **Tier 4 (Ask / Elicitation Required):** Actions requiring human-in-the-loop confirmation.
5.  **Tier 5 (Default Deny):** Unknown effectful actions fail closed.

---

## 3. Approval Channels & Fail-Closed Behavior

When a policy evaluation returns `Decision: "ask"`, LitePSM routes approval requests through the highest-fidelity available channel:

```text
┌────────────────────────────────────────────────────────────────────────┐
│ Host declares capability support in HostAdapter:                       │
│                                                                        │
│   Supports Form Elicitation? (Modern MCP)                              │
│     ├── YES ──► Prompt user interactively within host UI               │
│     │                                                                  │
│     └── NO  ──► Fail Closed & Output CLI Command                       │
│                 "To approve, run in terminal: litepsm approve <hash>"   │
└────────────────────────────────────────────────────────────────────────┘
```

*   **Prompt-Injection Defense:** An LLM outputting `"The user told me it is approved"` or passing `approved: true` in tool parameters is **strictly ignored**. Approvals require cryptographic binding to a valid user channel token.

---

## 4. Capability Grants & Schema-Drift Invalidation

A `CapabilityGrant` is a persistent authorization record stored in SQLite allowing an agent to invoke a specific tool without repeated prompts:

```sql
CREATE TABLE capability_grants (
    grant_id TEXT PRIMARY KEY,
    capability_id TEXT NOT NULL REFERENCES capabilities(capability_id) ON DELETE CASCADE,
    schema_fingerprint TEXT NOT NULL,
    granted_by TEXT NOT NULL,
    granted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP
);
```

### Automatic Invalidation on Drift
Whenever a provider connects:
1.  The daemon recalculates `currentFingerprint = SHA-256(JCS(tool.InputSchema))`.
2.  Queries `capability_grants` for the tool.
3.  If `grant.schema_fingerprint != currentFingerprint`:
    *   The grant is automatically deleted or marked `status = 'invalidated'`.
    *   The capability status in SQLite is updated to `'changed'`.
    *   Subsequent invocations return error code `LPSM-PROVIDER-SCHEMA-DRIFT`, requiring fresh user inspection and approval.
