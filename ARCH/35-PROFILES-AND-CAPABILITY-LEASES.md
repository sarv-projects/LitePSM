# Profiles & Capability Leases

Status: **`DESIGNED`** for the OCI distribution, the `profile` verb set and runtime integration; the **library layer is `IMPLEMENTED`, not wired**: `internal/profiles` parses and validates profiles, diffs them, and issues/checks/revokes time- and scope-bounded leases with mandatory TTL and schema pinning (tested). Nothing consumes it yet — no `profile` verbs exist, leases are not consulted at the invoke gate, and both remain gated on the runtime invocation registry ([34](34-RUNTIME-INVOCATION-RECEIPTS.md)) per §5 below ([STATUS.md](../STATUS.md) §4–§5). Do not present the verbs or the distribution as implemented.

---

## 1. Why Profiles

A user's working context is not one package; it is a *set* of capabilities plus their configuration, targets, and policy. Today that set must be reassembled by hand per project or per agent. A **profile** is a named, versioned, distributable description of such a set.

Profiles are deliberately **wider than a single kind**: a profile may include MCP servers, skills, connectors, agents, and plugins together, exactly as a real workspace does.

---

## 2. Profile Model

```yaml
# profile: acme-backend.senior-review
schemaVersion: 1
name: acme-backend.senior-review
version: 2.1.0
description: Backend review workspace for Acme services.

members:
  - kind: mcp
    requires: mcp:builtin:mcp-registry:postgres
    constraint: ">=1.4 <2"
  - kind: skill
    requires: skill:builtin:agent-skills:review-pr
    constraint: "1.2.0"
  - kind: connector
    requires: connector:acme:internal-api        # connector is a deferred v2 kind (ARCH/26 §3.4.1, ARCH/29, D-021); membership is declarative only — no connector code ships
  - kind: agent
    requires: agent:acp:registry:codex
  - kind: plugin
    requires: plugin:git:anthropic-skills:web-toolkit

targets:
  - host: claude-code
    scope: project
  - host: codex
    scope: project

policy:                       # profile-scoped, tighten-only (ARCH/36)
  allowSources: [builtin:mcp-registry, git:anthropic-skills]
  denyEffects: [external.delete]
```

Rules:

* `kind` is the neutral capability type ([26 §3.4](26-ECOSYSTEM-IA-PACKAGE-MODEL.md)). A profile may mix kinds; it does not flatten them.
* A profile expresses **requirements**, not resolved versions. Resolution produces a lock ([32](32-MANIFEST-LOCK-INTEROP.md)).
* A profile MAY declare policy, but it can only **tighten** inherited policy, never loosen it ([36](36-ENTERPRISE-POLICY-AND-AUDIT.md)).
* Profiles are content-addressable: their canonical digest (RFC 8785 JCS) is the identity used for distribution.

---

## 3. Profile Verbs

| Verb | Contract |
|---|---|
| `litespm profile create <name>` | Author a new profile from current/selected capabilities. |
| `litespm profile activate <name>` | Apply the profile's members/targets to a scope; produces a plan and approval, not a blind write. |
| `litespm profile diff <a> [b]` | Structural diff of two profiles (or a profile vs the active install) — members, targets, policy, versions. |
| `litespm profile export <name>` | Emit the profile as a portable bundle (OCI or tar). |
| `litespm profile import <ref>` | Import a bundle; produces a preview diff before any write. |
| `litespm profile push <name> <oci-ref>` | Publish a signed profile to an OCI registry (§4). |
| `litespm profile pull <oci-ref>` | Fetch and verify a profile from an OCI registry. |

`activate` MUST be idempotent and MUST show the diff first. It never bypasses plan/approval.

---

## 4. OCI Distribution

Profiles are distributed as OCI artifacts to reuse existing registry auth, content addressing, and signing.

* **Manifest:** an OCI image manifest (`application/vnd.oci.image.manifest.v1+json`) with a custom artifact type, e.g. `application/vnd.litespm.profile.v1`.
* **Config blob:** the canonical-JSON profile document.
* **Layers:** an optional lock projection and any bundled files.
* **Digest pinning:** `pull` MUST resolve by digest; a tag-only reference is allowed for discovery but the resolved digest is recorded in the lock.
* **Signing:** cosign/Sigstore ([36](36-ENTERPRISE-POLICY-AND-AUDIT.md)); an unsigned `pull` is allowed only when policy permits and is recorded as `signature.result: unavailable`.

