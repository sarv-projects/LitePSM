# Security, trust, and credentials

## Security invariant

LitePSM's hosted catalog is not a secret store, execution host, MCP gateway, or authorization authority. It must not receive a user's downstream service credentials or proxy tool calls. It returns public package/discovery data. A separately installed local LitePSM Bridge may mediate calls on the user's device; that local runtime is a privileged boundary and must enforce local policy. The local client owns store/config writes and refers to credentials stored locally.

## Credential classes

1. **LitePSM service identity** (future only): may authenticate access to a private catalog or publisher account. It is not a Git credential or provider credential and must not be sent to an MCP server.
2. **Upstream Git credentials:** use the user's installed Git credential helper/SSH agent for private sources. LitePSM does not upload credentials to its catalog API.
3. **Service credentials/OAuth tokens:** live in the machine's OS secret store (Windows Credential Manager/DPAPI-backed vault, macOS Keychain, or Linux Secret Service/libsecret). Local config contains a secret handle/reference where supported.
4. **OAuth for remote MCP:** initiate the provider's authorization flow from the local client/Bridge when the protocol supports a safe user flow. Store refresh/access tokens locally. The remote MCP/provider necessarily receives authorization when the user invokes it; LitePSM's hosted catalog does not.

If an MCP server only works through its vendor-hosted gateway and requires server-side credential custody, mark that mode as provider-hosted and incompatible with LitePSM's strict local-secret mode unless the user explicitly chooses it outside LitePSM. Do not represent “per-user credentials” as “client-side credentials.”

## Secret handling rules

- Never put secret values in catalog records, package manifests, command arguments, shell history, URLs, logs, crash reports, analytics, lockfiles, or Git-tracked client config.
- The CLI asks for secrets through a protected prompt or opens the provider's browser authorization flow. Do not echo secrets to the terminal.
- Prefer secret-store handles or short-lived pipes/file descriptors when launching local servers. Environment variables may be required by an upstream server; treat process-list, child-process, crash-dump, and debug-log exposure as residual risks and disclose the local process boundary.
- Never send secrets to the static site, catalog API, discovery MCP, telemetry, or build pipeline.
- No usage analytics at launch. If later added, opt-in, privacy reviewed, and must not contain query terms, installed package identity, repo path, IP-derived user identity, or credential metadata without explicit policy.

## Installation and execution are separate

```text
fetch metadata/artifact ≠ install files ≠ configure client ≠ start process ≠ invoke tool
```

- Downloading a package does not run its scripts.
- Installing a skill does not execute helper scripts; skill text remains untrusted instructions.
- Adding an MCP provider to the LitePSM local store does not start it without the user's approval. One-time agent setup configures only the LitePSM Bridge; provider enablement remains a separate decision.
- Starting a local MCP is code execution under the user's account. Show executable, arguments, source, version/digest, environment variable names, network access, and filesystem access before first start.
- Hooks, lifecycle code, and arbitrary plugin scripts are high-risk and must have a separate trust and permission step. They remain disabled by default.
- Tool permission prompts remain owned by the selected agent/host where provider tools are exposed natively. When calls pass through the local Bridge, the Bridge must independently enforce configured action policy and a reliable approval flow; it must fail closed for operations it cannot authorize safely. LitePSM does not silently approve tool actions.

## Supply-chain/provenance checks

- Resolve mutable refs to immutable versions/digests before a reproducible install.
- Verify artifact digest after download; reject mismatches and path traversal, archive bombs, symlinks escaping the install root, duplicate normalized paths, and unsupported file types.
- Preserve source, publisher claim, manifest format, upstream identifiers, and version; distinguish claims from proof.
- Verify signatures where available; absence of a signature is shown as “unsigned/unverified,” not silently treated as safe.
- Compatibility tests use isolated fixtures and read-only initialization/listing operations. Never call real write/action tools as a catalog health check.
- Keep “publisher verified,” “source reachable,” “format parsed,” “host tested,” and “security scanned” as distinct facts with timestamps/evidence.

## Local config safety

- Host adapters read and merge exact documented formats and manage one Bridge registration per supported host; they must not rewrite unrelated settings. Extension installs normally modify only LitePSM's managed store/config.
- Before mutation, generate a plan showing files, config keys, package versions, and secret handles affected.
- Back up an existing file, write to a same-filesystem temp file, validate syntax, then atomically replace.
- Refuse destructive edits or ambiguous ownership. Removal deletes only entries/files LitePSM can prove it owns.
- Private source installation uses local Git credentials without copying them into LitePSM's process state or service.

## Service threat model

The public web/catalog service receives ordinary HTTP requests and may receive IP/standard edge logs from its hosting provider. It serves untrusted publisher-controlled descriptions/manifests. It does not receive credentials for installed integrations or tool results. The site must escape rendered metadata and avoid rendering active HTML from publishers.

If dynamic accounts/publishing are added, require a separate threat model covering account takeover, package replacement, publisher verification, moderation, API abuse, personal data, retention, backups, and signing-key rotation before implementation.

## Honest security claim

“Credentials stay client-side” means LitePSM servers do not store or proxy them. It does not mean a remote MCP/provider cannot receive a token or the user's authorized request. It also does not make downloaded plugin code or skills trustworthy. These limits must be visible in the product and docs.
