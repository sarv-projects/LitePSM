package main

// help.go — `litespm help` (ARCH/38 §3).
//
// Three surfaces, one catalog:
//
//	litespm help            the educational page (`--help`/`-h` print it too)
//	litespm help <command>  USAGE / WHAT IT DOES / WHAT IT DOES NOT / EXAMPLES /
//	                        EXIT CODES / SEE ALSO for one shipped command
//	litespm help concepts   the vocabulary glossary
//
// helpCatalog is keyed by EXACTLY the strings the dispatch switch in main.go
// matches — aliases included — and TestHelpCoversEveryDispatchCase parses that
// switch: a command cannot ship without help, and help cannot advertise a
// command that does not dispatch. printUsage() in main.go is a thin alias of
// renderDefaultHelp, so a bare invocation and `litespm help` can never
// disagree.
//
// Every line describes what the command does today. No DESIGNED verb (ARCH/38
// §2) may appear: help documents shipped commands only.

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// commandHelp is one command's help page. Every field is filled from real
// behavior; an empty field renders as a missing section rather than filler,
// and the completeness tests keep each page whole.
type commandHelp struct {
	// Canonical is the name the page is titled and listed under — the first
	// literal of the command's dispatch case.
	Canonical string
	// Aliases are the remaining literals of the same dispatch case; they
	// share this page so a help target can never disagree with itself.
	Aliases  []string
	Group    string
	Purpose  string   // one line, printed under the title
	Synopsis []string // USAGE lines
	Does     []string // WHAT IT DOES: plain language first
	DoesNot  []string // WHAT IT DOES NOT: the honest limits
	Examples []string // 2-5 copy-pasteable
	SeeAlso  []string // related commands, dispatched names only
	ExitNote string   // extra exit-code lines beyond 0/1/2 ("" = none)
}

// helpGroup orders the default page. Names are canonical help keys; the
// completeness test proves the list and the catalog cannot drift apart.
type helpGroup struct {
	Label   string
	Entries []string
}

// helpGroups is the section layout of `litespm help` (ARCH/38 §3.1), carrying
// only commands that exist in the dispatcher.
var helpGroups = []helpGroup{
	{"DISCOVER", []string{"search", "agent", "catalog"}},
	{"VIEW", []string{"list", "inventory"}},
	{"MANAGE", []string{"install", "copy", "lock", "skills", "grant", "approve"}},
	{"RECOVER", []string{"restore", "doctor", "uninstall"}},
	{"CONFIGURE", []string{"setup", "host", "daemon", "bridge"}},
	{"SYSTEM", []string{"self-update", "version", "capabilities", "invoke"}},
	{"HELP", []string{"help"}},
}

// helpCatalog maps every literal the dispatch switch matches to its page.
var helpCatalog = buildHelpCatalog()

