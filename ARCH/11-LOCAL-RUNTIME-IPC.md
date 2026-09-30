# Local Runtime & IPC Architecture

## 1. Daemon Process Lifecycle

The LitePSM Daemon is a single-instance background worker per operating system user account. It manages all state mutations, provider processes, and security policies.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        Daemon Lifecycle States                         │
│                                                                        │
│   [Not Running]                                                        │
│         │ Bridge Shim or CLI boots daemon if absent                    │
│         ▼                                                              │
│   [Initializing] ──> Acquire Lock -> Run SQLite Migrations -> Recovery │
│         │                                                              │
│         ▼                                                              │
│   [Listening]    ──> Serve Named Pipe / Domain Socket IPC              │
│         │                                                              │
│         ▼ (Idle timeout after 30 min with 0 sessions OR SIGTERM)       │
│   [Shutting Down]──> Stop Provider Procs -> Close DB -> Release Lock   │
│         │                                                              │
│         ▼                                                              │
│   [Terminated]                                                         │
└────────────────────────────────────────────────────────────────────────┘
```

### 1.1 Exclusive Instance Locking
To prevent split-brain scenarios where two daemon processes run concurrently:
*   **Windows:** Opens `RUNTIME_ROOT/daemon.lock` with `LockFileEx` (`LOCKFILE_EXCLUSIVE_LOCK`).
*   **Linux / macOS:** Opens `RUNTIME_ROOT/daemon.lock` with `flock(fd, LOCK_EX | LOCK_NB)`.
*   If another process holds the lock, the new invocation connects to the existing daemon rather than starting a duplicate listener.

### 1.2 Autostart Behavior
When a host agent boots a Bridge Shim (e.g., `litepsm bridge stdio --host claude-code`), the shim attempts to connect to the IPC endpoint. If connection fails (`ECONNREFUSED` or `ENOENT`):
1.  The shim spawns `litepsm daemon serve --background` detached.
2.  Polls the IPC endpoint with exponential backoff (initial: 20ms, max: 200ms, timeout: 5s).
3.  Once connected, completes the handshake and proceeds.

---

## 2. Platform IPC Transports

### 2.1 Windows Named Pipes
*   **Pipe Path:** `\\.\pipe\litepsm-daemon-<SHA256(Username+UserSID)[:16]>`
*   **Security Descriptor:** Created with a custom Security Descriptor Definition Language (SDDL) string that grants Full Control (`GA`) exclusively to the Creator/Owner (`OW`) and denies all other users:
    ```text
    SDDL: D:(A;;GA;;;OW)
    ```
*   **Buffer Sizes:** In/out buffers initialized to 64 KiB with message-mode or byte-stream framing.

### 2.2 Linux & macOS Domain Sockets
*   **Socket Path:** `$XDG_RUNTIME_DIR/litepsm/daemon.sock` (fallback: `~/.local/state/litepsm/daemon.sock`).
*   **Permissions:** The containing directory is initialized with `0700` (`rwx------`). The socket file itself is restricted to `0600` (`rw-------`).

---

## 3. IPC Protocol Contract (JSON-RPC 2.0)

All communications between clients (CLI, Bridge Shims, Doctor) and the Daemon use standard JSON-RPC 2.0 over the streaming IPC channel.

### 3.1 Handshake Procedure
Every new connection must issue `daemon.handshake` as its first request before invoking any other RPC method:

```json
// Request -> Daemon
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "daemon.handshake",
  "params": {
    "protocolVersion": "1.0",
    "clientType": "bridge",
    "clientVersion": "1.0.0",
    "hostId": "claude-code",
    "sessionId": "sess_01J9X8K2M4N5P6Q7R8S9T0U1V2"
  }
}

// Response <- Daemon
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "serverVersion": "1.0.0",
    "protocolVersion": "1.0",
    "supportedFeatures": ["streamable-http", "rfc8785", "oauth-pkce"],
    "minimumClientVersion": "1.0.0"
  }
}
```

If the client's protocol version is unsupported, the daemon responds with `LPSM-IPC-VERSION-INCOMPATIBLE` and immediately terminates the connection.

### 3.2 Request Cancellation (`$/cancelRequest`)
Clients can cancel long-running operations (such as multi-megabyte artifact downloads or long-running provider calls) by sending a standard notification:
```json
{
  "jsonrpc": "2.0",
  "method": "$/cancelRequest",
  "params": {
    "id": 42
  }
}
```
The daemon cancels the associated Go context, propagating cancellation to network requests or provider child processes.
