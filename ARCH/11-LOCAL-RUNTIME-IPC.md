# Local Runtime & IPC Architecture

Transcribed from the tree at `ff0a1db`. State labels use the vocabulary of
[`STATUS.md`](../STATUS.md); where a behaviour below is specified but absent from
the code, it is marked `DESIGNED` rather than described as running.

## 1. Daemon Process Lifecycle

The LiteSPM Daemon is a single-instance background worker per operating system
user account (`litespm daemon serve`). It manages state mutations, provider
processes, and security policy evaluation. **It is started by an operator, not by
a client**: no code path spawns it (§1.2).

```text
┌────────────────────────────────────────────────────────────────────────────┐
│                        Daemon Lifecycle States                             │
│                                                                            │
│   [Not Running]                                                            │
│         │  operator: `litespm daemon serve`   (no auto-boot — §1.2)        │
│         ▼                                                                  │
│   [Initializing] ──> Open secret store -> Acquire DATA_ROOT/daemon.lock    │
│                      -> Open SQLite (run migrations) -> Startup recovery   │
│                      -> Start configured providers -> Bind IPC endpoint    │
│         │            (any failure here exits non-zero before serving)      │
│         ▼                                                                  │
│   [Listening]    ──> Serve Named Pipe / Domain Socket JSON-RPC             │
│         │                                                                  │
│         ▼ (SIGINT / SIGTERM — cmd/litespm/main.go:1211-1232)               │
│   [Shutting Down]──> server.Stop -> close listener -> StopAll providers    │
│                      -> close DB -> release daemon.lock                    │
│         │                                                                  │
│         ▼                                                                  │
│   [Terminated]                                                             │
└────────────────────────────────────────────────────────────────────────────┘
```

Ordering facts, `cmd/litespm/main.go:1121-1233`:

*   **Secrets first.** `secrets.OpenSecretStore()` runs before anything else; if
    the vault is unavailable the daemon halts rather than storing credentials
    unprotected (`LPSM-AUTH-VAULT-UNAVAILABLE`, `main.go:1135-1140`).
*   **Recovery before serving.** `runStartupRecovery` executes *after* the DB
    opens and *before* the IPC listener is bound (`main.go:1162-1176`); a journal
    failure halts startup instead of serving unreconciled state.
*   **Providers start before the listener accepts.** `provider.StartConfigured`
    runs at `main.go:1184-1194`; `supervisor.StopAll` is deferred.
*   **Shutdown order** is the defer chain LIFO: listener → `StopAll` → DB → lock.

### 1.1 Exclusive Instance Locking

To prevent split-brain scenarios where two daemon processes run concurrently:

*   **Location:** `DATA_ROOT/daemon.lock` — `DaemonLockPath()` returns
    `filepath.Join(p.DataRoot, "daemon.lock")` (`internal/config/paths.go:207-209`).
    The lock is **not** under `RuntimeRoot`, and there is **no `daemon.pid`
    file**: the PID is the *contents* of `daemon.lock`.
*   **Mechanism (all platforms):** an atomic `O_CREATE|O_EXCL` file creation
    holding the daemon's PID (`cmd/litespm/main.go:1919-1937`, mode `0600`), not
    `flock`/`LockFileEx`. A pre-existing lock is read; if its PID looks alive the
    daemon refuses to start with `active daemon process running with PID %d`
    (`main.go:1142-1147`), otherwise the stale file is removed and re-created.
*   **Stale-lock caveat:** liveness is `processAlive`, which signals `0` on Unix
    but returns `true` unconditionally on Windows whenever `os.FindProcess`
    succeeds (`main.go:1946-1957`). A leftover `daemon.lock` after a Windows
    crash therefore blocks startup until the file is deleted by hand.
*   **A second invocation does not attach.** `daemon serve` exits on lock
    contention; it does not connect to the running daemon. The only production
    IPC client is the bridge shim, which dials the endpoint itself
    (`cmd/litespm/main.go:344`).

