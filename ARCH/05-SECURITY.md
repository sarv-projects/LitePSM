# Security, Trust, and Credentials

## 1. Core Security Invariant

LiteSPM's hosted catalog service is strictly a discovery index. It is **never** a secret store, execution environment, MCP proxy, or authentication authority. Downstream service credentials and runtime tool execution remain strictly on the user's local machine or with the user's chosen remote provider.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        Hosted Catalog (Untrusted)                      │
│                                                                        │
│   Public JSON · Static Schemas · Publisher Manifests · Search Shards   │
│   (Zero credentials · Zero execution · Zero tool proxying)             │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ HTTPS (Read-Only)
════════════════════════════════════╪═════════════════════════════════════
                         PRIVILEGE BOUNDARY (Local Machine)
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        LiteSPM Local Control Plane                     │
│                                                                        │
│   Daemon owns:                                                         │
│     ├── Operating System Secret Store (DPAPI / Keychain / Libsecret)   │
│     ├── Process Supervision & Resource Limits (Job Objects / cgroups)  │
│     ├── Capability Schema-Drift Invalidation & Effect Policy           │
│     └── Content-Addressed Local Store & Operation Journal              │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Artifact Extraction & Filesystem Safety

Third-party packages (skills, MCP distribution archives, plugins) are untrusted. Artifact extraction is centralized in `internal/artifact` and enforced against strict safety limits:

### 2.1 Extraction Safety Thresholds
*   **Max Download Archive Size:** 256 MiB.
*   **Max Extracted Tree Size:** 1 GiB.
*   **Max Total Extracted Files:** 20,000 files.
*   **Max Single File Size:** 128 MiB.
*   **Max Normalized Path Length:** 1,024 bytes.
*   **Max Compression Ratio:** 100:1 (Zip-bomb defense) — planned; `internal/artifact/extractor.go` currently enforces archive/tree/file-count/single-file/path caps but no explicit ratio check. Treat size caps as the enforced mitigation until the ratio check lands.
*   **Max HTTP Redirects:** 3 hops across identical origin; cross-origin redirects require explicit re-authorization.

