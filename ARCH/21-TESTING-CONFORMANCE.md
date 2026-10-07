# Testing & Conformance Architecture

## 1. Testing Pyramid

LiteSPM's reliability is enforced through a multi-tier testing strategy:

```text
               ┌───────────────────────────────┐
               │    End-to-End & Conformance    │  (test/conformance_test.go)
               ├───────────────────────────────┤
               │  Crash-Injection Harness       │  (DESIGNED — no harness in tree)
               ├───────────────────────────────┤
               │   Property & Fuzzing Suites   │  (Malicious archives, parser fuzzing)
               ├───────────────────────────────┤
               │    Unit & Domain Invariants   │  (100% pure domain coverage)
               └───────────────────────────────┘
```

---

## 2. Property-Based Testing

Using automated property-testing generators:
1.  **Path Traversal Invariant:** For any arbitrary string generated as an archive entry name, `ExtractArchiveSafely` must either reject the path or ensure the resulting destination starts with the normalized staging root prefix.
2.  **Canonical Hashing Invariant:** Serializing an `InstallPlan` or `ToolInputSchema` must yield identical `planHash` and `schemaFingerprint` bytes regardless of map key iteration order or Go struct memory layout.
3.  **Host Config Preservation:** Round-tripping any third-party JSON/TOML configuration file through `PlanSetup` and `ApplySetup` must leave 100% of non-LiteSPM keys, values, and comments intact.

---

## 3. Hostile Archive Cases (generated in-code — no `fixtures/hostile/` directory)

Automated CI runs extraction against malicious archive payloads constructed in-test (`test/fuzz_hostile_archive_test.go`, `internal/artifact/artifact_test.go`):
*   Zip-slip traversal (`../../../../tmp/evil.sh`) → rejected with `LPSM-CAS-ARCHIVE-SLIP`.
*   Windows drive-letter escapes (`C:\Windows\System32\evil.dll`, UNC, `:` identifiers) → rejected.
*   Symlinks, hardlinks, FIFOs, sockets, device nodes → rejected.
*   Oversize payloads (256 MiB archive / 1 GiB tree / 20k files / 128 MiB single-file caps) → rejected before disk allocation.
*   Case-fold collisions (`File.txt` vs `file.txt`) → rejected.

---

## 4. Crash-Injection Fault Matrix (`DESIGNED` — no harness in tree)

**The truth:** there is **no crash-injection harness** in this repository, and there never was.
The recovery *logic* exists (`DB.RecoverIncompleteOperations` in `internal/state/operations.go`) and
is exercised by ordinary Go tests in `test/conformance_test.go` (which restart the daemon against a
seeded database rather than killing a live process). A kill-at-each-checkpoint harness with
`CP-01`…`CP-11` markers does **not** exist. If `TODO.md` or any other document claims otherwise, this
paragraph is correct and the claim is wrong. The matrix below is the acceptance target for the
harness that must be built:

| Injection Point | Operation State | Verification on Daemon Restart |
|---|---|---|
| **CP-01** | `created` | Operation cleaned up; no filesystem traces. |
| **CP-02** | `fetching` | Partial download removed from staging. |
| **CP-03** | `verified` | Unextracted archive preserved; staging cleaned. |
| **CP-04** | `staging` | Incomplete staging directory deleted; rolled back. |
| **CP-05** | `commit_intent` (before rename) | Target CAS tree absent; rolled back cleanly. |
| **CP-06** | `commit_intent` (after rename) | Target CAS tree present; SQLite transaction completed forward. |
| **CP-07** | `committing` (in-tx) | SQLite rollback handled by WAL engine; state reverted. |
| **CP-08** | `committed` | Clean startup; operation recognized as complete. |
| **CP-09** | `rolling_back` | Incomplete rollback finalized to `rolled_back`. |
| **CP-10** | Host setup (temp write) | Target config intact; temp file deleted. |
| **CP-11** | Host setup (post rename) | Registration recorded; backup preserved. |

---

## 5. MCP Conformance Test Matrix (`DESIGNED` — not executed)

`internal/mcpclient` is `WIRED` through `internal/discover`, which dials a real installed server,
probes its tools and calls one — so a live MCP exchange **is** exercised end to end
([STATUS.md](../STATUS.md) §4). The matrix below is the **additional** intended coverage
(negotiation, HTTP headers, cancellation, elicitation) that no test runs today.
*   **Version Negotiation:** Tests client behavior when server advertises modern `2026-07-28` vs. legacy `2025-11-25`.
*   **Header Mirroring:** Verifies that HTTP POST requests mirror `Mcp-Method` headers.
*   **Cancellation:** Verifies that sending `$/cancelRequest` cleanly terminates child process computation.
*   **Elicitation Fallback:** Verifies that when an MCP server requests form elicitation on an unsupported host, LiteSPM cleanly falls back to the CLI command prompt.

---

## 6. Secret-Leak Canary Scans

Continuous integration executes automated regex scans on:
*   SQLite raw database files.
*   Application log files and terminal stdout/stderr captures.
*   Crash dump logs and audit export files.
*   Generated `InstallPlan` JSON outputs.

If a synthetic canary token (`CANARY_SECRET_01J9X...`) appears anywhere outside the designated in-memory secret vault mock, the CI test run immediately fails.