### 1.2 Autostart Behavior — `DESIGNED`, not implemented

The previously documented flow (shim spawns `litespm daemon serve --background`
and polls with exponential backoff) **does not exist**. What actually happens:

1.  `litespm bridge stdio --host <id>` resolves platform paths and performs
    **one** `ipc.Dial(paths.IPCEndpoint())` (`cmd/litespm/main.go:328-358`).
2.  If the dial fails (`ENOENT` / `ECONNREFUSED` / no listener), the shim prints
    `daemon dial failed: …; running standalone with no capabilities (start the
    daemon with 'litespm daemon serve')` and starts anyway with a `nil` client
    (`main.go:346`).
3.  In standalone mode every tool fails closed with
    `LPSM-IPC-DAEMON-UNREACHABLE` — no inventory, status, or result is fabricated
    (`internal/bridge/shim.go:274-280`, `:575-587`); `ping` honestly reports
    `{"connected": false}` (`shim.go:206-214`).

There is no spawn, no connect retry, and no backoff for the daemon endpoint
anywhere in the tree (the only `exec.Command` uses in the CLI are `git` in
`cmd/litespm/skills_update.go`). An
idle-timeout exit is equally absent: `config.DefaultConfig` defines
`IdleTimeout: 30 * time.Minute` (`internal/config/config.go:66`) but **no code
reads it**, so the daemon runs until a signal arrives. Both behaviours remain
`DESIGNED`; `STATUS.md` does not claim them.

---

## 2. Platform IPC Transports

### 2.1 Windows Named Pipes
*   **Pipe Path:** `\\.\pipe\litespm-daemon-<hex>` where `<hex>` is the **first
    12 hex characters of `SHA256(username)`** —
    `hex.EncodeToString(hasher.Sum(nil))[:12]` over the username only
    (`internal/config/paths.go:60-64`). No UserSID is mixed in, and the digest is
    12 characters, not 16. The pipe prefix always uses the current brand
    (`litespm`), so an adopted legacy `litepsm` root never changes the endpoint.
*   **Security Descriptor:** created with the SDDL string that grants Generic All
    (`GA`) exclusively to the object owner (`OW`):
    ```text
    SDDL: D:(A;;GA;;;OW)
    ```
    (`internal/ipc/transport_windows.go:13-16`, via `winio.PipeConfig`).
*   **Buffers / framing:** `InputBufferSize` and `OutputBufferSize` are 65,536
    bytes (64 KiB) and `MessageMode: false` — byte-stream framing carrying
    **line-delimited** JSON-RPC (`transport_windows.go:17-19`), not message-mode
    frames.
*   **Peer authentication:** there is **no in-process peer-identity check** on
    Windows — the owner-only DACL above is the boundary: the OS rejects a
    foreign SID's `CreateFile` before `Accept` ever returns. Because this
    package performs no query of its own, `daemon.handshake` reports
    `peerAuth.verified: false` with that reason (`transport_windows.go`,
    `peerCredentials`) rather than claiming a verification it did not perform.

### 2.2 Linux & macOS Domain Sockets
*   **Socket Path:** `<RuntimeRoot>/litespm.sock`, where `RuntimeRoot` resolves in
    this order (`internal/config/paths.go:73-93, 146-154`):
    1.  `LITESPM_RUNTIME_ROOT` (then legacy `LITEPSM_RUNTIME_ROOT`) if set;
    2.  `$XDG_RUNTIME_DIR/litespm` when `XDG_RUNTIME_DIR` is set;
    3.  **fallback `/tmp/litespm-<uid>`** when it is not
        (`filepath.Join("/tmp", fmt.Sprintf("%s-%s", brand.unixDir, uid))`);
    4.  on macOS: `~/Library/Caches/LiteSPM/run/litespm.sock`.
    A legacy `litepsm` runtime root is adopted when it already exists and the new
    default does not (`paths.go:163-175`), but the socket *file* name is always
    `litespm.sock` (`applyRootOverrides`, `paths.go:146-154`).
    The previously documented `~/.local/state/litespm/daemon.sock` is **not** a
    path this code produces (`~/.local/state` appears nowhere), and the file is
    named `litespm.sock`, not `daemon.sock`.
