# Competitive Landscape, Proposal Adjudication & Ordered Roadmap

## 1. Status & Precedence

*   **Informative.** This document records a third-party comparison, an adjudication of an
    external proposal set, and an ordered backlog. It creates **no new normative contract** and
    changes no existing one. Where it disagrees with a normative document, the normative document
    wins (precedence per [00 — Index](00-INDEX.md) §1).
*   **Binding on vocabulary only.** §2 (evidence states) and §13 (forbidden claims) are binding on
    every other document in this repository and on release communications.
*   **Snapshot:** written against `ff0a1db` (the `LitePSM` → `LiteSPM` rename and remediation
    phases 0–3 are **committed** at this revision). The working tree now carries the two-wave
    documentation overhaul **plus one uncommitted code change**: `internal/catalog` and
    `internal/catalogbuild` were corrected to emit and fetch `/v1/releases/<id>/…` with a
    path-contract test (`TestReleasePathContractPinsDocumentedLayout`,
    `internal/catalog/catalog_test.go:217`), and `release.yml`/`npm/package.json` were extended to
    stage `LICENSE`/`NOTICE` into the npm tarball. Where this document cites code, the citation was
    verified in that tree. Competitor rows in §3 carry their own verification date and method.
*   It extends, and does not replace, the defect tracker in [REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md).
    Findings 4.1–4.4 below are **new** and are not in that tracker's numbering.

---

## 2. Evidence vocabulary (binding, repo-wide)

The single largest documentation defect in this repository is that "done" has meant at least four
different things. This document fixes the terms. **These six states are distinct and may not be
collapsed.** The search `grep -rn 'DESIGNED\|IMPLEMENTED\|WIRED\|SHIPPED' ARCH/` returned nothing
before this document: the vocabulary did not previously exist.

| State | Meaning | Minimum evidence to claim it |
|---|---|---|
| `DESIGNED` | Specified in an ARCH document. No code implied. | A normative section exists |
| `IMPLEMENTED` | Functions/types exist and are reachable from a package API. | Code compiles; unit tests pass |
| `WIRED` | Reachable from a real user workflow, with its real dependencies supplied by production code. | A named non-test caller exists |
| `TESTED` | Covered by an automated test that would fail if the behaviour regressed. | A test asserts the behaviour, not merely that the symbol exists |
| `VERIFIED` | An independent falsifier attempted to break the claim and could not. | Mutation/reproduction evidence, per the [REMEDIATION-PLAN](../REMEDIATION-PLAN.md) verification protocol |
| `SHIPPED` | Published to users through a real release channel. | A tag, a published artifact, and a live origin serving it |

**Rule.** "Files exist" is at most `IMPLEMENTED`. "Tests exist" is at most `TESTED`. Nothing is
`SHIPPED` because a package compiles. A component whose only caller is a test is `IMPLEMENTED`, not
`WIRED` — this distinction is load-bearing, and §4.1 is a live example of the failure mode.

---

## 3. Competitor positions (what was actually verified)

Method: primary sources only — vendor documentation, vendor repositories, package registries,
public spec trackers. Marketing pages were read but never used as the sole basis for a capability
claim. Verified 2026-10-05; the Microsoft APM and OpenAPM rows were re-verified against primary
sources on 2026-10-06 and corrected — see §3.4 for what that correction changed.

### 3.1 Verified as described

| System | Verified capability | Source |
|---|---|---|
| **Microsoft APM** (`github.com/microsoft/apm`, MIT, Python) | Lockfile with content hashes and deployed-file records; frozen installs (`apm install --frozen`); **CycloneDX 1.5 / SPDX 2.3** SBOM export (`apm lock export --format cyclonedx\|spdx`, purl identity, deterministic, credentials scrubbed); `apm audit --ci` that **replays the install into a scratch location** and diffs expected vs actual (hand-edits, missing integrations, orphaned outputs, hashes, ownership) with SARIF output; tighten-only org policy inheritance (`apm-policy.yml`, enterprise → org → repo); hidden-Unicode scanning on every install; enterprise governance guide | `microsoft.github.io/apm` reference docs, `apm.lock.yaml`, OpenAPM v0.1 (see below) |
| **Microsoft APM — MCP installation** | `apm install --mcp` installs from the **MCP registry** (`api.mcp.github.com`), from stdio argv after `--`, or from a **remote URL**; writes per-harness MCP config through a documented path/key table; **honours `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME`, `HERMES_HOME`**; injects tokens as **target-native references** (`${GITHUB_TOKEN}`, `${env:GITHUB_TOKEN}`) and states plaintext secrets are never written for runtime-resolved targets; blocks **transitive** MCP servers unless declared or trusted; rejects `websockets`/`file://` and requires HTTPS except literal loopback on Codex; `devDependencies.mcp` from a dependency package never reaches the consumer's config | `microsoft.github.io/apm/consumer/install-mcp-servers/` |
| **Microsoft APM — targets** | GitHub Copilot CLI, Claude Code, Grok Build, Cursor, OpenCode, Codex, Gemini, Windsurf, Kiro, plus Antigravity, Hermes, JetBrains, VS Code and `agent-skills`. Explicitly "vendor-neutral: a sibling registrar can be contributed for any harness" | `microsoft.github.io/apm` targets matrix |
| **Microsoft APM — stated limits** | Microsoft's own docs state its policy is **build-time, not runtime enforcement** ("runtime behavior is your harness's domain"). `apm compile` compiles `instructions/*.instructions.md` into `AGENTS.md` / `CLAUDE.md` / `GEMINI.md` and per-harness rules trees — it is context compilation, not packaging or MCP handling. **No OAuth scope governance.** Prompt-safety analysis is no longer absent: `apm audit` has an experimental external-scanner integration with an LLM-powered mode (`--external-llm`, requires `OPENAI_API_KEY`, fails closed), so the earlier absolute claim that APM "does no semantic prompt-safety analysis" was stale as of 2026-10-06 | APM official documentation, `reference/cli/audit/` |
| **OpenAPM v0.1** | **Published and normative**, not a proposal: `microsoft.github.io/apm/specs/openapm-v0.1/` consolidates `apm.yml`, `apm.lock.yaml`, `apm.policy.yml`, the registry HTTP API, dependency-resolution semantics, the primitive type system and the targets matrix into one RFC-2119 document with 87 normative `req-*` statements, inline Draft 2020-12 JSON Schemas and a `CONFORMANCE.md` covering Producer / Consumer / Registry / Governance. The wire contract is explicitly deferred to v0.2. Originated as `microsoft/apm#1502`, closed 2026-05-28 | `microsoft.github.io/apm/specs/openapm-v01/` |
| **Docker MCP** | Catalog; Gateway as centralized proxy owning server lifecycle/routing/auth; **Profiles**; **Dynamic MCP** exposing `mcp-find`, `mcp-add`, `mcp-config-set`, `mcp-remove`, `mcp-exec`; OCI catalogs; signed server images; SBOM; container isolation; tool allowlists; call tracing; 300+ advertised verified servers | Docker MCP documentation |
| **AAM** (`agent-package-manager`) | `github.com/spazyCZ/agent-package-manager`; Sigstore/GPG package signing; built-in MCP control server with 29 tools / 9 resources; read-only by default with `--allow-write` to enable mutations; its own registry described as in progress | Project repository |
| **Vercel Skills** | `vercel-labs/skills`; `skills-lock.json` lockfile; `add`/`remove`/`list`/`find`/`update`/`init` | Project repository, npm |
| **Pakx** | `pakx-registry-client` federates MCP Registry + Smithery + Glama + GitHub-raw into one manifest, deduplicating by `(source, id)` | Project repository / npm |
| **AgentMods** | `agentmods.dev`; very large artifact index; per-item token cost, static scan results with stated reasons, original/copy classification; explicitly distinguishes a **static scan from actual verification** | `agentmods.dev` |
| **Official MCP Registry** | Formal API and standardized `server.json`, with publishing/authentication rules; namespace-verification assertions | `modelcontextprotocol.io` registry docs |

