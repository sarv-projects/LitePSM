# Dependency License Inventory

Purpose: record the license and source of every dependency that ships in or is
linked by a LiteSPM release artifact, so packaging and redistribution comply
with each upstream license.

- **Scope:** direct modules in `go.mod` (statically linked into the Go binary)
  and any runtime dependencies of the npm launcher (`npm/package.json`).
- **Verification method:** each license below was read from the corresponding
  module's own `LICENSE` file resolved by `go mod download` (module cache), not
  inferred from package metadata. Versions are the resolved `go.mod` versions.
- **Rule:** if a license cannot be confirmed from the upstream text, it is
  recorded as `UNKNOWN — verify before release`, never estimated.

## Go modules (linked into the `litespm` binary)

| Module | Version | License | Source URL | Verified |
| --- | --- | --- | --- | --- |
| github.com/Microsoft/go-winio | v0.6.2 | MIT | https://github.com/microsoft/go-winio | Yes — `LICENSE` ("The MIT License (MIT)") |
| github.com/dustin/go-humanize | v1.0.1 | MIT | https://github.com/dustin/go-humanize | Yes — `LICENSE` (MIT-style grant, no endorsement clause) |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | https://github.com/google/uuid | Yes — `LICENSE` (3-clause) |
| github.com/mattn/go-isatty | v0.0.24 | MIT | https://github.com/mattn/go-isatty | Yes — `LICENSE` ("MIT License (Expat)") |
| github.com/ncruces/go-strftime | v1.0.0 | MIT | https://github.com/ncruces/go-strftime | Yes — `LICENSE` ("MIT License") |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | https://github.com/remyoudompheng/bigfft | Yes — `LICENSE` (3-clause) |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause | https://cs.opensource.google/go/x/sys | Yes — `LICENSE` (3-clause) |
| modernc.org/libc | v1.77.1 | BSD-3-Clause | https://gitlab.com/cznic/libc | Yes — `LICENSE` (3-clause) |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause | https://gitlab.com/cznic/mathutil | Yes — `LICENSE` (3-clause) |
| modernc.org/memory | v1.12.1 | BSD-3-Clause | https://gitlab.com/cznic/memory | Yes — `LICENSE` (3-clause) |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause | https://gitlab.com/cznic/sqlite | Yes — `LICENSE` (3-clause) |

Every module in `go.mod` is marked `// indirect`; they are nonetheless linked
into the binary through the SQLite driver and terminal/platform helpers, so they
require attribution.

### Transitive / bundled third-party notices

Some modules carry their own flattened third-party notice files. These are not
direct `go.mod` entries, but redistribution of the compiled binary inherits
them and they must be reviewed alongside the source release:

- `modernc.org/libc` ships `LICENSE-3RD-PARTY.md` (includes code from the Go
  project and others).
- `modernc.org/sqlite` ships `LICENSE-3RD-PARTY.md`, `LICENSE-SQLITE`, and
  `LICENSE-SQLITE_VEC` (transpiled SQLite and `sqlite-vec`).
- `modernc.org/memory` ships `LICENSE-GO`, `LICENSE-LOGO`, and
  `LICENSE-MMAP-GO`.

These texts ship with the corresponding module sources and were not copied into
this repository. Before shipping a source distribution that bundles those
modules, re-verify that the notices are preserved per their terms.

## npm package (`npm/package.json`)

| Dependency | Version | License | Source URL | Verified |
| --- | --- | --- | --- | --- |
| _(none)_ | — | — | — | The package declares no `dependencies`, `devDependencies`, `peerDependencies`, or `optionalDependencies`. It uses only Node.js built-in modules (`assert`, `child_process`, `crypto`, `fs`, `https`, `os`, `path`). |

## Unresolved

None. All direct dependencies were verified from their upstream license text at
the versions listed above. Any future dependency whose license cannot be
confirmed must be added here as `UNKNOWN — verify before release`.