### 2.2 Prohibited Archive Artifacts
The extraction engine rejects the entire archive if any of the following are detected:
1.  **Directory Traversal:** Paths containing `..`, un-normalized slashes, or trailing dots.
2.  **Absolute & Escape Paths:** Paths starting with `/`, `\`, Windows drive letters (`C:`), or UNC paths (`\\server\share`).
3.  **Special File Types:** Symlinks, hardlinks, named pipes (FIFOs), sockets, and device nodes.
4.  **Case Collisions:** Multiple files whose paths collide on case-insensitive filesystems (e.g., `README.md` and `readme.md`).
5.  **Digest Discrepancies:** Extracted content whose computed SHA-256 tree digest does not match the manifest declaration.

---

## 3. Network Fetch Policy & SSRF Prevention

### 3.1 Public Ingestion Restrictions
When the catalog builder or client queries public URLs:
*   **HTTPS Enforced:** Only `https://` schemes are permitted. `http://`, `file://`, `data:`, `gopher:`, or custom schemes are rejected.
*   **Private Network Blacklist:** Outbound HTTP clients perform DNS resolution and verify that resolved IP addresses do not fall into private, loopback, or cloud-metadata ranges:
    *   `127.0.0.0/8`, `::1` (Loopback)
    *   `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (Private RFC 1918)
    *   `169.254.0.0/16`, `fe80::/10` (Link-Local & Cloud Metadata endpoints such as AWS/GCP `169.254.169.254`)
    *   `fc00::/7` (IPv6 Unique Local)

### 3.2 User-Configured Local Sources
Users may explicitly configure local filesystem paths or private network Git repositories. However, **public catalog records can never redirect a client to a private network destination**.

---

## 4. Nested MCP Capability Clamping

When the LiteSPM Daemon acts as an MCP client connecting to downstream local or remote MCP servers, it deliberately **clamps** its advertised client capabilities to protect user privacy:
1.  **Roots Forwarding Disabled:** The daemon **does not** advertise filesystem roots to downstream providers by default. A provider cannot query the user's workspace structure.
2.  **Model Sampling Disabled:** Downstream providers are barred from requesting LLM sampling through LiteSPM.
3.  **Elicitation Clamping:** Downstream providers cannot initiate arbitrary user prompts unless the host agent explicitly supports modern form elicitation and local policy allows it.
4.  **Stderr Redaction:** Provider stderr streams are captured in a bounded (1 MiB), rotating memory buffer and stripped of sensitive patterns (e.g., tokens, authorization headers) before diagnostic logging.

---

## 5. Capability Schema-Drift Defense

Downstream MCP providers may update their tool definitions dynamically between restarts. An attacker or modified server could alter an innocuous tool (`view_file`) into an effectful action (`overwrite_file`).

LiteSPM prevents this via cryptographic schema fingerprinting:
1.  **Schema Fingerprint:** Upon tool discovery, LiteSPM generates a canonical SHA-256 digest of the tool's input JSON Schema:
    $$\text{schemaFingerprint} = \text{SHA-256}(\text{CanonicalizeJSON}(\text{ToolInputSchema}))$$
2.  **Cryptographic Grant Binding:** User capability approvals are bound to strong identity tuples:
    *   **Local Stdio Providers:** Stored as `(capability_id, schemaFingerprint, casTreeDigest)`. Both the tool input schema and the underlying unpacked disk tree are cryptographically bound.
    *   **Remote HTTP Providers:** Stored as `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)`. The remote HTTPS origin and server version digest are bound.
3.  **Drift Invalidation:** When a provider connects or is invoked:
    *   If `tool.InputSchema` diverges from stored fingerprint: invalidated with `LPSM-PROVIDER-SCHEMA-DRIFT`.
    *   If local files change: invalidated with `LPSM-PROVIDER-CODE-DRIFT`.
    *   If remote origin or version changes: invalidated with `LPSM-PROVIDER-ENDPOINT-DRIFT`.
    *   All drifted capabilities are blocked from execution until fresh user approval is granted.

---

## 5.1 Effect Provenance & Unknown Tools
LiteSPM tags every declared effect with an explicit classification provenance:
*   `publisher_declared`: Unverified claims from the package author's manifest.
*   `curated`: Reviewed and certified by catalog maintainers.
*   `runtime_observed`: Dynamically observed in automated sandbox test runs.
*   `user_classified`: Explicitly configured by the user in local policy.

**Unknown Tools:** LiteSPM **never** infers side effects by guessing from tool parameter names. Any tool with undeclared or unknown effects is treated as an atomic capability invocation that fails closed and requires explicit interactive user authorization.

---

## 6. Command Marketplace Source Prohibition

Claude Code and Grok Build marketplace manifests permit sources that execute local shell commands to generate plugin files.
*   **V1 Rule:** LiteSPM strictly **rejects** marketplace sources of type `command`.
*   **Rationale:** Ingesting a catalog listing must never execute arbitrary publisher-controlled code. Listings utilizing `command` sources are marked `supportedByLiteSPM: "no"` with the explanation `UNSUPPORTED_COMMAND_SOURCE`.

---

## 7. Local Daemon IPC Permissions

Communication between Bridge Shims, the CLI, and the local Daemon is secured against unauthorized local processes:
*   **Windows Security Descriptors:** The daemon's Named Pipe (`\\.\pipe\litespm-daemon-<user-hash>`) is initialized with a Discretionary Access Control List (DACL) that grants access exclusively to the owning user's Security Identifier (`SDDL: D:(A;;GA;;;OW)`). Administrators and other users are denied access.
*   **Unix / macOS Domain Sockets:** Sockets are created inside `$XDG_RUNTIME_DIR/litespm/` with directory permissions `0700` and socket file permissions `0600`.

---

## 8. Operating System Secret Storage

LiteSPM never stores plaintext credentials in SQLite, JSON files, environment variables, or log streams.
*   **Supported Platforms:**
    *   **Windows:** Windows Credential Manager (`wincred.dll`) or DPAPI encrypted local blobs.
    *   **macOS:** Apple Keychain Services API (`SecItemAdd`, `SecItemCopyMatching`).
    *   **Linux:** FreeDesktop Secret Service specification via D-Bus (`libsecret`).
*   **Database References:** SQLite stores only opaque string handles (`SecretRef: "secret:mcp:provider-1/api-key"`). Secret bytes are retrieved in memory only at the precise moment of provider launch or HTTP request signing.

---

## 9. Honest Security Claims

1.  **Not an OS Sandbox:** LiteSPM provides protocol mediation, schema validation, and authorization gating. Running a local stdio MCP provider executes real code on the host machine under the user's OS privileges.
2.  **Credentials Stay Client-Side:** LiteSPM hosted servers do not receive user credentials. However, when an agent invokes a downstream provider, that specific provider receives the authorized token necessary to fulfill the request.
3.  **Untrusted Skill Text:** Installing a skill does not run code, but skill Markdown instructions can influence model behavior (prompt injection). Skill text is presented to models as untrusted data with clear provenance metadata.