### 3.2 NOT verified — do not build on these

| System | Status | Note |
|---|---|---|
| **"Baton"** (config compiler: discovery, preview/diff, conditionals, include modes, profile inheritance, target transforms) | **UNVERIFIED** | Four unrelated products use this name. No artifact matching this description was located. Treat the described feature set as an unconfirmed third-party claim. |
| **"Agentry"** (ownership-aware multi-agent deployment, clean removal, exact-commit pinning) | **UNVERIFIED** | Same problem. The *underlying idea* (ownership-aware deployment) is sound engineering and is adopted in §7 on its own merits, not on this citation. |

**Consequence for the roadmap.** No phase below depends on either product existing. Where a proposal
was sourced from them, it is re-derived from a verifiable first principle and marked as such.

### 3.3 Where the external analysis was wrong

| Claim in the source analysis | Finding |
|---|---|
| "Dynamic MCP provisioning would be a unique LiteSPM innovation" | **False.** Docker MCP ships Dynamic MCP with exactly this shape. Not a differentiator. |
| "Another connector architecture — wire the existing one" | **Half stale, half adopted.** The *package* cannot be wired: `internal/connector` was **deleted** under locked decision `D1` (9 files, 2,072 lines) for having zero importers, so there is no existing code to connect. The **direction is adopted** — [ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md) (D-021) records *resurrect this design and wire it*, with re-deciding `D1` as the prerequisite. Until that re-decision ARCH/29 stays a design record marked *not implemented* and no connector code ships. |
| "A `48-command` CLI vocabulary" | Aspirational. The shipped surface is `version, setup/init, self-update, doctor, search, install/add, catalog, daemon, bridge, host, uninstall, agent, skills` (`cmd/litespm/main.go`). |
| "The repository has no LICENSE" | Was correct. `LICENSE` (Apache-2.0) and `NOTICE` were added during this pass. |

---

### 3.4 What re-verifying Microsoft APM corrected (2026-10-06)

The first pass recorded APM as a package/governance benchmark and omitted that it
also ships **end-to-end MCP installation into ~14 harnesses**. That omission was
not cosmetic: MCP installation into host configs is precisely the lane §7 builds
in, so the earlier comparison understated the thing we are measured against.

Three specific corrections, each because the earlier text was wrong rather than
merely incomplete:

*   **APM's MCP surface was absent from the table.** Registry install, remote-URL
    install, `--transport stdio|http|sse|streamable-http`, `--env`, `--header`,
    per-harness config writes, `CLAUDE_CONFIG_DIR` / `CODEX_HOME` / `COPILOT_HOME`
    / `HERMES_HOME` handling and target-native token *references*. Our own adapters
    ignored those environment variables until 2026-10-06, so this was the one row
    where the competitor was not merely ahead on polish but ahead on function.
*   **"No semantic prompt-safety analysis" was stale.** APM's `apm audit` now has
    an experimental LLM-powered external-scanner mode. Experimental and requiring
    an external tool plus a key, but the absolute claim was no longer true.