*   **Permissions:** the containing directory is created `0700` and the socket
    file is `chmod`'d `0600` after bind; any stale socket is unlinked first
    (`internal/ipc/transport_unix.go:13-34`).
*   **Peer authentication (register item A6 / I1):** directory and socket
    permissions are no longer the only boundary. After `Accept` and before the
    first request is read, the server resolves the peer's credentials with an
    authoritative kernel mechanism and **refuses** the connection when the peer
    uid differs from `os.Getuid()` — the peer's first request (typically
    `daemon.handshake`) is answered with `-32001` (`CodeUnauthorized`) naming
    the mismatch, then the connection is closed without any handler running
    (`internal/ipc/server.go`, `authenticatePeer`/`refusePeer`):
    *   **Linux:** `SO_PEERCRED` (`getsockopt(SOL_SOCKET, SO_PEERCRED)`) yields
        uid/gid/pid as snapshotted by the kernel at connect time — no
        pid-reuse race (`internal/ipc/transport_linux.go`).
    *   **macOS:** unix sockets have no `SO_PEERCRED`; `LOCAL_PEERCRED`
        (`SOL_LOCAL`, the `getpeereid()` path) returns the peer uid, with
        `LOCAL_PEERPID` + `sysctl(KERN_PROC_PID)` as fallback
        (`internal/ipc/transport_darwin.go`).
    *   **Fail-closed rule:** a mechanism that *exists* but errors refuses the
        connection; only a connection with no mechanism at all is allowed, and
        then it is marked **unverified with a stated reason** — in-memory
        `net.Pipe` listeners (no kernel credentials exist), and other unix GOOS
        where none is wired (`transport_other.go`). Verification is never
        silently claimed. The outcome rides the handler context
        (`ipc.PeerFromContext`) and is reported as `peerAuth` in the handshake
        result (`internal/ipc/peer.go`). Same-uid acceptance over a real unix
        socket and the refusal rule are covered by
        `internal/ipc/peer_test.go`; a genuine *cross-uid* connect is not —
        that needs a second OS user.

---

## 3. IPC Protocol Contract (JSON-RPC 2.0)

All communications between clients (the bridge shim, and any future CLI/doctor
client) and the daemon use JSON-RPC 2.0 over line-delimited streaming messages:
newline-terminated frames, `MaxMessageSize = 16 MiB`
(`internal/ipc/protocol.go:25-26, 110-166`), sequential integer request IDs minted by
the caller (`internal/ipc/client.go:110-114`), and ID-less notifications.

The daemon registers `daemon.handshake` plus 19 application methods
(`cmd/litespm/main.go:1244-1750`): `tools.list`, `catalog.search`,
`catalog.get_item`, `resolver.prepare_plan`, `install.execute`, `install.remove`,
`skills.list`, `skills.load_body`, `skills.read_resource`,
`capabilities.search`, `capabilities.describe`, `provider.probe`,
`provider.invoke`, `invocation.get`, `invocation.cancel`, `host.detect_config`,
`host.apply_setup`, `doctor.run_checks`, `system.status`. Method-by-method
contract lives in [ARCH/06 §3.2](06-API-CONTRACTS.md).

### 3.1 Handshake Procedure (`daemon.handshake`)

The wire types are `HandshakeParams` / `HandshakeResult`
(`internal/ipc/protocol.go:55-69`):

