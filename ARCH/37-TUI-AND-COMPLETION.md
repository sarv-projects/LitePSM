# TUI, Local Dashboard & Shell Completion

Status: **`DESIGNED`.** This document specifies the interactive terminal UI, the local dashboard boundary, shell completion, and the `why` command. **There is no TUI, no local dashboard, and no completion generator today** ([STATUS.md](../STATUS.md) §6). Do not present them as implemented.

---

## 1. The Prime Constraint: Zero Business Logic

The TUI is a **front-end**, not a second implementation.

> Every TUI action MUST call the same daemon operation (IPC method) that the equivalent CLI command calls. The TUI may format, paginate, and stage; it may not decide policy, resolve versions, compute digests, or write files itself.

Consequences:

* There is exactly one policy engine, one resolver, one install engine, one ledger. The TUI adds none.
* If a CLI command and a TUI action differ in behaviour, that is a TUI bug by definition.
* The TUI is testable as a client: it can be driven against a fake daemon and asserted to issue the documented IPC calls.

---

## 2. TUI as a First-Class Front-End

The TUI is the CLI's interactive surface, sharing the CLI's daemon client. It is not a wrapper that shells out to the binary and parses human output; it speaks the same typed IPC contract ([11](11-LOCAL-RUNTIME-IPC.md)) the CLI uses.

Layout: a left tab rail + main pane + a persistent **tray** (staged plan) + a status/health line.

### 2.1 Tabs

| Tab | Shows | Backing daemon ops (same as CLI) |
|---|---|---|
| **Explore** | Catalog search/browse, canonical entries with sources and evidence states | `catalog.search`, `catalog.get` |
| **Installed** | Ledger-backed installs + read-only detected externals; no fabricated health | `install.list`, `host.detect` |
| **Profiles** | Profile list, diff, activate ([35](35-PROFILES-AND-CAPABILITY-LEASES.md)) | `profile.*` |
| **Agents** | Bridge/skill targets, per-host state, `host setup/remove` | `host.list`, `host.detect`, `host.setup`, `host.remove` |
| **Connections** | Provider instances, auth state, leases ([34](34-RUNTIME-INVOCATION-RECEIPTS.md), [35](35-PROFILES-AND-CAPABILITY-LEASES.md)) | `provider.probe`, `auth.*` |
| **Activity** | Invocation receipts, audit events, in-flight jobs | `invocation.get`, `audit.list` |
| **Policy** | Effective policy and `policy explain` chain ([36](36-ENTERPRISE-POLICY-AND-AUDIT.md)) | `policy.explain` |
| **Doctor** | Diagnostics with category exit states and `--repair` plan | `doctor.run`, `doctor.repair` |
| **Settings** | Config, scopes, targets, secret **names** (never values) | `config.get`, `config.set`, `secrets.listMetadata` |

Rules:

* The **Installed** tab renders `— Unknown` where no observed status exists; it never fabricates a green light ([AGENTS.md](../AGENTS.md) Tab 4).
* No tab bypasses a plan or approval. Destructive actions open the tray.

### 2.2 Keybindings & Accessibility

* Full keyboard navigation; a visible focus ring per [25](25-WEB-FRONTEND-UI.md)/WCAG 2.2 discipline.
* A `?` help overlay lists every binding; every action is discoverable without a mouse.
* `--no-color`, `--quiet`, and non-TTY fallback must remain correct: when stdout is not a terminal the TUI does not start and the CLI path is used.

---

## 3. Staged-Plan "Tray"

The tray is the TUI's transaction surface. It accumulates *staged operations* (e.g. install A, update B, uninstall C, host setup D) and applies them as **one plan**, not N independent writes.

```text
┌ Tray (4 staged) ─────────────────────────────────────────────┐
│  + install   mcp:…:postgres        >=1.4 <2                  │
│  ~ update    skill:…:review-pr      1.1.0 → 1.2.0            │
│  - uninstall plugin:…:web-toolkit                            │
│  ⚙ host setup claude-code (project)                          │
│                                                              │
│  [review diff]   [apply as one plan]   [discard]             │
└──────────────────────────────────────────────────────────────┘
```

Rules:

* The tray produces one `InstallPlan` and one `planHash`; `apply` is atomic with one rollback scope ([13](13-RESOLVER-INSTALL-ENGINE.md), [33](33-DEPLOYMENT-LEDGER-RECONCILIATION.md)).
* A staged item is a **preview**; nothing is written until apply.
* Policy is evaluated at stage time and re-evaluated at apply time; a decision that changed during staging blocks apply and re-opens review.
* The tray shows the aggregate diff before apply; discard leaves no trace.

---

## 4. Local Dashboard vs Public Site

There are two distinct surfaces and they must not be confused:

| | Public website ([25](25-WEB-FRONTEND-UI.md)) | Local dashboard (this section) |
|---|---|---|
| Hosting | Cloudflare Pages, static, read-only | Served on loopback by the daemon/TUI |
| Data | Published catalog only | Local state: installs, ledger, receipts, policy |
| Secret material | Never present | **Never leaves the machine; never sent to the public site** |
| Auth | None (public) | Loopback only, same OS-user restriction as the IPC socket |

Hard rules:

* The local dashboard binds to `127.0.0.1`/loopback with the same access restriction as the IPC endpoint ([11 §2](11-LOCAL-RUNTIME-IPC.md)).
* **Keystore data never leaves the machine.** No page, telemetry, or sync path may send secret values, secret metadata beyond names, or vault contents to any remote origin.
* Local state is read through the same daemon ops as the TUI/CLI; the dashboard is another thin client, not an alternate state store.
* The two surfaces may share vocabulary (capability, source, evidence state) but not data.

---

## 5. Shell Completion

Dynamic completion for **bash, zsh, fish, and PowerShell**, generated by `cmd/litespm`.

* `litespm completion <shell>` prints the script; `install` instructions are documented per shell.
* Completion is **dynamic** where values are data-driven: capability IDs from the catalog, host IDs from the `BridgeTarget` registry ([30](30-DATA-DRIVEN-BRIDGE-TARGETS.md)), profile names, scopes, and formats. It must not ship a stale hard-coded list.
* Value completion queries the daemon/catalog through the same client; when offline it degrades to the static verb/flag set rather than erroring.
* Flags complete from a single source of truth shared with the CLI parser, so a new flag cannot be missing from completion.

---

## 6. `litespm why`

`why` answers provenance questions from the ledger and lock, not from inference:

* `litespm why <package-id>` — where this package came from (sources/aliases, lock entry, plan, approval, install).
* `litespm why <path>` — which install/capability owns a deployed config path, via the deployment ledger ([33](33-DEPLOYMENT-LEDGER-RECONCILIATION.md)).

The second form is the more novel half: it resolves an arbitrary owned path to the exact `DeploymentMutation` rows, host, scope, locator, and the plan that wrote it. `why` is read-only and MUST NOT mutate.

---

## 7. Acceptance Test Sketch

1. Every TUI action issues the same IPC method as its CLI equivalent (asserted against a recording fake daemon).
2. A staged tray of 4 operations applies as one plan with one rollback; a forced failure rolls all 4 back.
3. The local dashboard never transmits keystore data (network-egress test while browsing every page).
4. `litespm completion bash|zsh|fish|powershell` emits a script that completes verbs, flags, and dynamic IDs.
5. `why <path>` resolves a deployed path to its ledger rows; `why <package>` resolves to sources/lock/plan.
6. In a non-TTY, or with `--json`, the interactive UI does not start.

---

## 8. Related Documents

* IPC contract and loopback restriction: [11 — Local Runtime & IPC](11-LOCAL-RUNTIME-IPC.md).
* Public site (distinct surface): [25 — Web Frontend UI](25-WEB-FRONTEND-UI.md).
* Plan/tray transaction model: [13 — Resolver & Install Engine](13-RESOLVER-INSTALL-ENGINE.md).
* Ledger backing `why <path>`: [33 — Deployment Ledger & Reconciliation](33-DEPLOYMENT-LEDGER-RECONCILIATION.md).
* Profiles and leases surfaced in the TUI: [35 — Profiles & Capability Leases](35-PROFILES-AND-CAPABILITY-LEASES.md).
* Policy explain surfaced in the TUI: [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md).