*   **`apm compile -t copilot` was misdescribed** as a packaging/targeting feature.
    It compiles `instructions/*.instructions.md` into root context files
    (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`) and per-harness rules trees.

What did **not** change, and was independently re-confirmed: the lockfile with
content hashes and deployed-file records, frozen installs, the exact SBOM formats
(CycloneDX 1.5 / SPDX 2.3), the scratch-replay audit with SARIF, tighten-only
policy inheritance, build-time-not-runtime enforcement, and — checked with
suspicion, because it read as a citation of a specification that does not exist —
the OpenAPM reference. It is real: `microsoft/apm#1502` is the originating issue,
and **OpenAPM v0.1 is published as a normative RFC-2119 specification** with
87 `req-*` statements and Draft 2020-12 schemas. A confusing coincidence worth
recording so it is not "corrected" later: there is an unrelated `openapm` GitHub
organisation and an unrelated npm package, both application performance
monitoring, and neither has anything to do with agent packages.

The strategic consequence is unchanged and, if anything, strengthened: **support
OpenAPM rather than compete on manifest ergonomics** (§7). A published normative
spec with conformance classes is a contract to implement against, not a manifest
to invent against.

## 4. New defects found in this repository during the comparison

These are reproducible now, at `ff0a1db`. They are ordered by blast radius.

### 4.1 The agent-facing install path cannot complete — `WIRED` is false

*   `internal/install/engine.go:204` rejects any `Execute` that supplies neither `TreeSource` nor
    `ArchiveSource`.
*   The **only** non-test supplier of either is `cmd/litespm/main.go:863`, which injects a
    synthetic in-memory zip.
*   The daemon handler `install.execute` (`cmd/litespm/main.go:1420-1426`) constructs
    `install.InstallOptions` **without** any source.
*   `internal/bridge/shim.go:349` — the `request_install` Bridge tool — calls exactly that handler.

**Therefore an agent using `/marketplace` cannot install anything.** The CLI can, but only by
fabricating the package locally. `cmd/litespm/main.go:872` is honest about this and prints
`local synthetic package; remote resolve/verify not yet wired`. This is the known `M3`, and it is
the highest-value single fix in the repository: it is the difference between a package manager and
a config editor.

> **Update (2026-10-05).** The **skills half of this defect is closed.** Skills are files, not
> archives, so they never needed the artifact engine: `install.execute` and `litespm install` now
> route `kind=skill` through `installSkillFromListing` (`cmd/litespm/install_skill.go`) onto the
> ledger- and policy-gated skills path, with GitHub tree/blob browse URLs resolved to a clone
> target + pinned ref + subpath (`internal/skills/install.go`). Verified against the live catalog
> (`skill:phuryn:ab-test-analysis`): `prepare_install` → `install.execute` cloned the repo, wrote
> `SKILL.md` into the detected agent directory, and persisted the install record
> (`TestInstallExecuteSkillThroughDaemon`). The **synthetic package is removed**: MCP and plugin
> listings now fail closed with `LPSM-ARTIFACT-UNAVAILABLE` because the published catalog carries
> no artifact locator for them. Closing the archive half (MCP/plugin) is the remaining `M3` work;
> the artifact source that would feed it is ARCH/17 §4's `ArtifactFetcher` — its
> `HTTPArchiveFetcher` is now implemented and tested (`internal/artifact/fetcher.go`:
> HTTPS-only, SSRF/DNS-range refusal, redirect cap, bounded spool, fail-closed digest verification;
> end-to-end pinned by `test/archive_install_e2e_test.go`), with no production caller until the
> catalog carries artifact locators.

### 4.2 The published catalog and the client read different files — the sync path is dead at the origin

`internal/catalog.Client.Sync` fetches `/v1/current.json`, then `/v1/releases/<id>/manifest.json`
and `/v1/releases/<id>/listings.json`. Live probes against the configured origin
(`internal/config/config.go:13`):

```text
200  /v1/current.json
404  /v1/releases/rel-2026-09-30-01/manifest.json
404  /v1/releases/rel-2026-09-30-01/listings.json
```

`litespm catalog sync` therefore could not succeed against production. `web/public/v1/current.json`
exists and is served, but the release tree it points at was not published at the origin. (The client's release
path was corrected from the un-namespaced `releases/<id>/…` to `/v1/releases/<id>/…` in the
working tree — [ARCH/06 §1.1](06-API-CONTRACTS.md#11-endpoint-inventory--what-exists-what-reads-it-what-the-origin-serves).
The 404 was unchanged in either form when the origin was re-probed on 2026-10-05; it was closed
later the same day — see the update below.)

> **Update (2026-10-05, later).** The tree now *builds* in-repo: `litespm catalog build` cuts
> `rel-2026-10-05-01` (sequence 143) from the committed dataset, reproduces it byte-for-byte via
> `-materialize` (which `scripts/deploy-pages.sh` runs), and an automated end-to-end test
> (`test/catalog_e2e_test.go`) builds → serves → syncs → searches it. **The origin was then
> re-published the same day** and serves the Go-built pointer + tree (`rel-2026-10-05-01`, all
> four files 200, byte-identical to the committed release); `catalog sync` succeeds against it.
> The defect in §4.2 is closed. The same-day rename of the deployment from `litepsm` to `litespm`
> is recorded in `REMEDIATION-PLAN.md` M1.

**Three further mismatches in the same file, all verified** (these describe the *live* pointer;
the committed `web/public/v1/current.json` is now Go-built and no longer has them):

1. `manifestDigest` in the live pointer is `sha256` of **`web/data/catalog.json`**
   (`scripts/build_full_catalog.py:556-561`), while `catalogbuild.CurrentPointer.ManifestDigest`
   is documented as the digest of **`manifest.json`**
   (`internal/catalogbuild/compiler.go:40`). Same field, two different files. A client verifying it
   can never match.
2. The live pointer has **no `schemaVersion`** field; `CurrentPointer` declares it at
   `compiler.go:37`. It unmarshals to `0`.
3. The live pointer carries `advisories`, `totalCapabilities`, `mcpServersCount`,
   `agentSkillsCount`, `pluginsCount`, `hostCompatibility` — none of which exist in the Go
   `CurrentPointer` struct. The web UI reads them; the Go client cannot.

### 4.3 Decision D4 is contradicted by the repository — there are two catalog builders

`REMEDIATION-PLAN.md` locks **D4: "Go `catalogbuild` is the single builder."** In the tree:

*   `internal/catalogbuild/compiler.go` emits exactly four files: `v1/current.json` and
    `v1/releases/<id>/{listings,versions,manifest}.json`. At the time of the comparison it had
    **no non-test caller** — `grep -rn 'catalogbuild\.'` outside its own package found only type
    references from `internal/catalog/client.go` and three test call sites — and published nothing.
*   `scripts/build_full_catalog.py` is what actually produces the deployed catalog:
    `web/data/catalog.json` (the ~3.4 MB blob the web app bundles), `web/public/v1/current.json`,
    and `web/data/release.json`. It honours `LITESPM_RELEASE_ID`.

Either D4 is honoured (delete the Python builder, wire the Go one, publish the release tree) or D4
is amended. Leaving both while calling the Go one canonical means the canonical builder is dead code.

> **Update (2026-10-05, later).** The contradiction has been reduced to a *split*, not resolved as
> a decision: `internal/catalogbuild` now has a non-test caller (`litespm catalog build` in
> `cmd/litespm/main.go`), which owns the served pointer, the release id/sequence, and the release
> tree; `scripts/build_full_catalog.py` was narrowed to dataset ingestion (`web/data/catalog.json`
> + stats in `web/data/release.json` — single writer per key, ids funnelled through fail-closed
> `canonical_id()`; its `LITESPM_RELEASE_ID` override was removed with the pointer write). The
> Python builder is no longer a second *release* builder and no longer
> writes `web/public/v1/*`. **D4 itself is still unadjudicated** — the split needs to be accepted
> or overruled (`STATUS.md` §2).

### 4.4 `ARCH/18` specifies an output tree the compiler does not produce

*   §1's pipeline declares `Partition Shards (shards/<kind>/<category>.json)` and `Generate Compact
    Search Index (index.json)`. §2's tree also declares `metadata.json` and `items/`.
  **The compiler emits none of them** (`compiler.go:102,118,151,172` — the four assignments above).
*   §4's CDN cache policy **is** implemented: `scripts/deploy-pages.sh:31-42` writes a `_headers`
  file into the staging directory at deploy time, with `immutable` caching for `/v1/releases/*` and
  `no-cache, must-revalidate` for `/v1/current.json`, exactly as §4 specifies. The file is a build
  artifact under the gitignored `pages-dist/`, which is why it is absent from the repository tree.
  *(An earlier draft of this document claimed §4 was unimplemented on the basis of
  `find . -name _headers` returning nothing. That inference was wrong — a build-generated file is
  not a source-tree file. The claim is retracted; §4 stands as specified and implemented.)*
*   `TODO.md` `LPSM-C002` is marked **Completed** with acceptance evidence "Two successive builds
  from identical source fixtures yield byte-for-byte identical output" — true, and unrelated to
  whether the specified artifacts exist. `LPSM-C004` ("CI audit verifies only generated release JSON
  is published") is marked **Completed** and does hold: `deploy-pages.sh` implements both the
  allowlist and the leak audit.

---

## 5. Adjudication of the 60 proposals

Verdicts: **ADOPT** (build it) · **ADAPT** (build it, changed) · **DEFER** (right, not yet) ·
**REJECT** (do not build) · **DONE** (already in the tree).

| # | Proposal | Verdict | Evidence / reason |
|---|---|---|---|
| 1 | Reframe as 5-plane control plane | ADAPT | The 5 planes are the right decomposition and largely match `ARCH/02`. Do **not** rebrand in `README.md` before §4.1–4.4 close; the current one-line description is the more honest claim. |
| 2 | Five structural holes (install path, catalog pipeline, runtime router, connectors, docs) | ADOPT | Holes 1, 2, 5 confirmed and quantified in §4.1, §4.2, §4.3, §6. **Hole 4 was mis-stated as an unwired package** — `internal/connector` was deleted under D1, so the connector gap is tracked as a resurrection ([ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md), re-decide `D1` first), not as existing code awaiting a wire. Hole 3 confirmed: zero non-test `INSERT INTO providers` in the tree. |
| 3 | APM is the package/governance benchmark | ADOPT | Verified §3.1. Steal scratch-replay audit; do not compete on manifest ergonomics. |
| 4 | Support OpenAPM rather than compete | ADOPT | Spec issue #1502 is real. Import/export is cheap and de-risks the ecosystem bet. |
| 5 | Dynamic Capabilities + capability leases | DEFER | Dynamic MCP is **not** a differentiator (§3.3). Leases are genuinely novel and cheap *after* §4.1/§4.2. |
| 6 | Profiles wider than Docker's + OCI backend | DEFER | Docker already has OCI push/pull. Profiles are a Phase-8 UX feature; OCI is a Phase-6 backend. |
| 7 | Split Package / PackageRelease / Components / Projection / RuntimeCapabilities | DEFER | The *concept* is right and `ARCH/26` §3 already encodes a neutral 8-type taxonomy. Executing a schema split before the release tree is published (4.2) would be cost without a user-visible gain. |
| 8 | Stronger manifest + lockfile (≈25 recorded fields) | ADOPT | No `litespm.yml`/`litespm.lock` exists today. This is APM parity and a hard prerequisite for reproducible install. |
| 9 | Deployment ledger instead of `ownership.json` | ADOPT | Replaces backup-based ownership. Existing `host_backups` is a snapshot, not an ownership model. High value: makes uninstall safe against user edits. |
| 10 | Three-way reconciliation (A/B/C) | ADOPT | Depends on #9. This is a real package-manager capability that none of the verified competitors demonstrably has. |
| 11 | `compile` / `preview` / `diff` over a canonical IR | DEFER | Needs #7. Premature while §4.1 stands. |
| 12 | Federation UX: one canonical ID, sources as proof | ADOPT | Directly matches `ARCH/27`'s intent. `internal/source` has 8 adapters; the user-facing unification does not exist. |
| 13 | Canonical identity + alias graph with relationship types | ADOPT | Strongest clean-up lever. The `Listing` ID grammar is already normative (`ARCH/10`); the cross-source edge set is not modelled. |
| 14 | Evidence Card instead of a single score | ADOPT | Reinforces `ARCH/26` §12.4. **The source analysis's "Verified by LitePSM Security Guild" wording must be rejected outright** — no such certification exists. |
| 15 | Token cost as a first-class resource + context budget | DEFER | Real, and LiteSPM can measure it better than AgentMods because it owns the runtime. But it is analytics until §4.1 lands. |
| 16 | Two-surface management bridge (READ / MUTATING) with plan-bound approval | ADOPT | LiteSPM's existing plan/approval architecture already exceeds AAM's `--allow-write` global switch. Mostly a matter of splitting the current 12 Bridge tools along the read/write line. |
| 17 | Vercel `skills-lock.json` import/export | ADOPT | Verified §3.1. `internal/skills` already has atomic update, dry-run and provenance (§REMEDIATION-PLAN finding 118). Import/export is the small half. |
| 18 | Data-driven target capability registry for all 77 agents | ADOPT | **Partly done**: `ARCH/30` + `internal/host/targets_data.go` already do this for 44 generic BridgeTargets. Extend the same table to skills/instructions rather than building a new mechanism. |
| 19 | Official MCP Registry as high-confidence identity source | ADOPT | Already an adapter (`internal/source/mcp_registry.go`). Upgrade is preserving its namespace-verification assertions through normalization instead of flattening them. |
| 20 | Bidirectional plugin import/export (Agent Plugins, Claude plugins) | DEFER | Genuine interop gap, but a large surface. Phase 4. |
| 21 | Connector model + corrected secret terminology | ADOPT (terminology) / DEFER (implementation) | The **terminology correction is the valuable half** and is adopted now. See §8 for the exact README/ARCH/26 wording to fix. Implementation is deferred **pending re-deciding `D1`**: the recorded direction is to resurrect [ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md) and wire it (D-021), so the gap is a missing prerequisite decision, not a rejected design. |
| 22 | Sandbox tiers 0–3 | ADOPT (tier declaration) / DEFER (tier 3) | LiteSPM is weaker than Docker here, and `ARCH/08` §3 already refuses OS-sandbox claims. Declaring the true tier is honest and cheap; tier 3 is a Phase-5 project. |
| 23 | Unified invocation engine | ADOPT | This is the runtime moat and it is the largest single build. `providers` is never populated (§2 of #2), so the router has nothing to route. |
| 24 | Restart/retry yes, blind semantic fallback no | ADOPT | The rejection is correct and should be recorded in `ARCH/15`. Automatically substituting a `send_email` for a failed `send_email`-alike is unacceptable. |
| 25 | Circuit breakers + health scoring | ADOPT | Depends on #23. Pairs naturally with the 6-state provider health model already implied by `ARCH/14`. |
| 26 | Bounded output protection, never pipe provider stderr to the model | ADOPT | Bounded ring buffer is already built (finding 118/E001). The missing half is the rule that stderr never reaches model context. |
| 27 | Tamper-evident invocation receipts | ADOPT | Genuinely differentiating. The fields map onto existing `planHash`, `schemaFingerprint`, and `capability_grants` (§2 of #2). |
| 28 | Enterprise policy hierarchy, tighten-only | ADOPT | `internal/policy/engine.go` today is `Evaluate(ctx, PolicyInput) PolicyDecision` — flat, with an injected `[]DenyRule` and no hierarchy or provenance. This is a real build. |
| 29 | `litespm policy explain` | ADOPT | Small, high value, and it is a *prerequisite* for #28 being usable. `PolicyDecision` needs a provenance field regardless. |
| 30 | `litespm audit --ci` incl. scratch replay | ADOPT | Replay is the single best idea in the whole proposal set. Requires #9 to have something to replay. |
| 31 | Private registry / federation | DEFER | Enterprise Phase 6. |
| 32 | Air-gapped signed bundle | DEFER | Enterprise Phase 6, and it is a legitimate differentiator worth stating in positioning once it exists. |
| 33 | Provenance: SLSA, Sigstore/cosign, SBOM, signed tags | ADOPT (releases) / DEFER (packages) | `SECURITY.md` already documents the unsigned-release gap. Signed *releases* are cheap and unblock `self-update`; signed *packages* is Phase 5. |
| 34 | TUF-style catalog signing | DEFER | Correct in principle. Depends on #30 and on the catalog existing at all (§4.2). |
| 35 | Revocation / quarantine | DEFER | Depends on signed advisories (#33). Must not silently remove user software. |
| 36 | Static security scanning, labelled *scan* not *verification* | ADOPT | `internal/doctor` already has a canary mechanism to build on. AgentMods' discipline (exact reason, explicit "this is a scan") is the part to copy. |
| 37 | Reject arbitrary lifecycle shell scripts | **REJECT** (the scripts) / ADOPT (declarative actions) | The repo already has the better invariant — install does not execute package code. Keep it. Declarative `postInstall: [verifyMCPHandshake, checkPort]` is the correct replacement. |
| 38 | No cross-agent shared memory in core | **REJECT** for core | Agreed. If built later it is a separate optional package; core must not depend on it. |
| 39 | Mock / record / replay | DEFER | High value for CI, but strictly downstream of #23. |
| 40 | Semantic update diffs | ADOPT | Genuinely better than semver, and mostly a rendering problem over data that will exist post-#23. |
| 41 | Compatibility evidence as first-class metadata | ADOPT | Directly fixes `ARCH/26` §4.2's `testedHosts` complaint. "Never infer compatibility from config-file location" is the right rule. |
| 42 | Sharded search index; SQLite FTS client-side | DEFER | Real at scale; 3.4 MB is not yet a measured failure. Should be an explicit, measured trigger, not a guess. |
| 43 | Catalog deltas | DEFER | Same trigger condition as #42. |
| 44 | Evidence-based ranking, no single magic score | ADOPT | Reinforces §12.4. Also the concrete home for #14's Evidence Card. |
| 45 | First-class TUI | DEFER | Phase 8, after the core works. Non-negotiable constraint: **zero TUI business logic** — every action must call the same daemon operation as the CLI. |
| 46 | Transaction / staging tray | ADOPT | This is a small change to the UI over an existing plan engine, with very high perceived polish. Phase 8. |
| 47 | Local dashboard, keep public site separate | DEFER | Phase 8. The hard rule (never send keystore data to the public site) is already `ARCH/05`. |
| 48 | Standardize the CLI vocabulary (~40 commands) | DEFER | Not a rewrite target. Add the missing *primitives* (`plan`, `apply`, `diff`, `why`, `policy`, `audit`) as the underlying features land. |
| 49 | Shell completion (bash/zsh/fish/PowerShell) | ADOPT | Cheap, disproportionate polish, no architectural dependency. |
| 50 | `litespm why` for packages and for owned config paths | ADOPT | Requires #9's ownership records. The second form (config-path provenance) is the more novel half. |
| 51 | `adopt` as the killer migration flow | ADOPT | The strongest onboarding play, and it is mostly a *read* path over configs LiteSPM already parses. High value per unit of work. |
| 52 | Competitor migration adapters | ADOPT (OpenAPM, skills-lock) / DEFER (rest) | Depends on #3's verification status. Nothing may be built for the two unverified products (§3.2). |
| 53 | Rename now, not later | **ALREADY DONE** | `LitePSM` → `LiteSPM` landed at `ff0a1db`; legacy root/env/host-key adoption is retained and covered by `internal/config/legacy_test.go` and `internal/host/legacy_adoption_test.go`. Residual: `m8` (module path casing vs git remote) — non-breaking, fix at release. |
| 54 | Add a LICENSE | **DONE** | `LICENSE` (Apache-2.0) and `NOTICE` exist at the repo root and are referenced from `README.md` §5/§8. The npm package carries them: `npm/package.json` `files` lists `LICENSE` and `NOTICE`, `.github/workflows/release.yml` stages them (`cp LICENSE NOTICE npm/`) before `npm publish --provenance`, and `npm pack --dry-run` lists `11.3kB LICENSE` + `2.0kB NOTICE` |
| 55 | The removal table | ADOPT (as §10) | Applied below. One row needed correction: "Another connector architecture — wire the existing one" (§3.3) — the deleted package cannot be wired; the adopted direction is [ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md)'s resurrect-and-wire under a re-decided `D1`. |
| 56 | The ten strategic ideas | ADOPT | Leases, compatibility evidence, identity graph, semantic diff, policy explain, receipts, three-way reconcile, context budget, cross-format interop, signed offline bundles. All are in the table above. |
| 57 | Target architecture diagram | ADAPT | Keep the plane decomposition. Do not publish the diagram as current state — it describes a destination. |
| 58 | Phase 0–9 ordering | ADOPT (reordered) | Adopted as §12, with Phase 1 **reordered** so the catalog is actually published and the install path actually completes before anything is layered on top. |
| 59 | Competitive end-state | DEFER (as a claim) | The positioning is right, but it may not be stated as present-tense. §13 governs. |
| 60 | Acceptance rule: a real workflow end-to-end, not "files exist" | **ADOPT — this is the most important item in the set** | It is precisely the rule this repository broke (§4.1, §4.3, §4.4). It is now §2 plus the per-phase gate in §12. |

**Tally:** 26 ADOPT · 2 ADAPT · 15 DEFER · 2 REJECT (as proposals) · 2 already DONE · 13 partial/split
across both columns.

---

## 6. ADD

New code the tree does not contain. Grouped by the defect it closes; each is independently shippable.

| Addition | Closes | Placement |
|---|---|---|
| Release-tree publisher: build the release from committed snapshots and publish `v1/releases/<id>/{manifest,listings,versions}.json` + a `current.json` whose fields match `CurrentPointer` exactly | §4.2, §4.3 | `internal/catalogbuild` (wiring) + CI |
| `_headers` for the documented cache policy | — | **Already done.** `scripts/deploy-pages.sh:31-42` generates it. Listed here only so §6 is not mistaken for a gap |
| `litespm.yml` (project manifest) + `litespm.lock` (deterministic lockfile) | #8 | new `internal/manifest`, `internal/lock` |
| `deployment_mutations` ledger (mutation_id, install_id, host_id, scope, file_path, locator, preimage_hash, postimage_hash, owner) + migration `002` | #9, #50, #51 | `internal/state/migrations/`, `internal/deploy` |
| Three-way reconcile (preimage / owned-write / current) with `Keep local` / `Use package` / `Detach` | #10 | `internal/deploy` |
| `PolicyDecision.provenance` + `litespm policy explain` | #29, #28 | `internal/policy` |
| `explain_policy` / `update_available` / `compatibility` Bridge tools; split the 12 Bridge tools into READ and MUTATING surfaces | #16 | `internal/bridge/shim.go` |
| Capability index / invocation registry + `invoke`/`get`/`cancel` implemented for real; provider rows persisted on install (m4) | #23, §2 of #2 | `internal/provider`, `internal/ipc` handler table |
| Provider health states (STARTING/READY/DEGRADED/UNHEALTHY/BACKOFF/STOPPED) + circuit breaker | #25 | `internal/provider` |
| Invocation receipt record (invocation_id, capability, package digest, schema fingerprint, provider instance, policy decision, approval id, outcome) | #27 | `internal/state` + schema |
| Static content scan (hidden Unicode, prompt-override patterns, `curl \| shell`, privilege escalation, encoded payloads, undeclared binaries) emitting `scan` verdicts with exact reasons and source lines | #36 | `internal/scan` |
| Canonical identity + alias edge set (same_project / claim / hash_match / declared / heuristic / fork / mirror) | #12, #13 | `internal/domain` + schema |
| Compatibility evidence records (host × OS × version × date × test) | #41 | `internal/catalogbuild` + schema |
| OpenAPM importer/exporter; `skills-lock.json` importer/exporter | #4, #17, #52 | `internal/interop` |
| Scratch-replay audit (`litespm audit --ci`) | #30 | `internal/audit` |
| Shell completion for bash/zsh/fish/PowerShell | #49 | `cmd/litespm` |
| Signed releases (cosign) + SBOM generation for the CLI binary | #33 | `.github/workflows/release.yml`, `internal/update` |

---

## 7. UPGRADE

Existing subsystems that should be strengthened rather than replaced. **No second implementation of
any of these is to be built** — the proposal set explicitly says so, and the tree agrees.

| Subsystem | Current state | Upgrade to |
|---|---|---|
| `internal/host/targets_data.go` + `ARCH/30` | 44 generic BridgeTargets, data-driven | Extend the same table with `skill` / `instruction` / `command` / `hook` component kinds so all 77 agents come from one registry (#18) |
| `internal/policy` | flat `Evaluate`, injected deny rules, no hierarchy, no provenance | Layered tighten-only inheritance with per-decision rule provenance (#28, #29) |
| `internal/catalog/search.go` | lexical tiers + a dormant verification bonus (`security_audited` +10, `signature_verified` +5) that no adapter can produce | Separate, individually sortable dimensions — relevance, publisher trust, compatibility, freshness, security findings, originality, reliability. Never one magic score (#44) |
| `internal/doctor` | diagnostics + `--repair`; already runs a synthetic secret canary round-trip | Add security-scan findings, deployment-ownership drift, and three-way conflict detection to the repair plan |
| `internal/skills` | atomic update + rollback + dry-run + provenance (finding 118) | Add lockfile import/export and a skill-level diff (#17) |
| `internal/provider` | start/stop/probe real; `invoke`/`get`/`cancel` return explicit `-32601` | Real dispatch, deadlines, cancellation, concurrency limits, backpressure (#23) |
| `internal/host` backups | `host_backups` = pre-edit file snapshots | Keep as a safety net, but make the deployment ledger authoritative for ownership (#9) |
| `cmd/litespm` | 13 top-level commands | Add `plan`, `apply`, `diff`, `why`, `policy`, `audit`, `adopt` as the features above land — not as a bulk rename (#48) |
| `ARCH/27` support matrix | 8 types × sources, with honest gaps | Re-baseline once the identity graph (#13) exists; it currently cannot express "same package, two sources" |

---

## 8. REFINE

Corrections to existing documents. Each is a factually wrong statement today, not a style preference.

| R1a | *(self-audit of this document)* | An earlier draft asserted that `ARCH/18` §4's `_headers` CDN policy was unimplemented, based on `find . -name '_headers'` returning nothing. The file is generated at deploy time into the gitignored `pages-dist/`, so the inference was wrong | Retracted in §4.4 and in R4. §4 is implemented and specified consistently. Lesson recorded: a negative filesystem result is not evidence of absence when the artifact is generated at build time |
| R1 | `README.md` §1 | **RESOLVED.** The "Phases A–I all `COMPLETED`" table is gone; §1 now uses the §2 evidence states and points at [STATUS.md](../STATUS.md) | Done — no action |
| R2 | `TODO.md` closing line | **RESOLVED.** The closing "All Phases Complete" line is gone; the ledger now states the four known broken/unwired capabilities and does not claim completion | Done — no action |
| R3 | `ARCH/18` §1, §2 | **RESOLVED.** The `index.json`, `shards/`, `items/`, `metadata.json` tree the compiler never emits is now labelled `DESIGNED` at the top of §1 and inline in §2; the emitted subset (`v1/current.json` + `v1/releases/<id>/{manifest,listings,versions}.json`) is stated as the client contract | Done — no action |
| R4 | `ARCH/18` §4 | **No defect.** The `_headers` CDN policy is implemented in `scripts/deploy-pages.sh:31-42` as a deploy-time build artifact under the gitignored `pages-dist/`. Recorded here because an earlier draft of this document wrongly flagged it — the retraction is in §4.4 | None; do not "fix" |
| R5 | `REMEDIATION-PLAN.md` | **RESOLVED.** "Where things stand" now cites `ff0a1db` and states the rename and phases 0–3 are committed | Done — no action |
| R6 | `REMEDIATION-PLAN.md` D4 | "Go `catalogbuild` is the single builder" is contradicted by `scripts/build_full_catalog.py` being the real producer (§4.3) | Amend D4 or delete the Python builder |
| R7 | `ARCH/00-INDEX.md` §2 | **RESOLVED.** `18` is registered `Normative for the client path + search contract; DESIGNED for the unbuilt shard tree`, `17` is status-qualified the same way, `16`/`19` are now scoped to the sections their code supports, `19`'s blurb names the real backends, the ADR range is `D-001 through D-025`, and `12` claims "22 tables" (correct) | Done — no action |
| R8 | `README.md` §5 | **RESOLVED, then extended.** The tree lists `LICENSE` and `NOTICE`, the CLI file listing matches `cmd/litespm` (including `skills_update.go`, `skills_policy.go`), and the ARCH count has since grown to `38 … (00–37)` with `32`–`37` and `scripts/README.md` / `web/README.md` registered | Done — no action |
| R9 | `ARCH/29` | **RESOLVED.** `ARCH/29` now carries the D1 status note: `internal/connector` was deleted for having zero production importers and no wiring path | Done — no action |
| R10 | Secret-exposure wording | The claim that provider processes never see credentials is not generically true — an MCP server launched with `GITHUB_TOKEN` receives it. `ARCH/05` and `README` must distinguish **brokered connector** (agent never holds the token) from **MCP server** (the server process may receive a scoped credential) | Reword both |
| R11 | `ARCH/26` §12.4 | Correct and load-bearing; `ARCH/18` §3.1 already complies. Extend the rule to cover *ranking* on compatibility or originality, which are equally unsourced today | Extend |
| R12 | `ARCH/08` §3 | Lists "no OS sandboxing claims" as a non-goal, but the runtime has no tier declaration at all — the absence is undocumented, not just the claim | Add the true tier to the docs |

---

## 9. OPTIMIZE

| Target | Current | Optimization |
|---|---|---|
| `web/data/catalog.json` | one ~3.4 MB blob bundled into the static export, hydrated into browser JS | Split into a small search-metadata index + lazy detail records, with a **measured** trigger for sharding (not a guess) (#42, #43) |
| Catalog sync | full release re-download per sequence bump | Delta releases with immutable full snapshots retained for recovery (#43) |
| Provider stderr | bounded ring buffer exists | Add rate limiting, ANSI stripping, binary detection, UTF-8 validation, secret redaction — and the hard rule that stderr never enters model context (#26) |
| Resolver | pure DFS, cycle detection | Keep the pure property. Add lockfile-driven resolution so a frozen install skips solving entirely (#8) |
| Doctor | per-category exit codes already correct (m7) | Add timing so `doctor` can report which check dominated |
| CLI output | ad-hoc | Stable universal flags: `--json --quiet --verbose --no-color --offline --frozen --dry-run --yes --scope --target --profile --timeout`, and a hard guarantee that `--json` stdout is never contaminated by progress text |

---

## 10. REMOVE

| Target | Action | Reason |
|---|---|---|
| "All phases complete" status language in `README.md` §1 and `TODO.md` | Remove | Contradicted by the tracker and by four reproducible defects |
| The production synthetic-install path | Remove from the normal user path | `cmd/litespm/main.go:872` fabricates a package. Keep it strictly as a test/`--dry-run` fixture and make the real path the default (D2) |
| `scripts/build_full_catalog.py` as a *second* builder | Remove once §4.3 is resolved | D4 names one builder. Two is not a migration, it is drift |
| Any plan to restore the deleted `internal/connector` package as-is, or any claim that a connector package ships today | Remove | `D1` deleted it (D-021). The adopted direction is [ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md)'s resurrect-and-wire, gated on re-deciding `D1` — not a restoration of dead code, and not a present-tense capability claim (#21) |
| Unstructured backup/restore as the ownership model | Replace with the deployment ledger | A backup cannot tell you who owns which node (#9) |
| "Verified" derived from a static scan | Remove the wording | `ARCH/26` §12.4 and AgentMods' own discipline both require *scan*, not *verification* |
| "Verified by LiteSPM Security Guild" | Remove outright | No such body, certification, or process exists. This would be a fabricated endorsement |
| Automatic semantic fallback between tools | Reject | `write_database()` must never silently become another provider's `write_database()` unless an explicit equivalence declaration exists (#24) |
| Cross-agent shared memory in core | Reject for core | Scope discipline (#38) |
| Claims that dynamic MCP provisioning is unique | Remove | Docker ships it (§3.3) |
| Any built on "Baton" or "Agentry" as described | Remove | Unverifiable (§3.2) |

---

## 11. CREATE NEW

| Artifact | Purpose |
|---|---|
| `ARCH/32-MANIFEST-LOCK-INTEROP.md` | `litespm.yml` / `litespm.lock` schemas, frozen installs, SBOM export, OpenAPM + skills-lock interop |
| `ARCH/33-DEPLOYMENT-LEDGER-RECONCILIATION.md` | Ledger schema, locator grammar, three-way merge semantics, conflict UX |
| `ARCH/34-RUNTIME-INVOCATION-RECEIPTS.md` | Capability registry, invocation engine, health/circuit-breaker states, receipt format |
| `ARCH/31` (this document) | Competitive record and ordered backlog |
| `.github/DEPENDENCY-LICENSE-INVENTORY.md` | Third-party license inventory for releases, alongside the new `NOTICE` |
| A published `/v1/releases/<id>/` tree | The single artifact whose absence currently makes §4.2 unfixable from the client side |

---

## 12. Ordered backlog

Reordered from the source proposal's Phase 0–9 with one change: **Phase 1 must publish the catalog and
complete the install path before anything is layered on it.** Every phase carries one gate, and the
gate is always a real user workflow — never "files exist", never "tests exist" (#60, §2).

| Phase | Objective | Gate (a real workflow must pass) |
|---|---|---|
| **0 — Truth** | Close §4.1–4.4; apply §8 (R1–R12); land `LICENSE`/`NOTICE`; reconcile D4 | `litespm catalog sync` succeeds against the live origin (**done 2026-10-05**); an agent's `/marketplace` install completes (**open**); no document claims a state its code lacks |
| **1 — Reproducibility** | `litespm.yml` + `litespm.lock`; frozen install; deployment ledger; three-way reconcile | `litespm install --frozen` reproduces byte-identical CAS trees on a clean machine; a hand-edited owned config node produces a conflict, not a silent overwrite |
| **2 — Runtime** | Capability registry; real `invoke`/`get`/`cancel`; deadlines, cancellation, concurrency; provider rows persisted; health + circuit breaker | A tool called through the Bridge reaches a provider, respects a deadline, and returns a receipt — with the `providers` table populated by non-test code (closes m4) |
| **3 — Evidence** | Static scan; canonical identity graph; compatibility evidence; Evidence Card; `policy explain` | `litespm search postgres` shows one canonical entry with its sources and evidence states, and every deny names the rule that caused it |
| **4 — Interop** | OpenAPM import/export; skills-lock import/export; plugin import/export; private registry federation | `litespm import apm.yml` and `litespm import skills-lock.json` both produce a previewable diff before any write |
| **5 — Security** | Signed releases; SBOM; sandbox tier declaration; egress policy; advisories + quarantine | `self-update` verifies a cosign signature; `doctor` reports the true isolation tier rather than a claim |
| **6 — Enterprise** | Policy hierarchy (tighten-only); `audit --ci` with scratch replay; SARIF; air-gapped signed bundle | A CI run fails on a policy violation with the inherited rule chain, and on a hand-edit detected by replay |
| **7 — Runtime governance** | Semantic update diffs; context/token budgeting; capability leases | An update shows schema, permission, signer and token deltas before applying; a lease expires and the capability disappears |
| **8 — UX** | TUI (zero business logic — daemon calls only); staging tray; `adopt`; completion; local dashboard | Every TUI action is reachable via the CLI and produces identical daemon calls; a staged tray of 4 operations applies as one plan with one rollback |
| **9 — Advanced** | Record/replay; explicit fallback groups; optional memory package | A recorded trace replays in CI with no secrets present |

**Re-audit after every phase**, per the [REMEDIATION-PLAN](../REMEDIATION-PLAN.md) protocol:
implementer runs the toolchain, an independent falsifier attacks each claim, status flips to
`VERIFIED` only via the falsifier.

---

## 13. Forbidden claims (binding)

Until the corresponding phase gate passes, the following may not appear in `README.md`, the website,
release notes, or any document — in any wording that implies present capability:

1. Any "verified", "audited", or "certified" badge not backed by a real, attributable check.
   A static scan is a **scan**. `Popularity`, `stars`, or a security score with no source renders as
   `Not published` (`ARCH/26` §12.4).
2. Any popularity, install-count, or usage number. The dataset has none; the builder hard-codes
   `stars: null` on all rows.
3. Any sandbox or isolation claim above the tier actually implemented and reported by `doctor`.
4. Any implication that arbitrary MCP servers cannot receive credentials (see R10).
5. Any "unique" or "only" competitive claim. Three of the capabilities in the source analysis that
   were presented as novel are already shipped by Docker, Microsoft, and Vercel (§3.1, §3.3).
6. Any feature marked `DESIGNED`, `IMPLEMENTED`, or `WIRED` in this repository's own documents being
   described to users as shipped.

**The rule of thumb:** if the sentence could not be substantiated by pointing at a file, a test, or a
live origin, it does not ship.