```json
// Request -> Daemon
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "daemon.handshake",
  "params": {
    "clientVersion": "1.0.0",
    "clientKind": "bridge",
    "hostId": "claude-code",
    "pid": 12345
  }
}

// Response <- Daemon
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "daemonVersion": "0.3.0",
    "protocolVersion": "2026-07-28",
    "pid": 54321,
    "peerAuth": {
      "verified": true,
      "method": "SO_PEERCRED",
      "uid": 1000,
      "pid": 12345
    }
  }
}
```

Exact field inventory — nothing else exists:

| Direction | Fields |
|---|---|
| Request params | `clientVersion` (string), `clientKind` (`"cli"` \| `"bridge"`), `hostId` (optional), `pid` (int) |
| Response result | `daemonVersion`, `protocolVersion`, `pid`, `peerAuth` (object: `verified` (bool), `method`?, `uid` (int, `-1` when unknown), `pid`?, `reason`?) |

*   **`peerAuth` is always present on connections served by this server** and
    reports peer authentication honestly: `verified: true` with the mechanism
    and uid on Linux/macOS unix sockets, `verified: false` **with a `reason`**
    where no credential mechanism exists (in-memory pipes, unimplemented unix
    GOOS, Windows' DACL-only transport) — see [ARCH/11 §2.2](11-LOCAL-RUNTIME-IPC.md)
    for the per-platform table. Handlers read the same identity from their
    context with `ipc.PeerFromContext(ctx)`.

*   **No** `sessionId`, `serverVersion`, `supportedFeatures`,
    `minimumClientVersion`, `protocolVersion`-in-params, or `clientType` fields
    exist.
*   **The handshake is neither mandatory nor enforced.** The server handler
    accepts any well-formed params (including empty) and returns the result
    without version gating (`internal/ipc/server.go:48-64`); no other method
    checks that a handshake happened. Version rejection
    (`LPSM-IPC-VERSION-INCOMPATIBLE`) is `DESIGNED` — no such error code exists in
    `internal/domain/errors.go`, whose only `LPSM-IPC-*` code is
    `LPSM-IPC-DAEMON-UNREACHABLE`.
*   **No production client calls it.** `ipc.Client.Handshake`
    (`internal/ipc/client.go:94-109`, sends `clientVersion: "0.1.0"`) has a single
    caller, `internal/ipc/ipc_test.go:78`; `runBridge` dials and serves without
    handshaking. Treat the procedure as the contract to wire, not as a step that
    occurs today.
*   **Agrees with ARCH/06.** [ARCH/06 §3.1](06-API-CONTRACTS.md) shows the same
    example and the same field inventory; both match `protocol.go`, and the
    earlier "ARCH/06 §3.1 is stale" flag has been withdrawn.

### 3.2 Request Cancellation (`$/cancelRequest`)

Clients can cancel in-flight work by sending a standard notification:

```json
{
  "jsonrpc": "2.0",
  "method": "$/cancelRequest",
  "params": {
    "id": 42
  }
}
```

*   **Implemented both ways.** The client emits it automatically when a caller's
    `context` is cancelled before the response arrives
    (`internal/ipc/client.go:143-151`, `CancelParams` at `protocol.go:71-73`); the
    server intercepts the notification **before** handler dispatch, looks up the
    per-connection `cancelFuncs` map keyed by the raw request id, and cancels that
    request's context (`internal/ipc/server.go:155-166, 191-207`).
*   Each request runs under `context.WithCancel(s.ctx)` derived from the server
    lifecycle context, so `server.Stop()` also cancels every in-flight handler.
*   Propagation depth: cancellation reaches whatever the handler honours its
    `ctx` (HTTP fetches, catalog reads). Provider child processes are *not*
    killed by this path — process teardown is the supervisor's job
    ([ARCH/14 §2](14-BRIDGE-PROVIDER-MCP.md)), and `provider.invoke` is
    unimplemented anyway ([STATUS.md](../STATUS.md) §4).
*   Cancelling an unknown or already-finished id is a silent no-op, and a
    notification never produces a response.
