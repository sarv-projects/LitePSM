# Deployment Ledger & Three-Way Reconciliation

Status: **`DESIGNED`.** This document specifies the deployment mutation ledger that replaces backup-based ownership, plus the three-way reconciliation algorithm and uninstall/update semantics. **No `deployment_mutations` table or reconciler exists**; today `host_backups` is a pre-edit snapshot, not an ownership model ([STATUS.md](../STATUS.md) §5). Do not present it as implemented.

---

## 1. Problem

LiteSPM edits other people's configuration files (MCP entries in host configs). Today it records a **backup** before editing. A backup answers "what did the file look like?" but not:

* **Who wrote this node?** Declarative ownership is required to uninstall safely.
* **Has the user edited it since?** A blind restore destroys user changes.
* **What exactly was written?** Structure type and locator, not just a path.

The ledger records **what LiteSPM wrote, where, and against what pre-state**, so uninstall and update can be surgical.

---

## 2. `DeploymentMutation` Ledger

One row per atomic, owned mutation. A single install may write several rows (one per file/structure).

```go
type DeploymentMutation struct {
    MutationID    string    // mutation_<26> (Crockford base32, per ARCH/23 ID note)
    InstallID     string    // inst_<scope>_<listing>_<digest8> (ARCH/23)
    CapabilityID  string    // canonical capability/listing id
    HostID        string    // claude-code, codex, …
    Scope         string    // user | project
    FilePath      string    // absolute, normalized path of the edited file
    StructureType string    // json-object | json-array | toml-table | yaml-path | …
    Locator       string    // in-structure address (see §3)
    PreImageHash  string    // sha256 of the owning structure/image BEFORE the write
    PostImageHash string    // sha256 of the owning structure/image AFTER the write
    Owner         string    // "litespm" or the adopting principal
    CreatedAt     time.Time
}
```

The row is written **inside the same transaction** as the file mutation's journal entry ([12 — Storage](12-STORAGE-TRANSACTIONS-RECOVERY.md)); a file edit without a ledger row is a bug, and recovery treats an orphan edit as drift.

### 2.1 Relationship to `host_backups`

`host_backups` is retained as a last-resort safety net and recovery aid. It is **not** the ownership source of truth. Ownership queries (`why`, uninstall, reconcile) read the ledger.

### 2.2 Image hashes

`PreImageHash`/`PostImageHash` are digests over the **owned structure** (the JSON object / TOML table / mapped node), canonically encoded (RFC 8785 JCS for JSON), not over the whole file. Whole-file hashes are recorded separately by the backup layer and are too coarse to distinguish "our node changed" from "an unrelated node changed".

---

## 3. Locator Grammar

The locator addresses the owned node deterministically and survives comment-preserving splices ([30 §4](30-DATA-DRIVEN-BRIDGE-TARGETS.md)).

```text
locator := segment ( "." segment )*
segment := key | index | keyGlob
key     := bare-key | quoted-key
example := mcpServers.litespm
example := mcp.servers.litespm
example := mcp_servers.litespm            # TOML [mcp_servers.litespm]
```

Rules:

* A locator MUST resolve against the current file at reconcile time or be reported as **missing** (not silently recreated) unless the operation is an install of that structure.
* `structureType` disambiguates syntax so the same logical locator is read/written with the right parser.
* Locators are never host-specific strings outside the `BridgeTarget` table ([30](30-DATA-DRIVEN-BRIDGE-TARGETS.md)); the table is the only place per-agent knowledge lives.

---

## 4. Three-Way Reconciliation

At verify/uninstall/update time, for each owned locator the reconciler computes three images:

```text
A  = PreImageHash    (what the locator held before LiteSPM wrote)
B  = PostImageHash   (what LiteSPM wrote — the owned image)
C  = current hash    (what the locator holds NOW)
```

Decision space:

| A vs C | B vs C | Meaning | Default action |
|---|---|---|---|
| equal | — | **Unmodified by user.** | Safe to update/uninstall. |
| different | equal | User edited, then reverted to our image. | Treat as **unmodified** (C == B). |
| different | different | **User edited our node.** | Merge/conflict — do not overwrite. |
| — | missing | Node (or file) was removed. | Report orphan; do not recreate silently. |

When `C != B` (user changed our node), the reconciler offers explicit resolutions — never a default destructive write:

* **Keep local** — keep the user's current node; detach ownership (LiteSPM stops claiming it). Record the detach.
* **Use package** — overwrite with `B` (requires approval; the diff is shown).
* **Diff** — show a structural diff of A→C vs B so the user decides.
* **Detach** — remove the ledger row and leave the node as-is.

Rules:

* No resolution is applied without a plan and, for destructive choices, an approval ([15](15-POLICY-APPROVALS.md)).
* "Use package" MUST be explicit; there is no auto-repair that silently discards user edits.
* A reconcile pass is read-only until a resolution is chosen; it emits a report even when it writes nothing.

---

## 5. Uninstall Semantics

`uninstall` is ledger-driven:

1. Load all `DeploymentMutation` rows for the install/capability.
2. For each, three-way reconcile. If `C != B`, the node is **not** removed; it is reported (kept-local by default) and the ledger row is detached.
3. If `C == B`, remove exactly the owned locator (comment-preserving splice), leaving sibling nodes and comments intact.
4. Remove CAS/artifact references only when no other install/ledger row references them (content-addressed ref-counting).
5. Journal and apply atomically; roll back on partial failure.

Uninstall MUST NOT restore a whole-file backup (that destroys unrelated user changes) and MUST NOT remove a node it no longer owns.

---

## 6. Update Semantics

An update is `uninstall(old owned node)` followed by `install(new owned node)` under one plan and one rollback scope:

* The new `PreImageHash` for the update is the `C` observed at update time (so a user-edited node produces a conflict, not a silent overwrite).
* If `C != oldPostImage`, the update halts with a conflict and offers the §4 resolutions; it does not proceed.
* Both old and new mutations are journaled together; a failed update restores the pre-update image.
* The lockfile ([32](32-MANIFEST-LOCK-INTEROP.md)) records the new `planDigest`; `audit --ci` ([36](36-ENTERPRISE-POLICY-AND-AUDIT.md)) replays writes and diffs expected vs actual.

---

## 7. Acceptance Test Sketch

1. Install, hand-edit the owned node, uninstall → node is preserved, reported, and detached; siblings untouched.
2. Install, leave untouched, uninstall → owned node removed, comments and sibling keys byte-identical.
3. Install v1, hand-edit node, update to v2 → conflict, not overwrite; each resolution branch behaves as specified.
4. Recovery after a crash mid-write leaves either the pre-state or the post-state, with a matching ledger row or no row.
5. A property test asserts `PreImageHash`/`PostImageHash` are stable under key reordering for JSON structures.
6. `why <path>` resolves a deployed path to its install, capability, mutations, and source.

---

## 8. Related Documents

* Storage journal and recovery: [12 — Storage, Transactions & Recovery](12-STORAGE-TRANSACTIONS-RECOVERY.md).
* Host merge mechanics and comment preservation: [16](16-HOST-ADAPTERS.md), [30](30-DATA-DRIVEN-BRIDGE-TARGETS.md).
* Plan/approval binding: [15 — Policy & Approvals](15-POLICY-APPROVALS.md).
* Lockfile projections and identity: [32 — Manifest, Lockfile & Interop](32-MANIFEST-LOCK-INTEROP.md).
* Replay audit and drift detection: [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md).