func buildHelpCatalog() map[string]commandHelp {
	pages := []commandHelp{
		{
			Canonical: "search",
			Group:     "DISCOVER",
			Purpose:   "Search the local catalog index for MCP servers, skills and plugins.",
			Synopsis:  []string{"litespm search <query>"},
			Does: []string{
				"Looks up your words in the catalog index that `litespm catalog sync` keeps on disk and prints up to 25 matches with their id, kind, publisher and summary. Plain words work: `litespm search github`.",
				"The index is local, so search answers the same way every time you run it with the same synced release.",
			},
			DoesNot: []string{
				"Does not search the network: an empty index is reported as empty, with the `litespm catalog sync` hint, never filled in from a guess.",
				"Does not install anything, and does not rank results by popularity — the catalog publishes no popularity numbers.",
			},
			Examples: []string{
				"litespm search github",
				"litespm search postgres",
				"litespm catalog sync   # refresh the index first if it is empty",
			},
			SeeAlso: []string{"install", "catalog", "list"},
		},
		{
			Canonical: "agent",
			Group:     "DISCOVER",
			Purpose:   "List installable coding agents and resolve how to launch one here.",
			Synopsis: []string{
				"litespm agent list [--registry <url>] [--file <path>] [--json]",
				"litespm agent resolve <id> [--registry <url>] [--file <path>] [--json]",
			},
			Does: []string{
				"`list` prints the agents the registry offers, with the launch strategy that applies to this operating system and architecture.",
				"`resolve <id>` computes that agent's launch spec: executable, arguments, environment and — when the registry publishes one — the archive and its SHA-256.",
			},
			DoesNot: []string{
				"Does not download, install or start the agent: resolve reports the spec, it does not act on it.",
				"Does not install MCP servers or skills; that is `litespm install` and `litespm skills add`.",
			},
			Examples: []string{
				"litespm agent list",
				"litespm agent list --json",
				"litespm agent resolve <agent-id>",
			},
			SeeAlso: []string{"install", "host", "capabilities"},
		},
		{
			Canonical: "catalog",
			Group:     "DISCOVER",
			Purpose:   "Synchronize the catalog index, or build the static release tree.",
			Synopsis: []string{
				"litespm catalog sync",
				"litespm catalog build [--dataset <file>] [--out <dir>] [--release-id <id>]",
			},
			Does: []string{
				"`sync` fetches the published release pointer, manifest and listings, verifies every digest, and rewrites the local index only when the whole chain checks out.",
				"`build` compiles the catalog dataset into the static /v1 release tree the website serves; `--verify` re-checks a tree against its own pointer.",
			},
			DoesNot: []string{
				"Does not install, update or remove anything: the catalog only feeds `search`, `install` and `lock`.",
				"Does not write a partial index: a failed sync leaves the existing local cache unchanged.",
			},
			Examples: []string{
				"litespm catalog sync",
				"litespm search postgres   # after a sync",
				"litespm catalog build --verify web/public",
			},
			SeeAlso: []string{"search", "install", "lock"},
		},
		{
			Canonical: "list",
			Group:     "VIEW",
			Purpose:   "Show what is deployed on this machine, for a person to read.",
			Synopsis: []string{
				"litespm list [agents|mcp|skills|plugins] [flags]",
				"  --agent <id>        only this agent",
				"  --managed           only what LiteSPM controls",
				"  --observed          only what LiteSPM found but does not control",
				"  --drifted           only what changed (or went missing) since install",
				"  --project <dir>     only project-scope rows for that directory",
				"  --global            only user-scope rows",
				"  --kind <kind>       the same kind filter as the positional word",
				"  --json              the machine-readable inventory instead of tables",
			},
			Does: []string{
				"Reads the same sources as `litespm inventory` — install rows, the deployment ledger, the skills ledger and each agent adapter's read-only detection — and prints tables grouped by agent and by kind.",
				"Counts unique capabilities separately from deployments: one capability installed for three agents is one capability and three deployments, and the headline says so (`N unique capabilities · M deployments`).",
				"Labels every row honestly: managed (LiteSPM controls its lifecycle), observed (LiteSPM found it in an agent config), adopted (copied from another agent and now managed), drifted (changed since install), broken (recorded but missing). A cell the data cannot answer renders `—`.",
			},
			DoesNot: []string{
				"Does not health-check anything: no process is started, so no row claims Ready, running or verified. State comes from recorded rows plus a config read-back, and nothing else.",
				"Does not write, repair or adopt anything — `list` is read-only. Adoption is not implemented yet, so an observed row stays observed.",
				"Does not invent counts, versions or compatibility: a field with no recorded value renders `—` or `not knowable`.",
			},
			Examples: []string{
				"litespm list",
				"litespm list agents",
				"litespm list --drifted",
				"litespm list --json",
			},
			SeeAlso: []string{"inventory", "doctor", "host"},
		},
		{
			Canonical: "inventory",
			Group:     "VIEW",
			Purpose:   "Print machine-state truth as JSON: one record per deployment.",
			Synopsis: []string{
				"litespm inventory [mcp|skills|plugins] [--agent <id>]",
				"  [--managed|--observed|--drifted]  [--project <dir>|--global]  [--json]",
			},
			Does: []string{
				"Reads exactly the sources `litespm list` reads and emits them as a stable JSON document (schema `litespm.inventory/v1`): one record per deployment, with kind, id, name, agent, scope, state, placement, origin, install id, config path, locator, entry style, digests, timestamps and a plain-language detail.",
				"`counts` separates `uniqueCapabilities` from `deployments`, `capturedAt` is when the observations were taken, and `warnings` carries every source that could not be read — a degraded read is reported, never silently treated as empty.",
			},
			DoesNot: []string{
				"Does not health-check, and does not report a value it did not read: unknown fields are empty strings, never a plausible guess.",
				"Does not write anything, and adds no state table. `--json` is accepted for symmetry; JSON is the only output this command has.",
			},
			Examples: []string{
				"litespm inventory",
				"litespm inventory --json",
				"litespm inventory mcp --agent codex --json",
			},
			SeeAlso: []string{"list", "doctor"},
		},
		{
			Canonical: "install",
			Aliases:   []string{"add", "i"},
			Group:     "MANAGE",
			Purpose:   "Install a capability, or remove one install surgically.",
			Synopsis: []string{
				"litespm install <listing-id> [--version <ver>] [--scope user|project]",
				"               [--workspace <id>] [--host <host-id>]... [--env <VAR>]...",
				"               [--force] [--frozen]",
				"litespm install remove <installId>",
			},
			Does: []string{
				"Resolves the listing in the local index, records a plan plus your approval, then installs: skills are copied as files into the agent's own skill directory, and MCP servers are registered as a named entry in each configured agent's config.",
				"`--host` names the agents to register with (repeatable; default: every agent where `litespm host setup` has been run). `--env <VAR>` forwards an environment variable NAME as a reference the agent expands.",
				"`install remove <installId>` strips only LiteSPM's entries and keeps any node you edited; `--frozen` installs exactly the version `litespm lock` recorded.",
			},
			DoesNot: []string{
				"Never writes a secret value: `--env` records a name/reference only, so the credential stays where your agent was started.",
				"Plugin listings fail closed with LPSM-ARTIFACT-UNAVAILABLE — there is no plugin artifact source yet, so no plugin is ever half-installed.",
				"Does not touch an agent that has no LiteSPM bridge entry, and refuses a name collision instead of overwriting (unless you pass `--force`).",
			},
			Examples: []string{
				"litespm install <listing-id>",
				"litespm install <listing-id> --host codex --env GITHUB_TOKEN",
				"litespm install <listing-id> --frozen",
				"litespm install remove <installId>",
			},
			SeeAlso: []string{"restore", "lock", "skills", "list"},
		},
		{
			Canonical: "copy",
			Group:     "MANAGE",
			Purpose:   "Port one agent's MCP servers and skills into another agent.",
			Synopsis: []string{
				"litespm copy [items|kinds...] --from <host> --to <host> [flags]",
				"  --conflict ask|keep-target|use-source|compatible|skip   --apply   --yes",
				"  --exclude <id>   --dry-run   --project <dir>   --global",
			},
			Does: []string{
				"Reads the source agent through its own adapter, normalizes every entry to one intermediate representation, and renders it through the target adapter's writer — so an array-shaped command becomes the target's own command-plus-args layout without any per-pair translator.",
				"Prints the plan first and writes nothing until you approve it: interactively in a terminal, or explicitly with `--apply --yes`. Writes go through the normal install path, so a copy is recorded, listed by `litespm list` and undoable with `install remove` or `restore`.",
			},
			DoesNot: []string{
				"Does not copy plaintext secrets: an environment variable found in the source is reported by NAME under `Needs` and dropped, never written to the target.",
				"Does not modify the source agent, is not a file copy, is not a config diff, and does not keep the two agents in sync afterwards.",
				"Kinds v1 cannot represent are reported with a reason, never silently dropped or converted.",
			},
			Examples: []string{
				"litespm copy --from <source-host> --to <target-host>",
				"litespm copy --from <source-host> --to <target-host> --apply --yes",
				"litespm copy mcp --from <source-host> --to <target-host> --conflict keep-target",
				"litespm copy --from <source-host> --to <target-host> --dry-run",
			},
			SeeAlso: []string{"list", "install", "restore"},
		},
		{
			Canonical: "lock",
			Group:     "MANAGE",
			Purpose:   "Resolve litespm.yml into a deterministic litespm.lock.",
			Synopsis: []string{
				"litespm lock [--check] [--sbom cyclonedx-json|spdx-json] [--verify]",
			},
			Does: []string{
				"Reads the manifest and the locally synced catalog index and writes a byte-identical lock for the same inputs, so committing it and running `litespm lock --check` in CI catches a manifest or catalog that moved without a re-lock.",
				"`--sbom` emits an SBOM from the lock; `--verify` re-checks the recorded lock digest and manifest binding and reports each entry as recorded.",
			},
			DoesNot: []string{
				"Does not fetch anything: an unsynced index must be refreshed with `litespm catalog sync` first.",
				"Never invents a field the catalog does not publish — signature and license stay `none` / `NOASSERTION` until a real check produces a value, and `--verify` never upgrades an entry to green.",
			},
			Examples: []string{
				"litespm lock",
				"litespm lock --check",
				"litespm lock --verify",
				"litespm install <listing-id> --frozen",
			},
			SeeAlso: []string{"install", "catalog"},
		},
		{
			Canonical: "skills",
			Group:     "MANAGE",
			Purpose:   "Add, list, update and remove SKILL.md skills through the skills ledger.",
			Synopsis: []string{
				"litespm skills add <source> [--skill <n>] [--agent <id>] [--global] [--yes]",
				"litespm skills list [--json]",
				"litespm skills update <name>... --source <src> [--ref <r>] [--dry-run]",
				"litespm skills remove <name>|--all [--scope project|global] [--dry-run]",
			},
			Does: []string{
				"`add` copies portable SKILL.md directories into each selected agent's own skills tree and records every directory it created in the skills ledger.",
				"`list` prints that ledger (skill, host, directory); `update` re-copies a named skill from its source; `remove` deletes only directories the ledger names, and reports what it kept.",
			},
			DoesNot: []string{
				"Never guesses a path: a directory the ledger does not name is never removed, and a directory holding files you added afterwards is kept unless you pass `--force`.",
				"Does not audit skill safety: risk data is not collected, so a skill is reported unverified rather than shown as trusted.",
				"Cross-scope removal is refused per entry instead of deleting silently (`remove --all` still respects `--scope`).",
			},
			Examples: []string{
				"litespm skills list",
				"litespm skills add <source> --list",
				"litespm skills remove --all --dry-run",
			},
			SeeAlso: []string{"install", "list", "agent"},
		},
		{
			Canonical: "grant",
			Group:     "MANAGE",
			Purpose:   "Issue, list and revoke the grants that authorize tool invocations.",
			Synopsis: []string{
				"litespm grant <capability-id>",
				"litespm grant list",
				"litespm grant revoke <capability-id>",
			},
			Does: []string{
				"`grant <capability-id>` shows what the tool may do and records your decision, bound to the capability's current input schema; `list` shows recorded grants and `revoke` withdraws one.",
				"Grants are what the policy engine checks before an invocation runs through the bridge.",
			},
			DoesNot: []string{
				"Refuses to run without an interactive terminal: a grant is given by a person, not scripted by the process asking for it.",
				"Does not grant an undiscovered capability (`litespm capabilities refresh` first), and a grant stops matching once the tool's input schema changes — you re-approve it.",
			},
			Examples: []string{
				"litespm grant <capability-id>",
				"litespm grant list",
				"litespm grant revoke <capability-id>",
			},
			SeeAlso: []string{"approve", "invoke", "capabilities"},
		},
		{
			Canonical: "approve",
			Group:     "MANAGE",
			Purpose:   "Approve a stored install plan as a person, in a terminal.",
			Synopsis:  []string{"litespm approve <plan-id>"},
			Does: []string{
				"Shows the stored plan and, when you type yes, records a single-use approval and prints the token an agent passes to `request_install`.",
				"The approval is recorded in state with its origin, so the audit trail says a human approved this exact plan.",
			},
			DoesNot: []string{
				"Refuses to run without an interactive terminal: approvals cannot be scripted by the process that wants them.",
				"Does not execute the plan itself — approval only unblocks the install that asks for it.",
			},
			Examples: []string{
				"litespm approve <plan-id>",
				"litespm approve <plan-id>   # type yes; the printed token is single-use",
			},
			SeeAlso: []string{"grant", "install"},
		},
		{
			Canonical: "restore",
			Group:     "RECOVER",
			Purpose:   "Roll an install back to its exact pre-install bytes, or refuse.",
			Synopsis: []string{
				"litespm restore <installId> [--dry-run]",
				"litespm restore --host <host-id> [--dry-run]",
			},
			Does: []string{
				"Replays the pre-write backup recorded for each write LiteSPM made, so the affected config ends up exactly as it was before the install — formatting and comments included.",
				"`--host` rolls back every install that touched one agent; `--dry-run` prints the plan and writes nothing.",
			},
			DoesNot: []string{
				"Refuses when a node you edited after the install would be overwritten, or when the difference to revert lies outside LiteSPM's own entries; it prints what differs instead.",
				"Never recreates a file the user deleted, and never restores another install's writes.",
			},
			Examples: []string{
				"litespm restore <installId> --dry-run",
				"litespm restore <installId>",
				"litespm restore --host codex",
			},
			SeeAlso: []string{"install", "doctor", "list"},
		},
		{
			Canonical: "doctor",
			Group:     "RECOVER",
			Purpose:   "Run the diagnostic checks and report what is actually true.",
			Synopsis: []string{
				"litespm doctor [--repair] [--yes]",
			},
			Does: []string{
				"Runs the 10 system diagnostics — storage directories, the state database, the operation journal, the CAS store, staging, the credential vault and the rest — and prints each as PASS, WARN or FAIL with a recommendation.",
				"`--repair` fixes what can be fixed, asking first (`--yes` approves non-interactively).",
			},
			DoesNot: []string{
				"Does not start your MCP servers or probe their tools, so it reports configuration and storage health, not runtime health; `litespm capabilities refresh` is what probes servers.",
				"Changes nothing without `--repair`, and never hides a failure to make the report look green.",
			},
			Examples: []string{
				"litespm doctor",
				"litespm doctor --repair",
			},
			SeeAlso:  []string{"capabilities", "list", "restore"},
			ExitNote: "a failed check also returns its category code: 10 catalog · 20 resolve · 30 approval · 40 install · 50 provider · 60 host · 70 state; the most severe failed category wins (ARCH/20).",
		},
		{
			Canonical: "uninstall",
			Group:     "RECOVER",
			Purpose:   "Remove LiteSPM's bridge entry from every agent config it touched.",
			Synopsis:  []string{"litespm uninstall [--dry-run]"},
			Does: []string{
				"Plans first with `--dry-run`, then strips the LiteSPM bridge entry from each agent config, keeping a pre-edit backup and leaving sibling entries untouched.",
				"Prints what is still on disk afterwards, with the exact commands that deal with it.",
			},
			DoesNot: []string{
				"Does not delete skill directories — they may hold files you added, so they are reported, and `litespm skills remove` is the explicit opt-in.",
				"Does not delete the backups it wrote; those are your restore points.",
			},
			Examples: []string{
				"litespm uninstall --dry-run",
				"litespm uninstall",
			},
			SeeAlso: []string{"host", "skills", "restore"},
		},
		{
			Canonical: "setup",
			Aliases:   []string{"init"},
			Group:     "CONFIGURE",
			Purpose:   "Run the interactive setup wizard.",
			Synopsis:  []string{"litespm setup", "litespm init     # same command"},
			Does: []string{
				"Walks you through choosing an agent, detects its configuration file, and registers the LiteSPM bridge entry — showing what will change before it changes it.",
				"If the config file is not where detection expects, the wizard offers a manual path, a copy-paste snippet and a retry.",
			},
			DoesNot: []string{
				"Does not run non-interactively: it has no flags and needs a terminal.",
				"Does not install packages — it only wires the bridge. `litespm install` installs capabilities.",
			},
			Examples: []string{
				"litespm setup",
				"litespm host detect",
			},
			SeeAlso: []string{"host", "daemon", "install"},
		},
		{
			Canonical: "host",
			Group:     "CONFIGURE",
			Purpose:   "Inspect and configure the agent host adapters.",
			Synopsis: []string{
				"litespm host list",
				"litespm host detect",
				"litespm host setup <host-id>",
				"litespm host remove <host-id> | --all",
			},
			Does: []string{
				"`list` prints every supported agent adapter and the config file it reads; `detect` scans this machine and reports which configs exist and whether the LiteSPM bridge is registered in each.",
				"`setup <host-id>` plans and applies the bridge entry (with a backup); `remove <host-id>` or `remove --all` takes it back out.",
			},
			DoesNot: []string{
				"`list` and `detect` are read-only; `remove` keeps the pre-edit backup and never deletes the rest of your config.",
				"Detection only looks at the documented config file of each adapter — a project-scope or undocumented location is not scanned.",
			},
			Examples: []string{
				"litespm host list",
				"litespm host detect",
				"litespm host setup <host-id>",
			},
			SeeAlso: []string{"bridge", "install", "list"},
		},
		{
			Canonical: "daemon",
			Group:     "CONFIGURE",
			Purpose:   "Run the LiteSPM background supervisor and IPC engine.",
			Synopsis:  []string{"litespm daemon serve"},
			Does: []string{
				"Recovers any interrupted install from the operation journal, then serves the IPC endpoint the bridge and agents call for search, plan, approval and invocation.",
				"Runs in the foreground so your terminal or process supervisor owns its lifetime.",
			},
			DoesNot: []string{
				"Does not install itself as an operating-system service: starting it is up to you.",
				"Does not install packages or edit agent configs; those effects belong to `install`, `copy` and `setup`.",
			},
			Examples: []string{
				"litespm daemon serve",
				"litespm daemon serve >> litespm.log 2>&1   # keep the log",
			},
			SeeAlso: []string{"bridge", "setup", "invoke"},
		},
		{
			Canonical: "bridge",
			Group:     "CONFIGURE",
			Purpose:   "Serve the stdio MCP bridge an agent host launches.",
			Synopsis:  []string{"litespm bridge stdio [--host <host-id>]"},
			Does: []string{
				"Speaks MCP over stdio on the agent's behalf and forwards each request to the running daemon: catalog search, plans, `list_installed`, skill loading and capability invocation.",
				"Without the daemon it still starts, and says so, so an agent sees an honest empty bridge instead of a hang.",
			},
			DoesNot: []string{
				"Does not persist anything itself, and does not health-check installed capabilities — an unobserved status is rendered unknown, never Ready.",
				"Does not start the daemon for you.",
			},
			Examples: []string{
				"litespm bridge stdio --host codex",
				"litespm bridge stdio   # --host defaults to generic",
			},
			SeeAlso: []string{"daemon", "host", "capabilities"},
		},
		{
			Canonical: "self-update",
			Aliases:   []string{"update"},
			Group:     "SYSTEM",
			Purpose:   "Check for a newer LiteSPM binary and replace this one atomically.",
			Synopsis:  []string{"litespm self-update [--force] [--help]"},
			Does: []string{
				"Fetches the published release manifest, and when a newer version exists downloads the release ASSET (never the release page), verifies its SHA-256 checksum, and replaces the running executable atomically.",
				"`--force` reinstalls or downgrades even when the manifest version is not newer.",
			},
			DoesNot: []string{
				"Does not update installed packages or skills — that is `litespm skills update`.",
				"Refuses to fall back to an unverified download when signature verification is required and unavailable.",
			},
			Examples: []string{
				"litespm self-update",
				"litespm self-update --force",
			},
			SeeAlso: []string{"version", "skills"},
		},
		{
			Canonical: "version",
			Aliases:   []string{"--version", "-v"},
			Group:     "SYSTEM",
			Purpose:   "Print the LiteSPM version and build details.",
			Synopsis:  []string{"litespm version", "litespm --version", "litespm -v"},
			Does: []string{
				"Prints the LiteSPM version with the protocol version, operating system, architecture and Go toolchain of this binary.",
				"It is the pair of numbers to quote in a bug report: the release you run and the protocol it speaks to agents and the daemon.",
			},
			DoesNot: []string{
				"Does not contact the network: it cannot tell you whether an update exists (that is `litespm self-update`).",
				"Reads and changes nothing on disk.",
			},
			Examples: []string{
				"litespm version",
				"litespm --version",
			},
			SeeAlso: []string{"self-update", "doctor"},
		},
		{
			Canonical: "capabilities",
			Aliases:   []string{"caps"},
			Group:     "SYSTEM",
			Purpose:   "List, search and describe the tools discovered from installed MCP servers.",
			Synopsis: []string{
				"litespm capabilities list [--host <id>]... [--limit <n>]",
				"litespm capabilities search <query> [--host <id>]... [--limit <n>]",
				"litespm capabilities describe --capability <id>",
				"litespm capabilities refresh [--host <id>]...",
			},
			Does: []string{
				"`refresh` starts each installed MCP server LiteSPM registered and records the tools it actually exposes; `list` and `search` read those recorded rows; `describe` prints one tool's input schema and the command behind it.",
				"`--host` restricts the probe to named agents (repeatable); the default is every agent with a verified bridge entry.",
			},
			DoesNot: []string{
				"Nothing is probed until you run `refresh`: an empty list means nothing has been discovered yet, not that you have no tools.",
				"Does not invoke tools — that is `litespm invoke` — and a server that will not start is reported per server, without abandoning the others.",
			},
			Examples: []string{
				"litespm capabilities refresh",
				"litespm capabilities list",
				"litespm capabilities search github",
			},
			SeeAlso: []string{"invoke", "grant", "doctor"},
		},
		{
			Canonical: "invoke",
			Group:     "SYSTEM",
			Purpose:   "Call one discovered capability from the terminal.",
			Synopsis:  []string{"litespm invoke <capability-id> [--arguments '<json>']"},
			Does: []string{
				"Calls the tool through the same path the bridge uses, after re-checking that the tool's input schema still matches what was granted — so the terminal and an agent cannot behave differently.",
				"Prints the tool's own output and exits non-zero when the tool reported an error.",
			},
			DoesNot: []string{
				"Does not bypass policy: without an engine that can enforce your rules the invocation is refused, never silently allowed.",
				"Does not accept malformed arguments: `--arguments` must be valid JSON.",
			},
			Examples: []string{
				"litespm invoke <capability-id>",
				"litespm invoke <capability-id> --arguments '{\"q\":\"lite\"}'",
			},
			SeeAlso: []string{"capabilities", "grant"},
		},
		{
			Canonical: "help",
			Aliases:   []string{"--help", "-h"},
			Group:     "HELP",
			Purpose:   "Explain the commands, or one command, or the vocabulary.",
			Synopsis: []string{
				"litespm help",
				"litespm help <command>",
				"litespm help concepts",
			},
			Does: []string{
				"`litespm help` prints the quick start and the shipped commands, grouped by what they are for.",
				"`litespm help <command>` prints one command's usage, what it does, what it does not, examples, exit codes and related commands; `litespm help concepts` defines the vocabulary these pages use.",
			},
			DoesNot: []string{
				"Documents only commands that dispatch: a verb that does not exist cannot appear here, and an unknown target prints the shipped list and exits 2 rather than pretending to know it.",
				"There is no `litespm help error <code>` yet — error families are documented in ARCH/20.",
			},
			Examples: []string{
				"litespm help",
				"litespm help install",
				"litespm help concepts",
			},
			SeeAlso: []string{"list", "inventory"},
		},
	}

	cat := make(map[string]commandHelp, len(pages)+8)
	for _, page := range pages {
		cat[page.Canonical] = page
		for _, alias := range page.Aliases {
			cat[alias] = page
		}
	}
	return cat
}

