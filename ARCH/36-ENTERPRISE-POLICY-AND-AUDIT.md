# Enterprise Policy, Audit, Provenance & Supply-Chain Trust

Status: **`DESIGNED`.** This document specifies the tighten-only policy hierarchy, `policy explain`, `audit --ci`, advisories/revocation, TUF-style catalog signing, SBOM/provenance, and air-gapped bundles. **None of it exists.** `internal/policy` is a flat `Evaluate` with injected `[]DenyRule` and no provenance or hierarchy ([STATUS.md](../STATUS.md) §5). Do not present it as implemented.

---

## 1. Tighten-Only Policy Hierarchy

Policy is evaluated over a fixed hierarchy. A lower layer may **narrow** a higher layer's permissions; it may **never** widen them.

```text
 1. Hard invariants        (compiled in; cannot be overridden anywhere)
 2. Admin                  (machine/tenant)
 3. Org
 4. Team
 5. Repo                   (litespm.yml / project)
 6. User                   (config.toml)
 7. Session                (runtime, ephemeral — e.g. lease scope)
```

Rules:

* **Monotonic narrowing.** The effective allow-set is the intersection down the chain; the effective deny-set is the union. There is no "allow overrides deny" at any layer.
* **Hard invariants are not a layer that can be voted away.** They are the compiled invariants in [15 §2.1](15-POLICY-APPROVALS.md) (command-source execution, plaintext-secret writes, SSRF).
* **Conflict is never resolved by widening.** If two layers disagree, the stricter result wins; `policy explain` reports the conflicting chain.
* **No silent source.** Every rule carries its origin (layer, file, rule ID) so a decision can be explained and audited.
* **Compatibility.** The existing flat `[]DenyRule` injected into `Engine.Evaluate` is the degenerate one-layer case and must keep working; the hierarchy is an extension of the same decision contract, not a second engine.

### 1.1 `PolicyDecision.provenance`

```go
type RuleProvenance struct {
    Layer      string `json:"layer"`      // hard_invariant|admin|org|team|repo|user|session
    RuleID     string `json:"ruleId"`
    Source     string `json:"source"`     // file path / compiled-in name
    Effect     string `json:"effect"`
    Decision   string `json:"decision"`   // allow|deny|ask
    Inherited  bool   `json:"inherited"`  // true when the deciding rule came from a parent layer
}

type PolicyDecision struct {           // extension of the ARCH/15 shape
    Decision    string           `json:"decision"`
    ReasonCodes []string         `json:"reasonCodes"`
    MatchedRuleIDs []string      `json:"matchedRuleIds"`
    Provenance  []RuleProvenance `json:"provenance"`   // NEW; required
    RequiredChannel string       `json:"requiredChannel"`
    Detail      string           `json:"detail,omitempty"`
}
```

`Provenance` is required for every decision so `policy explain` and `audit --ci` can attribute a deny to the exact inherited rule.

### 1.2 `litespm policy explain`

`litespm policy explain <operation> <target>` prints the full inherited chain:

* which layers were consulted, in order;
* each rule that matched and each that was skipped, with reason;
* the effective decision and the layer that decided it;
* for a deny, the single rule to change.