The registry is untrusted: profiles are parsed, schema-validated, size-bounded, and never executed.

---

## 5. Capability Leases

A `CapabilityGrant` ([15 §4](15-POLICY-APPROVALS.md)) is durable until revoked. A **lease** is a bounded grant: it scopes a capability to a *task/session* for a limited time and permission set, and disappears automatically.

```go
type CapabilityLease struct {
    LeaseID        string    `json:"leaseId"`        // lease_<26>
    CapabilityID   string    `json:"capabilityId"`
    SchemaFingerprint string `json:"schemaFingerprint"`
    Scope          string    `json:"scope"`          // session | task
    SessionID      string    `json:"sessionId,omitempty"`
    TaskID         string    `json:"taskId,omitempty"`
    Permissions    []string  `json:"permissions"`    // allowed effects/verbs for this lease
    CredentialRef  string    `json:"credentialRef,omitempty"` // brokered; the agent never holds it
    IssuedAt       time.Time `json:"issuedAt"`
    ExpiresAt      time.Time `json:"expiresAt"`       // mandatory TTL
    RevokedAt      *time.Time `json:"revokedAt,omitempty"`
    IssuedBy       string    `json:"issuedBy"`        // approval / policy decision
}
```

Invariants:

* **Mandatory TTL.** A lease always expires; there is no non-expiring lease. Evaluation refuses an expired lease with `LPSM-LEASE-EXPIRED`.
* **Scope binding.** A lease is valid only within its bound `sessionId`/`taskId`. Crossing sessions is refused.
* **Auto-revoke.** Session/task end revokes its leases; the capability disappears from discovery without a manual step. Revocation is recorded in the audit log.
* **Permission narrowing.** Lease permissions are a subset of the underlying grant's effects; a lease can never widen them.
* **Credential custody.** When a lease carries credentials, they are broker-injected at provider launch; the agent receives no secret material. This preserves the brokered-connector vs MCP-server distinction ([31 R10](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md)). The injection seam itself is `IMPLEMENTED`, not `WIRED` — `ResolveLaunchSecrets` has no production caller (`STATUS.md` §1).
* **Binding to schema.** A lease pins a `schemaFingerprint`; schema drift invalidates it exactly as it invalidates a grant ([15 §4](15-POLICY-APPROVALS.md)).
* **Dynamic MCP caution.** Dynamic capability exposure is **not** a differentiator and not unique ([31 §3.3](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md)); leases are adopted on their own merit, after the runtime registry ([34](34-RUNTIME-INVOCATION-RECEIPTS.md)) exists.

---

## 6. Acceptance Test Sketch

1. `profile create` → `export` → `import` round-trips members, targets, and policy.
2. `profile diff` reports member/version/policy deltas before `activate` writes anything.
3. `profile push` then a digest-pinned `profile pull` verifies content; a tampered digest is refused.
4. A profile attempting to loosen inherited policy is rejected.
5. A session lease expires: the capability is no longer invocable and `LPSM-LEASE-EXPIRED` is returned.
6. A task end auto-revokes its leases and the audit log records the revocation.
7. A lease bound to session A cannot be used from session B.

---

## 7. Related Documents

* Grants and approval binding: [15 — Policy & Approvals](15-POLICY-APPROVALS.md).
* Package model and capability taxonomy: [26 — Ecosystem IA & Package Model](26-ECOSYSTEM-IA-PACKAGE-MODEL.md).
* Registry needed before leases are meaningful: [34 — Runtime, Invocation & Receipts](34-RUNTIME-INVOCATION-RECEIPTS.md).
* Tiered policy and signing for OCI/profile distribution: [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md).
* Connector credential custody and its resurrection direction (`D1` re-decision first): [29 — Connector System Design](29-CONNECTOR-SYSTEM-DESIGN.md); decision record: [D-021](07-DECISIONS.md).