// runHelp is the dispatch entry point for `litespm help`/`--help`/`-h`.
func runHelp(args []string) {
	if code := helpTo(os.Stdout, os.Stderr, args); code != 0 {
		os.Exit(code)
	}
}

// helpTo renders against explicit writers so tests can capture both streams
// and assert the exit code: 0 for a page that exists, 2 for an unknown target
// (usage), never 0 for a name LiteSPM does not ship.
func helpTo(out, errOut io.Writer, args []string) int {
	if len(args) > 1 {
		fmt.Fprintf(errOut, "help: at most one target, got %d\n\n", len(args))
		renderShippedList(errOut)
		return 2
	}
	if len(args) == 0 {
		renderDefaultHelp(out)
		return 0
	}
	target := args[0]
	if strings.EqualFold(target, "concepts") {
		renderConcepts(out)
		return 0
	}
	if page, ok := helpCatalog[target]; ok {
		renderHelp(out, page)
		return 0
	}
	fmt.Fprintf(errOut, "Unknown help target %q. Shipped commands:\n\n", target)
	renderShippedList(errOut)
	fmt.Fprintln(errOut)
	fmt.Fprintln(errOut, "Run `litespm help concepts` for the vocabulary.")
	return 2
}

// printUsage lives in main.go and is a thin alias of renderDefaultHelp.