`--json` emits the same content machine-readably. This is a prerequisite for the hierarchy being usable, and it is the smallest valuable slice of this document ([31 §5 #29](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md)).

---

## 2. `litespm audit --ci`

`audit --ci` replays and verifies the deployed state without modifying it. Checks:

| Check | What it verifies |
|---|---|
| Lock consistency | `litespm.lock` matches `litespm.yml` and `--check` would pass ([32](32-MANIFEST-LOCK-INTEROP.md)). |
| Signature | Recorded signature results are `verified` where policy requires; `unavailable` is a failure when required. |
| SBOM | The SBOM digest recorded in the lock matches the artifact graph. |
| Ownership | Every deployed node has a ledger row; every ledger row resolves ([33](33-DEPLOYMENT-LEDGER-RECONCILIATION.md)). |
| Three-way drift | Pre/owned/current images reconcile; user edits are reported, not overwritten. |
| Scratch replay | Reinstall into a scratch location from the lock and diff expected vs actual (missing integrations, hand-edits, orphaned outputs, hashes). |
| Scan | Static content scan verdicts with exact reasons and source lines (labelled **scan**, not verification). |
| Policy | The effective policy decision for the deployment still holds under the current hierarchy. |
| Schema drift | Live capability schema fingerprints match the lock. |
| Licenses | Every resolved package has an SPDX id and a policy-acceptable license; `NOASSERTION` is reported. |

Output formats: `text`, `json`, `sarif` (for code-scanning UIs), `cyclonedx-json`, `spdx-json`. A single finding never aborts the run; the exit code is per-category, consistent with `doctor` ([20](20-ERRORS-AUDIT-DOCTOR.md)).

Scratch replay is the load-bearing check: it is what turns "we recorded a lock" into "we can prove the deployment matches the plan".

---

## 3. Advisories, Revocation & Quarantine

```go
type Advisory struct {
    AdvisoryID  string    // adv_<26>
    PackageID   string
    AffectedRange string  // semver / commit range
    Severity    string    // low|medium|high|critical
    Summary     string
    IssuedAt    time.Time
    Source      string    // signed advisory origin
    Signature   SignatureResult
}
```

* Advisories are **signed** and pulled from configured origins; unsigned advisories are not trusted.
* **Quarantine** marks a capability non-invocable and non-installable, but **never silently removes user software**. Removal is an explicit, approved uninstall ([33 §5](33-DEPLOYMENT-LEDGER-RECONCILIATION.md)).
* Revocation of a signing key/signer is a distinct action from an advisory.
* `doctor` and `audit --ci` surface quarantined/affected capabilities with the advisory that caused it.

---

## 4. TUF-Style Signed Catalog

The public catalog release tree ([18](18-CATALOG-BUILDER-RELEASE-SEARCH.md)) gains a trust root so clients can detect rollback and freeze:

```text
 root.json        (offline/root-key signed; pins the online keys)
 timestamp.json   (short-lived; pins the current snapshot)
 snapshot.json    (pins the targets metadata)
 targets.json     (pins the release files by digest/size)
```

Rules:

* Clients MUST reject a `timestamp`/`snapshot` that rolls back to an older version, and MUST reject metadata whose signature does not chain to `root.json`.
* The existing `manifest.json` per release ([18 §2.1](18-CATALOG-BUILDER-RELEASE-SEARCH.md)) remains the file-level digest list; TUF metadata wraps it.
* TUF depends on a published release tree; it is downstream of the publisher work ([31 §12](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md)).

---

## 5. SBOM & Provenance

* **SBOM:** CycloneDX and SPDX, generated from the lock ([32 §4.1](32-MANIFEST-LOCK-INTEROP.md)); the SBOM digest is recorded and attested.
* **Build provenance:** SLSA-style provenance for released binaries and packages, describing source, builder, and inputs.
* **Signing:** Sigstore/cosign for releases and (later) packages; signed tags. Self-update verifies SHA-256 only today, with **no** signature check ([22 §3](22-PLATFORM-RELEASE-MIGRATIONS.md), [STATUS.md](../STATUS.md) §1).
* **Rule:** a signature/provenance field is `unavailable` until a real verifier produces `verified`/`failed`. No fabricated green.

---

## 6. Air-Gapped Bundles

An **air-gapped bundle** is a signed, self-contained snapshot for environments with no network:

* contents: the lock, all resolved artifacts (by digest), the SBOM(s), provenance/attestations, the profile(s) ([35](35-PROFILES-AND-CAPABILITY-LEASES.md)), and the TUF metadata needed to verify them offline;
* `litespm bundle export` produces it; `litespm bundle import` verifies signatures/digests and installs `--frozen`;
* no step may fetch from the network; a missing artifact is a hard failure, never a fallback fetch.

This is a legitimate differentiator once it exists; it is `DESIGNED` and must not be described as shipped.

---

## 7. Acceptance Test Sketch

1. A repo-layer policy that tries to allow a hard-invariant-denied effect is refused; `policy explain` names the invariant.
2. A user-layer rule cannot loosen an org deny; the effective decision is the stricter one.
3. `audit --ci --format sarif` fails on a hand-edited owned node detected by scratch replay, and on an expired/replaced lock.
4. A rollback of `timestamp.json` is rejected.
5. A quarantined capability becomes non-invocable but is not removed; removal requires explicit approval.
6. `bundle export` → offline `bundle import` succeeds with the network disabled; a tampered artifact fails.
7. `policy explain --json` attributes every deny to exactly one inherited rule.

---

## 8. Related Documents

* Current flat policy engine, invariants, effects, grants: [15 — Policy & Approvals](15-POLICY-APPROVALS.md).
* Lock, SBOM, provenance digests: [32 — Manifest, Lockfile & Interop](32-MANIFEST-LOCK-INTEROP.md).
* Ledger and replay input: [33 — Deployment Ledger & Reconciliation](33-DEPLOYMENT-LEDGER-RECONCILIATION.md).
* Receipts and invocation audit: [34 — Runtime, Invocation & Receipts](34-RUNTIME-INVOCATION-RECEIPTS.md).
* Release verification today (SHA-256 only): [22 — Platform, Release & Migrations](22-PLATFORM-RELEASE-MIGRATIONS.md).
* Error taxonomy, audit log, `doctor`: [20 — Errors, Audit & Doctor](20-ERRORS-AUDIT-DOCTOR.md).
