# CLI Product Surface: Port, Orient & Help

> **Status: `DESIGNED`.** This document specifies the Phase-J command family —
> `litespm copy` (cross-agent porting), `list`/`inventory`, the `help` system,
> the orient commands (`status`/`diff`/`why`/`outdated`), and their
> relationship to profiles and the plan/apply spine. **None of it has code**
> except where a row in [TODO.md](../TODO.md) Phase J says otherwise, and no
> command below may appear in `litespm help` before it exists (§3.1). Delivery
> rows and acceptance evidence live in [TODO.md](../TODO.md) `LPSM-J010`–`J015`;
> this document is the design those rows implement.

---

## 1. Purpose & Invariants

LiteSPM has four user-facing jobs: **DISCOVER** (find capabilities across
ecosystems), **INSTALL** (add them safely to any supported agent), **PORT**
(reproduce an existing setup in another agent), and **MANAGE** (versions,
drift, updates, recovery). `ARCH/32`–`ARCH/37` cover the plumbing behind
those jobs; this document covers the **command surface** the user actually
types.

Invariants every command in this family must hold:

1. **Honesty over fluency.** No invented versions, counts, compatibility or
   popularity. A cell the data cannot answer renders `—` / `not knowable`,
   never a plausible guess. Any curated selection states its selection rule
   on screen ("Selected because the publisher is official — not a popularity
   ranking").
2. **Plan before write.** Any command that can mutate agent configuration
   (`copy`, later `sync`) prints its plan first and requires approval —
   interactively (TTY-gated, same terminal gate as `litespm approve`/`grant`)
   or explicitly (`--apply`/`--yes`). `--dry-run` is always accepted.
3. **Secrets move as references, never as values.** The canonical IR carries
   environment variable *names* (`ServerEntry.EnvNames`); literal values
   found in a source config are reported and dropped, never written to any
   target (this is the same rule `install --env` already enforces).
4. **Copies are managed, not spliced.** Writes go through the normal install
   paths — host-entry registration, skills ledger, deployment-mutation rows,
   the install transaction — so a copy is as reversible (`install remove`,
   `restore`) and as auditable as an install.
5. **Help documents only shipped commands.** The target taxonomy in §2 marks
   what exists; `help` output is generated from the dispatch table, so an
   unbuilt verb cannot be advertised (§3.1).

---

## 2. Command Taxonomy

The tree below is the target shape. **Bold** verbs exist in the dispatcher
today (`cmd/litespm/main.go`); the rest are `DESIGNED` here and move to
shipped only with their TODO row.

```text
LiteSPM — the package manager for AI agents
│
├── DISCOVER
│   ├── search        (shipped)
│   ├── info          — single-item detail view (not yet a verb; J013+)
│   ├── agents        (shipped: `litespm agent list|resolve`)
│   └── sources       — catalog sources in use (shipped: `catalog sync` prints them)
│
├── VIEW
│   ├── list          human setup overview ................ J011
│   ├── status        environment health at a glance ....... J013
│   ├── inventory     machine-state truth (JSON) ........... J011
│   ├── diff          compare agents/profiles/snapshots .... J013
│   ├── why           explain a resolution ................. J013
│   └── outdated      current vs compatible vs latest ....... J013
│
├── MANAGE
│   ├── install       (shipped; --frozen via ARCH/32/D1)
│   ├── install remove / restore (shipped)
│   ├── update        (shipped: self-update alias; package update is DESIGNED)
│   ├── copy          port a setup between agents .......... J010
│   ├── sync          reconcile a profile (gated on J014) .. later
│   ├── adopt         manage an existing external resource .. ARCH/33
│   └── unmanage      stop managing, leave installed ........ J011+ design
│
├── ENVIRONMENT
│   ├── profile       "Setups": create/add/apply ........... J014
│   ├── grant / approve (shipped)
│   └── lock          manifest + lockfile verbs ............ ARCH/32 (D1)
│
├── RECOVERY
│   ├── doctor        (shipped)
│   └── restore       (shipped: byte-exact rollback)
│
├── TRUST
│   ├── verify        source/digest/runtime checks ......... later (J013+)
│   └── trust         provenance report .................... later
│
├── SYSTEM
│   ├── setup, daemon, bridge, host, skills, catalog, agent,
│   │   capabilities, invoke, uninstall (all shipped)
│   ├── completion    (ARCH/37)
│   └── self-update   (shipped)
│
└── HELP
    ├── help              (shipped page; redesigned §3)
    ├── help <command>    (§3)
    └── help concepts     (§3.3)
```

Deliberately rejected for now: `clone` (use `profile create --from`),
`migrate` (`copy` then remove, user-driven), `mirror` (that is `sync`),
`matrix` (the website renders compatibility better; keep the CLI thin),
`help error <code>` (gains value only once error families like `LPSM-COPY-*`
are stable — spec'd in §5.7, shipped after copy).

---

## 3. Help System

### 3.1 Structure

`litespm help` (and bare `litespm`) becomes an educational page, not an
alphabetical dump:

```text
LiteSPM — the package manager for AI agents.
Discover, install, port and manage MCP servers, skills, plugins and
agents across Claude Code, Codex, OpenCode, Cursor, Cline and more.

QUICK START
  litespm search github
  litespm install github
  litespm copy --from codex --to opencode      (when shipped)
  litespm doctor

DISCOVER    search · agents · catalog sync
MANAGE      install · install remove · restore · skills · grant · approve
VIEW        (list · status · diff — when shipped)
RECOVER     restore · doctor · uninstall
CONFIGURE   setup · host · daemon · bridge · config
SYSTEM      self-update · version · capabilities · invoke

EXAMPLES    (3–5 real, copy-pasteable)
Run `litespm help <command>` for detail, `litespm help concepts` for
vocabulary.
```

`litespm help <command>` template — each field filled, none faked:

```text
LITESPM <COMMAND>

One-line purpose.

USAGE
  <synopsis>

WHAT IT DOES          (2–4 sentences, plain language first)
WHAT IT DOES NOT      (e.g. copy: does not copy plaintext secrets;
                       unsupported kinds are reported, not converted)
EXAMPLES              (2–4, including the common failure-avoiding one)
EXIT CODES            (0 ok · 1 refused/failed · 2 usage — ARCH/20)
SEE ALSO              (related commands)
```

**Binding rule:** the help catalog is a table keyed by the same strings the
dispatch switch matches. `TestHelpCoversEveryDispatchCase` parses the
dispatch cases of `cmd/litespm/main.go` and fails when a case has no help
entry (pattern: the existing `TestCapabilitiesUsageMentionsEveryCommand`).
A new command therefore cannot ship without help, and help cannot advertise
a command that does not dispatch.

### 3.2 LLD

- New file `cmd/litespm/help.go`: `helpCatalog map[string]commandHelp` +
  `renderHelp(cmd)` / `renderDefaultHelp()` / `renderConcepts()`.
- `printUsage()` in `main.go` becomes a thin alias of `renderDefaultHelp`
  so bare `litespm` and `litespm help` can never disagree.
- `litespm help <command>` for an unknown name prints the shipped list and
  exits 2 (usage), never 0.

### 3.3 `help concepts`

A glossary that defines vocabulary in user language first: Package,
Capability, Agent, MCP Server ("lets an AI work with services such as
GitHub, databases and browsers"), Skill, Plugin, Bridge, Scope
(user/project), **Observed** (LiteSPM found it) vs **Managed** (LiteSPM
controls its lifecycle) vs **Adopted** (user asked LiteSPM to take over),
Deployment vs Capability (one capability deployed to three agents is three
deployments), Reference vs Value (secrets).

---

## 4. `list` and `inventory`

Two audiences, one truth. Both read the same sources; they differ only in
what they surface.

| | `litespm list` | `litespm inventory` |
|---|---|---|
| Audience | a person checking their setup | an engineer / support / automation |
| Sources | state rows (installs, registrations, grants), adapter detection (`DetectPreExistingComponents`), skills ledger, deployment ledger | same, plus content digests, config fingerprints, first/last seen, drift state |
| Shape | human tables, grouped by agent and kind | `--json` (stable schema), one record per deployment |
| Vocabulary | managed / observed / adopted / drifted / broken | the same words, machine-emitted |

Filters (both): `list agents|mcp|skills|plugins`, `list --agent <id>`,
`--managed|--observed|--drifted`, `--project .|--global`, `--json`.

Counting rule (must appear in the human output): **unique capabilities ≠
deployments** — "18 unique capabilities · 27 deployments" is the honest
phrasing; a capability installed for three agents counts once as a
capability and three times as deployments.

LLD: implementation is `cmd/litespm/list.go` wiring the existing but
unwired `internal/inventory` + `internal/binding` libraries (STATUS §5, D3)
and the adapters' detection; no new state tables. `inventory --json` gets a
schema test (`TestInventoryJSONShape`) so automation can depend on it.

---

## 5. `copy` — cross-agent porting

### 5.1 Problem & non-goals

Users move between agents and re-derive every config by hand (the motivating
real transcript: nine OpenCode MCP servers re-typed into `~/.codex/config.toml`
by a human, including splitting OpenCode's `command: ["bin","arg",…]` array
into Codex's `command` + `args`). Copy exists so that translation is
mechanical.

`copy` is **not**: a file copy, a config diff tool (`diff` is), a continuous
reconciler (`sync` is), or a secret mover (invariant 3).

The N×N problem is refused outright: with ~50 bridge targets, pairwise
translators are 2,450 directed pairs. LiteSPM already ships the alternative.

### 5.2 HLD — one intermediate representation

```text
 SOURCE AGENT                          TARGET AGENT
 ┌──────────────────┐                  ┌──────────────────┐
 │ opencode.json    │                  │ config.toml      │
 │ command:[engram, │                  │ command="engram" │
 │   mcp, --tools…] │                  │ args=["mcp",…]   │
 └────────┬─────────┘                  └────────▲─────────┘
          │ adapter READ                        │ adapter RENDER
          │ (existing seams)                    │ (existing seams)
          ▼                                     │
 ┌─────────────────────────────────────────────┴──────────┐
 │ canonical IR (host.ServerEntry / skills record)        │
 │ name · command · args[] · envRefs[] · transport · scope│
 └────────────────────────────────────────────────────────┘
          │ support check: does the target have an
          │ entrySpec for this shape? (entrySpecFor)
          ▼
      CopyPlan → approval → managed write → read-back verify
```

- **Read seam (exists):** `host.ListServerEntriesWithValues` (registered
  entries with their env content) and each adapter's
  `DetectPreExistingComponents` (pre-existing/native components).
- **IR (exists):** `host.ServerEntry{Command, Args, EnvNames, …}` — note the
  type already separates `Env` (literal values found in a config) from
  `EnvNames` (forwarded variable names). The IR carries **`EnvNames` only**;
  literals are reported under `Needs secrets` and dropped.
- **Render seam (exists):** `host.InstallServerEntry` + the data-driven
  entry specs — the same code path installs use, so target-shape variation
  stays data, not code.
- **Support check (exists):** `entrySpecFor(scope)` presence for the target
  host decides `direct` vs `unsupported` at the config-shape level.
- **Skills IR:** skill name + provenance digest from the skills ledger;
  target = `AgentSkillDir(target)` through the normal skills installer.

No adapter gains a new interface method for v1 (recorded as D-028).

### 5.3 LLD — package layout & types

```text
internal/porting/          pure planner, no daemon I/O
    ir.go                  normalize(source entries) → IR
    plan.go                plan(IR, targetCaps, options) → CopyPlan
    conflict.go            conflict strategies
    secrets.go             env classification: ref vs literal
cmd/litespm/copy.go        CLI: read source, build plan, render, approve, apply
```

```go
type CopyOptions struct {
    From, To   string   // agent ids, validated against the adapter registry
    Kinds      []string // "" = all supported (mcp, skill); others reported
    Items      []string // explicit ids/names to include
    Exclude    []string
    Conflict   string    // ask (default) | keep-target | use-source | compatible | skip
    Apply      bool      // plan is the default; --apply executes
    Yes        bool      // non-interactive approval (requires --apply)
    DryRun     bool      // always accepted; identical to the default plan
    Global     bool      // scope: user (default) | project via --project <dir>
}

type CopyItem struct {
    Kind       string // mcp | skill | plugin | …
    ID         string
    IR         ServerEntry // or skill record
    Action     string      // direct | translated | conflict | skipped | unsupported
    Reason     string      // why not direct / why skipped
    TargetDiff string      // human one-liner of the shape change
    Needs      []string    // env var NAMES the target must provide
    Verify     []string    // verification steps this item will run
}

type CopyPlan struct {
    Source, Target string
    Found          map[string]int // per kind
    Items          []CopyItem
    Conflicts      []Conflict
    Unchanged      int            // already identical on target
}
```

**Pipeline (the only order):**

```text
read source → normalize to IR → filter (kinds/items/exclude)
  → support check per target → translate (adapter render in-memory)
  → dependency/executable validation (LookPath on Command; report missing)
  → conflict detection (target already has this name?)
  → PLAN printed (no writes happened)
  → approval (TTY "yes" | --apply --yes)
  → managed write (InstallServerEntry / skills installer,
                   deployment rows, install transaction — A1)
  → read-back verify: re-read target entries, compare fingerprints to plan
  → report (per-item verification state) + exit code
```

**Origin of copied installs.** A copied entry may have no catalog listing.
Such installs get a local-origin listing id under the existing grammar
(`mcp:adopted:<source-host>:<name>`, `skill:adopted:…`), status recorded
honestly as copied/adopted — never as catalog-verified. Provider/capability
rows appear after a discovery pass (`capabilities refresh`), not before.
*Implementation checkpoint:* confirm `deployment_mutations.capability_id`
accepts empty for not-yet-discovered entries before wiring (FK audit).

**Conflict strategies** (default `ask`, TTY-gated like approve/grant):

| Strategy | Semantics |
|---|---|
| `ask` | per-conflict prompt; refuses in non-TTY without `--yes` |
| `keep-target` | leave target as-is, item becomes `skipped:conflict` |
| `use-source` | overwrite target with the translated source entry (backup + ledger, ordinary force semantics) |
| `compatible` | keep whichever side satisfies the target's entry spec; report if both do and versions differ |
| `skip` | never touch conflicts; exit 0 with counts |

A conflict is detected by name on the target and classified by comparing
fingerprints: identical → `unchanged`; different → `conflict`.

**Verification ladder** (reported per item; levels 1–2 ship with v1):

| Level | Meaning | v1 |
|---|---|---|
| L1 config | target re-parses; entry fingerprint equals the plan's rendered entry | yes |
| L2 executable | `Command` resolves (LookPath / exists) | yes |
| L3 runtime | process starts | later, via `internal/discover` |
| L4 protocol | MCP `initialize` succeeds | later |
| L5 capability | `tools/list` returns the expected surface | later |

Later levels reuse the existing probe path — copy must not grow its own
process supervisor.

### 5.4 CLI surface

```text
litespm copy --from codex --to opencode                  # plan (default)
litespm copy --from codex --to opencode --apply          # execute plan
litespm copy skills --from codex --to opencode           # kind filter
litespm copy mcp,skills --from opencode --to codex --exclude exa
litespm copy github --from codex --to opencode           # single item
litespm copy --from codex --to opencode --conflict keep-target --yes
litespm copy --from codex --to opencode --project . --global
```

Plan output shape (counts by action, then rows, then needs, then the apply
hint):

```text
Copy Codex → OpenCode                       (no changes have been made)

Found     14 MCP servers · 8 skills · 3 plugins
DIRECT      12 MCP · 7 skills
TRANSLATED   2 MCP (command → command+args splits reported per row)
SKIPPED      1 skill already identical · 1 plugin unsupported (reason)
CONFLICTS    github — target exists, sources differ
SECRETS      GITHUB_TOKEN — name only; value NOT copied

Apply:  litespm copy --from codex --to opencode --apply
```

Exit codes follow ARCH/20: 0 success (including "nothing to do"), 1
refused/failed (unresolvable conflict, verification failure), 2 usage.

### 5.5 Secrets (normative)

1. Source `Env` literals → listed under `Needs`/`dropped` with a warning;
   **never written anywhere**.
2. Source env references (`${VAR}` / name-list styles) → carried as
   `EnvNames`; the target gets the same reference form its spec declares.
3. The plan always shows the names it will ask the target to provide; the
   user binds real values through the existing secrets paths
   (`install --env`, OS vault) — copy never sees a value.

### 5.6 `copy` vs neighbours

| Command | Question it answers |
|---|---|
| `copy` | one-time: make agent B like agent A (source untouched) |
| `diff a b` | what differs between two agents/profiles/snapshots right now |
| `sync` (later) | keep agents aligned with a declared desired state |
| `profile apply` (J014) | apply a named Setup to N agents — copy's plan, persisted |

A `copy --from X` that succeeds is the natural birth of
`profile create --from X` (same planner, output persisted instead of
written).

### 5.7 Error families (for future `help error <code>`)

`LPSM-COPY-001` unknown source/target agent · `002` target cannot represent
this entry shape · `003` unsupported kind (reason attached) · `004`
unresolved conflict without a strategy in non-TTY · `005` literal secret in
source — value dropped, name reported · `006` executable not resolvable on
target machine (copy still allowed, verification says so) · `007`
verification failed after write (install transaction already rolled back).

### 5.8 Acceptance sketches

1. `TestPortingTranslatesOpenCodeCommandArrayToCodexArgs` — the engram
   fixture: `["engram","mcp","--tools=agent,graph"]` → `command`+`args`,
   byte-pinned.
2. `TestPortingPlanCountsAndNoWrites` — plan phase leaves both configs
   byte-identical (hash before/after).
3. `TestPortingNeverWritesLiteralEnvValues` — source contains a literal
   token; after apply, target config and all plan output are grepped for
   the literal: absent; `Needs` lists the name.
4. `TestPortingApplyIsManagedAndUndoable` — apply creates install +
   deployment rows; `install remove` (or `restore`) reverses it to
   byte-identical (A1/A2 machinery).
5. `TestPortingConflictStrategies` — one test, five sub-cases.
6. `TestPortingReportsUnsupportedPluginWithReason`.
7. `TestPortingSkillCopyRoundTrip` — skill ledger → target agent dir.

---

## 6. Orient commands (`status`/`diff`/`why`/`outdated`)

Contracts (J013), all over real signals only:

- `status` — one screen: agents found, capabilities (unique vs deployments),
  managed/observed counts, updates available, drift count, broken count, a
  single health verdict; footer points at `doctor`. Never claims health it
  did not measure (the doctor honesty rules apply).
- `diff <a> <b>` — accepts two agents, `profile:x`, or `snapshot:y`
  (snapshots are ARCH/33-adjacent; until they exist, only agents and
  profiles are accepted, and the command says so). Output sections: only in
  A / only in B / different (with both digests) / same.
- `why <pkg>` — resolution explanation: which plan/profile/requirement
  pulled it in, which constraint selected the version, and — when a version
  was NOT selected — the recorded reason (env requirement, adapter
  verification). Prints `not recorded` rather than inventing a reason.
- `outdated` — current vs compatible vs latest columns; a column whose
  value is unknowable (no upstream version) prints `—`.

## 7. Profiles as "Setups" (pointer)

`profile create|add|apply` wires the existing `internal/profiles` library
(J014, ARCH/35): `profile create my-setup --from codex` persists a
`copy`-style plan; `profile apply --to a,b,c` replays it through the same
managed-write path. Leases/`sync` stay gated on ARCH/34 (J014/J015 rows say
so); no lease is issued before the runtime registry exists.

## 8. Plan/apply spine (pointer)

Long-term, `install`, `copy`, `update` and `sync` all become
intent → plan → approval → transaction → verify → receipt, sharing one plan
structure (J015; depends on ARCH/34 receipts). This document's `CopyPlan`
is deliberately shaped to become that structure rather than a private
format.

## 9. Related documents

- [ARCH/16 §6](16-HOST-ADAPTERS.md) — the adapter read/render contract copy
  composes.
- [ARCH/32](32-MANIFEST-LOCK-INTEROP.md) — lock/`--frozen` verbs.
- [ARCH/35](35-PROFILES-AND-CAPABILITY-LEASES.md) — profiles and leases.
- [ARCH/13](13-RESOLVER-INSTALL-ENGINE.md) / [ARCH/34](34-RUNTIME-INVOCATION-RECEIPTS.md)
  — plan/approval/receipt spine.
- [ARCH/20](20-ERRORS-AUDIT-DOCTOR.md) — error envelope and exit codes.
- [ARCH/25](25-WEB-FRONTEND-UI.md) §5–§8 — the UI half of Phase J.
- [TODO.md](../TODO.md) Phase J — delivery rows and acceptance evidence.