// renderDefaultHelp writes the educational front page (ARCH/38 §3.1): a quick
// start, the shipped commands grouped by purpose, real examples, and pointers
// to `help <command>` and `help concepts`.
func renderDefaultHelp(w io.Writer) {
	fmt.Fprint(w, `LiteSPM — the package manager for AI agents.
Discover, install, port and manage MCP servers, skills and plugins across
the agent hosts LiteSPM supports (`+"`litespm host list`"+` prints them).

QUICK START
  litespm host detect          which agent hosts this machine has
  litespm search github        find capabilities in the local index
  litespm list                 see what is deployed here
  litespm doctor               check the setup and how to fix it

`)
	for _, g := range helpGroups {
		fmt.Fprintf(w, "%-12s%s\n", g.Label, strings.Join(g.Entries, " · "))
	}
	fmt.Fprint(w, `
EXAMPLES
  litespm search github
  litespm list --json
  litespm skills list
  litespm help concepts

Run `+"`litespm help <command>`"+` for detail on one command, `+"`litespm help concepts`"+`
for the vocabulary. Exit codes: 0 success · 1 refused or failed · 2 usage.

Documentation: https://github.com/sarv-projects/litespm
`)
}

// renderShippedList prints the grouped command names; it is what an unknown
// help target falls back to.
func renderShippedList(w io.Writer) {
	for _, g := range helpGroups {
		fmt.Fprintf(w, "  %-12s%s\n", g.Label, strings.Join(g.Entries, " · "))
	}
}

