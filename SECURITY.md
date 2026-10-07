# Security Policy

## Reporting a vulnerability

Open a private security advisory at
<https://github.com/sarv-projects/LiteSPM/security/advisories/new>, or email the
maintainer listed on the GitHub profile.

Please include: the version (`litespm version`), your OS, the exact steps, and
the impact you believe it has. You do not need a working exploit.

We will acknowledge within 7 days. There is no bug bounty. Credit is given in
the advisory unless you ask otherwise.

**Please do not open a public issue for a vulnerability.** LiteSPM edits the
configuration files of other tools and brokers credentials, so a public report
is a working exploit for everyone until a fix ships.

## Supported versions

Only the latest published release receives security fixes. There are no
long-term-support branches.

## What LiteSPM is trusted to do

This is a small tool asking for a large amount of trust, so the boundaries are
stated plainly.

| Surface | What it can do | What limits it |
|---|---|---|
| **Host config adapters** | Write one `litespm` entry into an agent's config file | The file is backed up first; only the `litespm` member is touched; comments, key order and formatting elsewhere survive byte-for-byte; the result is re-parsed and asserted before anything is written; every write has an inverse (`litespm host remove`) |
| **Bridge shim** | Proxy MCP traffic between an agent and the daemon over stdio | It holds no credentials and makes no network calls of its own |
| **Daemon** | Supervise provider processes, enforce policy, broker secrets | Single writer to its own database; secrets go through the OS vault (Keychain / DPAPI / Secret Service), never to a config file |
| **Skill installer** | Copy a `SKILL.md` directory into an agent's skills tree | Refuses symlinks, non-https sources and overwrites; caps a skill at 32 MiB |
| **Self-update** | Replace its own binary | Fails closed on a missing checksum (see below) |
| **Connector executor** (design only) | Not implemented: `internal/connector` was deleted under decision D1 for having zero production importers, and no connector executor ships | The credential-custody design — the agent supplies a connection handle rather than a secret, the target host is pinned by the connector manifest, private/loopback/link-local addresses are refused after DNS resolution, and caller-supplied `Authorization`, `Cookie` and `Proxy-Authorization` headers are stripped — is recorded in [29 — Connector System Design](ARCH/29-CONNECTOR-SYSTEM-DESIGN.md) and carries no shipped guarantee |

## Supply chain

**Releases are integrity-checked, not signature-verified.** Each release
publishes `SHA256SUMS.txt` alongside the binaries. The updater verifies the
downloaded binary against that file and refuses to proceed on a mismatch. The
npm installer is **fail-closed on every path**: a missing `SHA256SUMS.txt`
manifest, a missing entry for your platform, an unreadable manifest, or a
digest mismatch all discard the download, print `INSTALL REFUSED`, and exit
non-zero — nothing unverified is installed or executed
(`npm/scripts/install-binary.js`, `verifyDownloadedBinary`). An earlier build
warned and proceeded unverified when the manifest had no entry for the platform;
that was removed.

The honest limit: those checksums travel in the same release as the binaries, so
they prove the download was not corrupted in transit. They do **not** prove the
release itself is authentic, because anyone able to publish to the release
channel could publish matching checksums. Signed releases are the missing piece
and are not implemented. Until then, the practical mitigation is to build from
source (`go build ./cmd/litespm`) or pin a commit.

The self-update path fails closed: if the release manifest publishes no checksum
for your platform, the update is refused rather than applied unverified. This
was not always true — an earlier build skipped verification when the checksum
was absent and carried a hard-coded bypass string. Both were removed.

`npm install -g litespm` runs a `postinstall` script that downloads the platform
binary from the GitHub release and verifies it against `SHA256SUMS.txt`; the
install fails closed (non-zero exit) when that proof cannot be produced. Read
`npm/scripts/install-binary.js` before installing if you want to see exactly
what it fetches.

## What we do not claim

These distinctions matter and are enforced in the product, not just documented:

- **"Verified" is not our verdict.** A `verified` badge on a publisher means the
  upstream registry flagged that publisher. LiteSPM has not audited it. The UI
  says so wherever the badge appears.
- **No popularity or usage data is published.** The catalog carries no star,
  download or install figures, because the upstream sources do not expose them.
  An earlier build estimated them; that was wrong and was removed.
- **No compatibility claim is a test result.** A capability's host list is
  publisher-declared or derived from what the host can technically install. It is
  not the output of running the capability against every host.
- **No risk verdict is invented.** When there is no audit data for a capability,
  the UI says `unverified`. It never fabricates a "safe" verdict.

## Untrusted input

The following are treated as untrusted data and are never followed as
instructions: catalog entries, upstream manifests, repository READMEs, MCP tool
descriptions and results, and skill contents. A
`SKILL.md` is instructions an agent will follow — installing one is a prompt
surface, which is why the installer shows you the source and refuses
non-https origins.

## Out of scope

- Vulnerabilities in the agents LiteSPM configures (report those upstream).
- Vulnerabilities in MCP servers or skills listed in the catalog. The catalog is
  an index; LiteSPM is not the publisher of those entries.
- Anything requiring an attacker who already has write access to your user
  account or your vault.
- The absence of signed releases, which is a known and documented gap rather
  than an undisclosed vulnerability.