// renderHelp writes one command's page (ARCH/38 §3.1 template).
func renderHelp(w io.Writer, page commandHelp) {
	fmt.Fprintf(w, "LITESPM %s\n\n", strings.ToUpper(page.Canonical))
	if page.Purpose != "" {
		fmt.Fprintln(w, page.Purpose)
		fmt.Fprintln(w)
	}
	if len(page.Synopsis) > 0 {
		fmt.Fprintln(w, "USAGE")
		for _, line := range page.Synopsis {
			// A synopsis line the terminal can take whole keeps its own
			// alignment (flag columns are part of its meaning); only longer
			// lines are folded.
			if pre := "  " + line; len(pre) <= 78 {
				fmt.Fprintln(w, pre)
				continue
			}
			writeWrapped(w, "  ", line)
		}
		fmt.Fprintln(w)
	}
	writeSection(w, "WHAT IT DOES", page.Does)
	writeSection(w, "WHAT IT DOES NOT", page.DoesNot)
	if len(page.Examples) > 0 {
		fmt.Fprintln(w, "EXAMPLES")
		for i, ex := range page.Examples {
			writeWrapped(w, fmt.Sprintf("  %d. ", i+1), ex)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "EXIT CODES")
	fmt.Fprintln(w, "  0  success, including nothing to do")
	fmt.Fprintln(w, "  1  refused or failed")
	fmt.Fprintln(w, "  2  usage error (unknown flag, missing argument, unknown command)")
	if page.ExitNote != "" {
		writeWrapped(w, "  ", page.ExitNote)
	}
	fmt.Fprintln(w)
	if len(page.SeeAlso) > 0 {
		fmt.Fprintln(w, "SEE ALSO")
		writeWrapped(w, "  ", strings.Join(page.SeeAlso, " · "))
	}
}

func writeSection(w io.Writer, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintln(w, title)
	for _, item := range items {
		writeWrapped(w, "  - ", item)
	}
	fmt.Fprintln(w)
}

// writeWrapped prints text prefixed by indent, folding at 78 columns with a
// hanging indent so long sentences stay readable in a terminal.
func writeWrapped(w io.Writer, indent, text string) {
	const width = 78
	words := strings.Fields(text)
	if len(words) == 0 {
		fmt.Fprintln(w, strings.TrimRight(indent, " "))
		return
	}
	line := indent
	for i, word := range words {
		if i > 0 && len(line)+1+len(word) > width {
			fmt.Fprintln(w, strings.TrimRight(line, " "))
			line = strings.Repeat(" ", len(indent))
		}
		if i > 0 {
			line += " "
		}
		line += word
	}
	fmt.Fprintln(w, line)
}

// renderConcepts writes the §3.3 glossary: user language first, the exact
// labels one level deeper.
func renderConcepts(w io.Writer) {
	fmt.Fprint(w, `LiteSPM concepts

The words `+"`litespm list`"+`, `+"`litespm inventory`"+` and every help page use.

PACKAGE
  A thing you install from the catalog: one row with an id such as
  mcp:<source>:<name>, a version and a publisher. A package may contain more
  than one piece (a plugin can bundle a skill, an MCP server and a hook).

CAPABILITY
  something your agent can DO. A tool such as "search the web" or "read a
  file" is a capability. `+"`litespm capabilities list`"+` shows the ones
  discovered from your installed MCP servers.

AGENT
  the AI coding tool you actually work with. LiteSPM configures agents; it is
  not one. Each supported agent reads its own config file, and LiteSPM's
  adapters know where each of them keeps it (`+"`litespm host list`"+`).

MCP SERVER
  a small service an agent can talk to through the Model Context Protocol —
  this is what lets an AI work with services such as GitHub, databases and
  browsers. In an agent's config it appears as one named entry with a command
  and arguments.

SKILL
  a portable set of instructions (a SKILL.md directory) that teaches an agent
  how to do a kind of task. LiteSPM copies it into the agent's own skills
  tree and records the directory in the skills ledger.

PLUGIN
  a bundle of several of the pieces above (skill, MCP server, rules, hooks)
  shipped as one package. Plugin installs are not available yet: they fail
  with LPSM-ARTIFACT-UNAVAILABLE rather than installing half a bundle.

BRIDGE
  the one entry LiteSPM registers in an agent's config. The agent starts
  `+"`litespm bridge stdio`"+` when it needs a capability, and the bridge
  forwards to the LiteSPM daemon. One entry serves every managed package, so
  installing does not grow your config.

SCOPE
  user (also called global) means it applies to everything you run on this
  machine; project means it applies to one checkout of a project. `+"`--global`"+`
  and `+"`--project <dir>`"+` select between them.

MANAGED / OBSERVED / ADOPTED
  managed: LiteSPM controls its lifecycle — it wrote it, and `+"`install remove`"+`
  or `+"`restore`"+` can take it out again.
  observed: LiteSPM FOUND it in an agent's own config and reports it read-only;
  it never edits or removes an observed entry.
  adopted: you asked LiteSPM to take over an existing entry (via `+"`copy`"+`),
  so it is managed now, with its origin recorded as adopted — never as
  catalog-verified.

CAPABILITY vs DEPLOYMENT
  a capability is the thing itself; a deployment is one copy of it living in
  one place. The same skill in three agents is ONE capability and THREE
  deployments, which is why `+"`litespm list`"+` counts both, like this:
  18 unique capabilities · 27 deployments.

REFERENCE vs VALUE
  a reference is the NAME of an environment variable (GITHUB_TOKEN); a value
  is what the variable holds. LiteSPM moves references only: `+"`install --env`"+`
  records ${GITHUB_TOKEN} in a config, and a literal secret found in a source
  is reported by name and dropped, never written anywhere.

Drift, and the quiet cells:
  drifted means the recorded write and what is on disk no longer agree —
  LiteSPM reports it and changes nothing. broken means something recorded is
  missing. A cell the data cannot answer shows `+"—`"+` or "not knowable";
  LiteSPM never fills it in with a plausible guess.
`)
}
