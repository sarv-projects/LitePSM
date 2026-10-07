import urllib.error
import urllib.request
import re
import json
import os
import sys
import time

sys.stdout.reconfigure(encoding='utf-8')

# FETCH SNAPSHOT LAYER
# Every upstream fetch goes through scripts/snapshot_store.py: raw bytes are
# recorded as a durable source snapshot (ARCH/03 §4 record shape) BEFORE any
# parsing, and the parse below always reads the recorded bytes -- a re-run
# replays the recording without touching the network, `--refresh` issues a
# conditional request (If-None-Match) and only replaces a recording when the
# server says the content changed. A recording whose digest does not match its
# bytes is refused, never used; a fetch that failed records nothing (the
# failure goes to failures.jsonl); an ETag-less endpoint still snapshots and
# refresh compares digests only.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import snapshot_store

# UPSTREAM FEEDS: (URL, sourceId) pairs. The sourceIds follow the
# domain.SourceID grammar (`^[a-z0-9_-]{3,32}:[a-z0-9_-]{3,64}$`); the git:*
# ids are the ones already registered in internal/source/sources.go.
FEED_AWESOME_MCP = (
    "https://raw.githubusercontent.com/punkpeye/awesome-mcp-servers/main/README.md",
    "feed:punkpeye-awesome-mcp-servers",
)
FEED_AWESOME_SKILLS = (
    "https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md",
    "feed:voltagent-awesome-agent-skills",
)
FEED_OFFICIAL_SERVERS = (
    "https://raw.githubusercontent.com/modelcontextprotocol/servers/main/README.md",
    "feed:modelcontextprotocol-servers",
)

# NOTE ON POPULARITY DATA
# This builder deliberately publishes NO star / download / popularity figures.
# The upstream sources it reads (awesome-mcp-servers markdown lists, skills
# directories) do not expose machine-readable star counts, and an earlier
# revision of this file invented them -- a hard-coded table plus a
# `hash(slug)`-derived fallback. That produced numbers that looked like real
# adoption data, ranked the public index by them, and mis-attributed a
# 41,200-star plugin to this project's own repository.
#
# The rule is: publish a signal only when it is measured. `stars` is therefore
# null everywhere, and the web UI renders "not published" rather than a number.
# If a real star signal is ever ingested from an API, set it here and nowhere
# else.

# HOST COMPATIBILITY
# A capability's host list must come from a registry, not from a constant typed
# into this file. An earlier revision stamped the same seven host names onto
# every one of 4,079 MCP servers and six onto every skill, so the public site
# reported "9 agent hosts" while the binary shipped 50 adapters -- the catalog
# understated the product by an order of magnitude, and the per-row claim was
# asserted rather than derived.
#
# The two registries are different things and are kept apart:
#   hosts.json          bridge adapters: hosts whose config file we can edit.
#                       Every MCP server is installable into all of them.
#   skill-targets.json  hosts with a skills directory we can write SKILL.md to.
#
# Both are generated from the Go source by scripts/gen_hosts_ts.go, so the
# catalog cannot drift from the binary.

def _load_registry(name, field):
    path = os.path.join(os.path.dirname(__file__), "..", "web", "data", name)
    try:
        with open(path, "r", encoding="utf-8") as f:
            rows = json.load(f)
    except (OSError, ValueError) as exc:
        print(f"  WARNING: {name} unreadable ({exc}); host lists will be empty")
        return []
    return [r[field] for r in rows if r.get(field)]


BRIDGE_HOSTS = _load_registry("hosts.json", "name")
SKILL_TARGET_HOSTS = _load_registry("skill-targets.json", "displayName")


# LISTING ID GRAMMAR
# The Go domain requires every listing id to match
#   RegexListingID = ^(plugin|mcp|skill|...):[a-z0-9_:-]+:[A-Za-z0-9_.~%+-]+$
# (internal/domain/identifiers.go). An earlier revision interpolated raw
# upstream names into ids, which shipped six invalid ones: two MCP servers
# whose names carried a scraped "?tab=readme-ov-file" query string, and four
# skills whose names contained spaces or an apostrophe. Downstream, every Go
# consumer rejects those ids, so the fix belongs here -- at the source -- and
# every id-construction site funnels through canonical_id().
_UPSTREAM_INVALID = re.compile(r"[^A-Za-z0-9_.~%+-]+")


def canonical_slug(text):
    """Normalize an upstream name into the listing-id upstream grammar.

    Strips URL query/fragment junk and apostrophes first (the two defects
    that shipped invalid ids), then maps every remaining character outside
    ``[A-Za-z0-9_.~%+-]`` to '-'. Idempotent: a grammar-conforming input
    returns unchanged.
    """
    s = str(text).strip().split("?", 1)[0].split("#", 1)[0]
    s = s.replace("'", "")
    s = _UPSTREAM_INVALID.sub("-", s)
    s = re.sub(r"-{2,}", "-", s).strip("-")
    return s


def canonical_id(kind, source, upstream):
    tail = canonical_slug(upstream)
    if not tail:
        raise ValueError(f"empty upstream id for {kind}:{source} (from {upstream!r})")
    return f"{kind}:{source.lower()}:{tail}"


def clean_desc(text):
    # Remove markdown badges [![...](...)]
    text = re.sub(r'\[\!\[[^\]]*\]\([^\)]*\)\](?:\([^\)]*\))?', '', text)
    # Remove standalone markdown links like [★ 1.2k](...)
    text = re.sub(r'\[[★⭐][^\]]*\]\([^\)]*\)', '', text)
    # Remove emojis that clutter description
    text = re.sub(r'[🐍📇🦀🐹☁️🏠🍎🪟🐧🎖️✨🔥⚡🚀💡🤖🛠️📦]+', '', text)
    text = text.strip()
    if text.startswith('-'):
        text = text[1:].strip()
    if text.startswith(':'):
        text = text[1:].strip()
    return text.strip()

# INSTALLABILITY / PROVENANCE
# Every row carries an `installability` class (domain.Installability):
#   discovery_only    heuristic ingestion (awesome-list markdown, the official
#                     servers README). The upstream names a repository, not a
#                     package: no authoritative manifest proved a version, a
#                     package coordinate, or a launch line. These rows are
#                     searchable metadata only; `version`, `command` and `args`
#                     are null and the installer refuses them (LPSM-NOT-INSTALLABLE).
#   metadata_verified a vendor's own marketplace manifest supplied the row.
# An earlier revision stamped version "1.0.0" on every row and guessed a launch
# line from an emoji ("npx -y {repo}-mcp", "uvx {repo}", "cargo run"). Those
# commands named packages that mostly do not exist; installing one would have
# executed an attacker-registerable npm/PyPI name. Nothing here guesses one now.
#
# `publisher.verified` is likewise no longer a hand-typed allowlist. It is true
# only when the row was read from the publisher's own repository manifest
# (`publisher.provenance == "vendor-manifest"`); awesome-list rows are
# `provenance == "awesome-list-claim"` and never verified. Neither is a LiteSPM
# security audit.
PROV_VENDOR = "vendor-manifest"
PROV_LIST = "awesome-list-claim"
DISCOVERY_ONLY = "discovery_only"
METADATA_VERIFIED = "metadata_verified"


def publisher_obj(name, url, provenance):
    return {
        "name": name,
        "verified": provenance == PROV_VENDOR,
        "provenance": provenance,
        "url": url,
    }


CATEGORY_MAPPING = {
    "databases": "Databases",
    "developer tools": "Developer Tools",
    "browser automation": "Browser Automation",
    "workplace & productivity": "Productivity & Workflow",
    "productivity": "Productivity & Workflow",
    "cloud platforms": "Cloud Infrastructure",
    "security": "Security & Testing",
    "finance & fintech": "Finance & Crypto",
    "finance": "Finance & Crypto",
    "search & data extraction": "Search & Retrieval",
    "search": "Search & Retrieval",
    "knowledge & memory": "Knowledge & Memory",
    "communication": "Communication",
    "coding agents": "Developer Tools",
    "aggregators": "Developer Tools",
    "marketing": "Productivity & Workflow",
    "multimedia": "Multimedia",
    "file systems": "Developer Tools",
    "version control": "Developer Tools",
    "official core": "Official Core",
    "plugins & toolkits": "Plugins & Toolkits",
    "development": "Developer Tools",
    "database": "Databases",
    "monitoring": "Monitoring",
    "observability": "Monitoring",
    "design": "Multimedia",
    "creativity": "Multimedia",
    "deployment": "Cloud Infrastructure",
    "education & research": "Education & Research",
    "education": "Education & Research",
    "research": "Education & Research",
    "learning": "Education & Research",
    "automation": "Productivity & Workflow",
    "testing": "Security & Testing",
    "migration": "Developer Tools",
    "location": "Location Services",
}

def clean_category(cat_str):
    if not cat_str:
        return "Developer Tools"
    # Remove HTML anchor tags
    cat = re.sub(r'<[^>]+>', '', cat_str)
    # Remove malformed anchor fragments
    cat = re.sub(r'a\s+name=[\'"][^\'"]*[\'"]\s*>?(?:<\/a>)?', '', cat)
    # Remove emojis and symbols
    cat = re.sub(r'[🐍📇🦀🐹☁️🏠🍎🪟🐧🎖️✨🔥⚡🚀💡🤖🛠️📦🗄️🔍🔒🌐💼📊💳🎮🏷️]+', '', cat)
    # Strip leading non-alphanumeric
    cat = re.sub(r'^[^\w]+', '', cat).strip()
    
    cat_lower = cat.lower()
    for k, v in CATEGORY_MAPPING.items():
        if k in cat_lower:
            return v
    return cat if cat else "Developer Tools"

def _http_get(url, timeout, etag=None):
    """GET `url`; return (status, headers, body).

    A conditional request answering 304 comes back as status 304 with an empty
    body whether urllib raises it (HTTPError) or returns it directly.
    """
    headers = {"User-Agent": "Mozilla/5.0"}
    if etag:
        headers["If-None-Match"] = etag
    req = urllib.request.Request(url, headers=headers)
    try:
        resp = urllib.request.urlopen(req, timeout=timeout)
    except urllib.error.HTTPError as exc:
        if exc.code == 304:
            return 304, exc.headers, b""
        raise
    try:
        status = resp.getcode()
        if status == 304:
            return 304, resp.headers, b""
        return status, resp.headers, resp.read()
    finally:
        resp.close()


def _record_snapshot(root, url, source_id, status, headers, body, started_at):
    """Persist a fetched response as the snapshot this run will parse from."""
    rec = snapshot_store.write_snapshot(
        root, url, source_id,
        headers.get("ETag") if headers is not None else None,
        status, body, started_at=started_at,
    )
    etag_note = ", etag recorded" if rec["fetch"]["etag"] else ", digest-only"
    print(f"    recorded {rec['snapshot']['snapshotId']} "
          f"({rec['fetch']['byteSize']} bytes{etag_note})")
    return rec


def obtain(url, source_id, timeout, refresh, label):
    """Return the bytes this build normalizes from -- always the recorded
    snapshot, never a live response.

    Replay (default): use the recording; fetch and record only what is
    missing. Refresh: conditional request with the recorded ETag; a 304 or a
    200 whose content digest matches keeps the recording, a 200 with new
    bytes replaces it, and a network failure keeps the recorded bytes while
    logging the failure (failures.jsonl) and printing an explicit STALE
    notice. A corrupt recording is fatal on a replay run (refused, not used)
    and replaced on a refresh run.
    """
    root = snapshot_store.default_root()
    rec_body = rec_meta = None
    try:
        rec_body, rec_meta = snapshot_store.load_snapshot(root, url)
    except snapshot_store.SnapshotMissing:
        pass
    except snapshot_store.SnapshotIntegrityError as exc:
        if not refresh:
            raise
        print(f"    {label}: recorded snapshot REFUSED ({exc}); fetching a replacement")
        snapshot_store.log_failure(root, url, "snapshot-integrity", str(exc))
        rec_body = rec_meta = None

    if not refresh:
        if rec_meta is not None:
            print(f"    {label}: replay {rec_meta['snapshot']['snapshotId']} "
                  f"(recorded {rec_meta['fetch']['fetchedAt']})")
            return rec_body
        print(f"    {label}: no recorded snapshot; fetching")
        started_at = time.time()
        try:
            status, headers, body = _http_get(url, timeout)
        except Exception as exc:
            snapshot_store.log_failure(root, url, "fetch", str(exc))
            raise
        _record_snapshot(root, url, source_id, status, headers, body, started_at)
        return body

    etag = rec_meta["fetch"].get("etag") if rec_meta is not None else None
    started_at = time.time()
    try:
        status, headers, body = _http_get(url, timeout, etag)
    except Exception as exc:
        if rec_meta is None:
            snapshot_store.log_failure(root, url, "refresh", str(exc))
            raise
        print(f"    {label}: STALE -- refresh failed ({exc}); keeping snapshot "
              f"recorded {rec_meta['fetch']['fetchedAt']}")
        snapshot_store.log_failure(root, url, "refresh", str(exc))
        return rec_body
    if status == 304:
        if rec_meta is None:
            # Nothing recorded: a 304 gives us no bytes to normalize from, and
            # inventing an entry we did not fetch is forbidden.
            raise snapshot_store.SnapshotIntegrityError(
                f"upstream answered 304 for {url} but no snapshot is recorded")
        print(f"    {label}: upstream unchanged (304); snapshot kept")
        return rec_body
    if rec_meta is not None and \
            snapshot_store.digest_bytes(body) == rec_meta["snapshot"]["contentDigest"]:
        print(f"    {label}: content unchanged (digest match); snapshot kept")
        return rec_body
    if rec_meta is not None:
        print(f"    {label}: upstream content changed; replacing snapshot")
    else:
        print(f"    {label}: recording snapshot")
    _record_snapshot(root, url, source_id, status, headers, body, started_at)
    return body


def build_full_catalog(refresh=False):
    print("Ingesting real registries...")
    snap_root = snapshot_store.default_root()
    if refresh:
        print("Mode: refresh -- conditional requests; recorded snapshots are "
              "replaced only when upstream content changes")
    else:
        print("Mode: replay -- recorded snapshots are used as-is; the network "
              "is touched only for missing entries")
    print(f"Snapshot store: {snap_root}")

    mcp_url, mcp_source = FEED_AWESOME_MCP
    skills_url, skills_source = FEED_AWESOME_SKILLS
    official_url, official_source = FEED_OFFICIAL_SERVERS

    # 1. punkpeye/awesome-mcp-servers
    print("1/3 punkpeye/awesome-mcp-servers...")
    mcp_md = obtain(mcp_url, mcp_source, 25, refresh,
                    "awesome-mcp-servers").decode("utf-8", errors="ignore")

    # 2. VoltAgent/awesome-agent-skills
    print("2/3 VoltAgent/awesome-agent-skills...")
    skills_md = obtain(skills_url, skills_source, 25, refresh,
                       "awesome-agent-skills").decode("utf-8", errors="ignore")

    # 3. modelcontextprotocol/servers -- tolerating an upstream failure on a
    #    FIRST fetch (published behaviour: this feed may be absent), but never
    #    tolerating a corrupt recording: a refused snapshot aborts the build.
    print("3/3 modelcontextprotocol/servers...")
    try:
        official_raw = obtain(official_url, official_source, 15, refresh,
                              "official servers")
    except snapshot_store.SnapshotIntegrityError:
        raise
    except Exception:
        official_raw = None
    official_mcp_md = official_raw.decode("utf-8", errors="ignore") if official_raw else ""

    items = []
    seen_ids = set()
    # (url, sourceId, rows this run contributed) -- used to finalize each
    # recorded snapshot with its real itemCount after a successful parse.
    row_counts = []

    # --- Parse Official MCP Servers ---
    print("Parsing official MCP servers...")
    official_cat = "Official Core"
    rows_before = len(items)
    for line in official_mcp_md.splitlines():
        line_s = line.strip()
        if line_s.startswith("- [") and "github.com/modelcontextprotocol/servers" in line_s:
            # - [SQLite](src/sqlite) - Query SQLite database
            m = re.match(r'-\s+\[([^\]]+)\]\(([^)]+)\)\s*[-–—:]\s*(.+)', line_s)
            if m:
                name, path, desc = m.groups()
                slug = canonical_slug(name.lower())
                item_id = canonical_id("mcp", "modelcontextprotocol", slug)
                if item_id not in seen_ids:
                    seen_ids.add(item_id)
                    items.append({
                        "id": item_id,
                        "name": f"{name} MCP Server",
                        "slug": slug,
                        "kind": "mcp",
                        "summary": desc.strip(),
                        "category": official_cat,
                        "publisher": publisher_obj(
                            "modelcontextprotocol",
                            "https://github.com/modelcontextprotocol/servers",
                            PROV_VENDOR,
                        ),
                        # The README links a source directory, not a published
                        # package: the npm name was previously guessed from the
                        # slug. No manifest proved one, so there is no launch line.
                        "transport": None,
                        "runtime": None,
                        "stars": None,
                        "version": None,
                        "command": None,
                        "args": None,
                        "installability": DISCOVERY_ONLY,
                    })
    row_counts.append((official_url, official_source, len(items) - rows_before))

    # --- Parse punkpeye/awesome-mcp-servers ---
    print(f"Parsing punkpeye servers...")
    current_category = "Developer Tools"
    rows_before = len(items)
    for line in mcp_md.splitlines():
        raw_line = line.strip()
        if raw_line.startswith("### ") or raw_line.startswith("## "):
            cat_candidate = raw_line.lstrip("#").strip()
            # filter out non-categories
            if not any(k in cat_candidate.lower() for k in ["table of contents", "contents", "license", "contributing", "awesome", "sponsor"]):
                cat_cleaned = clean_category(cat_candidate)
                if cat_cleaned:
                    current_category = cat_cleaned
            continue

        if raw_line.startswith("- ["):
            # Format: - [owner/repo](https://github.com/owner/repo) [![...](...)] 🐍 ☁️ - Description
            m = re.match(r'-\s+\[([^\]]+)\]\((https?://github\.com/([^/]+)/([^/\)#]+)[^\)]*)\)(.*)', raw_line)
            if m:
                label, gh_url, owner, repo, rest = m.groups()
                slug = canonical_slug(repo.lower().replace(".", "-").replace("_", "-"))
                item_id = canonical_id("mcp", owner, slug)
                if item_id in seen_ids:
                    continue
                seen_ids.add(item_id)

                # Determine runtime and transport from emojis in line
                runtime = "typescript"
                if "🐍" in rest:
                    runtime = "python"
                elif "🦀" in rest:
                    runtime = "rust"
                elif "🐹" in rest:
                    runtime = "go"

                # The cloud/house emoji is the list author's claim about where
                # the server runs, not a transport. It is kept only as a hint
                # and never as `transport` (see migrate_dataset_transport.py).
                upstream_transport_hint = None
                if "☁️" in rest and "🏠" not in rest:
                    upstream_transport_hint = "sse"

                desc = clean_desc(rest)
                if not desc:
                    desc = f"Model Context Protocol server for {label}."

                items.append({
                    "id": item_id,
                    "name": label if "/" not in label else repo,
                    "slug": slug,
                    "kind": "mcp",
                    "summary": desc,
                    "category": current_category,
                    "publisher": publisher_obj(owner, f"https://github.com/{owner}", PROV_LIST),
                    # No manifest was read: transport, version, command and args
                    # are unknown, not defaulted.
                    "transport": None,
                    **({"upstreamTransportHint": upstream_transport_hint} if upstream_transport_hint else {}),
                    "runtime": runtime,
                    "stars": None,
                    "version": None,
                    "command": None,
                    "args": None,
                    "installability": DISCOVERY_ONLY,
                })
    row_counts.append((mcp_url, mcp_source, len(items) - rows_before))

    # --- Parse VoltAgent/awesome-agent-skills ---
    print(f"Parsing VoltAgent skills...")
    skill_category = "Agent Skills"
    rows_before = len(items)
    for line in skills_md.splitlines():
        raw_line = line.strip()
        if raw_line.startswith("### ") or raw_line.startswith("## "):
            cat_candidate = raw_line.lstrip("#").strip()
            if not any(k in cat_candidate.lower() for k in ["table of contents", "contents", "license", "contributing", "awesome", "sponsor", "paths for other", "quality standards"]):
                cat_cleaned = re.sub(r'^[^\w\s]+', '', cat_candidate).strip()
                if cat_cleaned:
                    skill_category = cat_cleaned
            continue

        if raw_line.startswith("- **[") or raw_line.startswith("- ["):
            # e.g. - **[anthropics/docx](https://officialskills.sh/anthropics/skills/docx)** - Create, edit...
            m = re.match(r'-\s+(?:\*\*\[|\[)([^\]]+)(?:\]\*\*|\])\((https?://[^\)]+)\)(?:\s*[-–—:]\s*(.+))?', raw_line)
            if m:
                label, url, rest = m.groups()
                desc = clean_desc(rest or f"Production-grade agent playbook for {label}")
                
                parts = label.split("/")
                owner = parts[0] if len(parts) > 1 else "community"
                slug_name = parts[1] if len(parts) > 1 else parts[0]
                slug = canonical_slug(slug_name.lower().replace(".", "-").replace("_", "-"))
                item_id = canonical_id("skill", owner, slug)

                if item_id in seen_ids:
                    continue
                seen_ids.add(item_id)


                items.append({
                    "id": item_id,
                    "name": slug_name.replace("-", " ").title(),
                    "slug": slug,
                    "kind": "skill",
                    "summary": desc,
                    "category": skill_category if skill_category != "General" else "Workflow & Playbooks",
                    "publisher": publisher_obj(owner, url, PROV_LIST),
                    "stars": None,
                    "version": None,
                    "skillSource": url,
                    "installability": DISCOVERY_ONLY,
                })
    row_counts.append((skills_url, skills_source, len(items) - rows_before))

    # --- Parse vendor plugin marketplaces (registered in internal/source/sources.go) ---
    # Each manifest is obtained through the snapshot layer (recorded bytes, see
    # obtain()); entries map to plugin rows (and, for anthropics/skills, one
    # skill row per bundled skill). Slugs are namespaced per source so they can
    # never collide with awesome-list rows.
    print("Parsing vendor plugin marketplaces...")
    MARKETPLACE_SOURCES = [
        {
            "key": "claude", "id_owner": "claude-official",
            "url": "https://raw.githubusercontent.com/anthropics/claude-plugins-official/main/.claude-plugin/marketplace.json",
            "source_id": "git:claude-plugins-official",
            "repo": "https://github.com/anthropics/claude-plugins-official",
            "publisher": "Anthropic", "family": "claude",
            "marketplace": "claude-plugins-official", "hosts": ["Claude Code"],
        },
        {
            "key": "knowledge", "id_owner": "knowledge-work",
            "url": "https://raw.githubusercontent.com/anthropics/knowledge-work-plugins/main/.claude-plugin/marketplace.json",
            "source_id": "git:knowledge-work-plugins",
            "repo": "https://github.com/anthropics/knowledge-work-plugins",
            "publisher": "Anthropic", "family": "claude",
            "marketplace": "knowledge-work-plugins", "hosts": ["Claude Code"],
        },
        {
            "key": "askills", "id_owner": "anthropic-skills",
            "url": "https://raw.githubusercontent.com/anthropics/skills/main/.claude-plugin/marketplace.json",
            "source_id": "git:anthropics-skills",
            "repo": "https://github.com/anthropics/skills",
            "publisher": "Anthropic", "family": "claude",
            "marketplace": "anthropic-agent-skills", "hosts": ["Claude Code"],
        },
        {
            "key": "codex", "id_owner": "openai-plugins",
            "url": "https://raw.githubusercontent.com/openai/plugins/main/.agents/plugins/marketplace.json",
            "source_id": "git:openai-plugins",
            "repo": "https://github.com/openai/plugins",
            "publisher": "OpenAI", "family": "codex",
            "marketplace": "", "hosts": ["Codex"],
        },
        {
            "key": "codex", "id_owner": "openai-plugins",
            "url": "https://raw.githubusercontent.com/openai/plugins/main/.agents/plugins/api_marketplace.json",
            "source_id": "git:openai-plugins",
            "repo": "https://github.com/openai/plugins",
            "publisher": "OpenAI", "family": "codex",
            "marketplace": "", "hosts": ["Codex"],
        },
        {
            "key": "cursor", "id_owner": "cursor-plugins",
            "url": "https://raw.githubusercontent.com/cursor/plugins/main/.cursor-plugin/marketplace.json",
            "source_id": "git:cursor-plugins",
            "repo": "https://github.com/cursor/plugins",
            "publisher": "Cursor", "family": "cursor",
            "marketplace": "", "hosts": ["Cursor"],
        },
        {
            "key": "xai", "id_owner": "xai-plugins",
            "url": "https://raw.githubusercontent.com/xai-org/plugin-marketplace/main/.grok-plugin/marketplace.json",
            "source_id": "git:xai-plugin-marketplace",
            "repo": "https://github.com/xai-org/plugin-marketplace",
            "publisher": "xAI", "family": "grok",
            "marketplace": "", "hosts": ["Grok Build"],
        },
    ]

    def manifest_author(a, fallback):
        if isinstance(a, dict):
            return (a.get("name") or fallback).strip() or fallback
        if isinstance(a, str) and a.strip():
            return a.strip()
        return fallback

    def manifest_source_url(s):
        if isinstance(s, dict):
            return s.get("url") or s.get("repo") or ""
        return ""

    def manifest_is_command(s):
        if not isinstance(s, dict):
            return False
        kind = str(s.get("source") or s.get("type") or "").lower()
        return kind == "command" or bool(s.get("command"))

    def base_slug(name):
        return re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")


    for src in MARKETPLACE_SOURCES:
        try:
            manifest = json.loads(
                obtain(src["url"], src["source_id"], 25, refresh, src["key"])
                .decode("utf-8", errors="ignore"))
        except snapshot_store.SnapshotIntegrityError:
            raise
        except Exception as e:
            print(f"  {src['key']}: fetch failed ({e}), skipped")
            snapshot_store.log_failure(snap_root, src["url"], "obtain", str(e))
            continue
        entries = manifest.get("plugins", []) if isinstance(manifest, dict) else []
        added = 0
        seen_slugs = set(i.get("slug") for i in items)
        for p in entries:
            if not isinstance(p, dict):
                continue
            name = (p.get("name") or "").strip()
            if not name or manifest_is_command(p.get("source")):
                continue
            display = (p.get("displayName") or p.get("interface", {}).get("displayName") if isinstance(p.get("interface"), dict) else p.get("displayName")) or name
            desc = (p.get("description") or "").strip()
            base = base_slug(name)
            if not base:
                continue
            slug = canonical_slug(f"{src['key']}-{base}")
            item_id = canonical_id("plugin", src["id_owner"], base)
            if item_id in seen_ids or slug in seen_slugs:
                continue
            seen_ids.add(item_id)
            seen_slugs.add(slug)

            if src["family"] == "codex":
                category = clean_category(p.get("category") or "developer tools")
                if not desc:
                    desc = f"Official Codex plugin ({category})."
                upstream = manifest_source_url(p.get("source")) or src["repo"]
                install_hint = ""
            elif src["family"] == "cursor":
                category = "Plugins & Toolkits"
                sub = p.get("source") if isinstance(p.get("source"), str) else ""
                upstream = f"{src['repo']}/tree/main/{sub}" if sub else src["repo"]
                install_hint = ""
            elif src["family"] == "grok":
                category = clean_category(p.get("category") or "developer tools")
                upstream = p.get("homepage") or manifest_source_url(p.get("source")) or src["repo"]
                install_hint = ""
            else:  # claude family
                raw_cat = p.get("category") or ""
                category = clean_category(raw_cat if isinstance(raw_cat, str) else "developer tools")
                upstream = manifest_source_url(p.get("source")) or p.get("homepage") or p.get("repository") or src["repo"]
                install_hint = f"/plugin install {name}@{src['marketplace']}" if src.get("marketplace") else ""

            # Manifest version only when the manifest publishes one.
            # An absent version is null, not a guess.
            manifest_version = p.get("version") if isinstance(p.get("version"), str) and p.get("version").strip() else None
            row = {
                "id": item_id,
                "name": display,
                "slug": slug,
                "kind": "plugin",
                "summary": desc or f"Plugin '{name}' from {src['publisher']}.",
                "category": category,
                "publisher": publisher_obj(src["publisher"], upstream, PROV_VENDOR),
                # The vendor manifests publish no star counts. null means
                # "not published"; 0 would read as a measured zero.
                "stars": None,
                # Only a version the vendor manifest itself declares.
                "version": p.get("version") if isinstance(p.get("version"), str) and p.get("version").strip() else None,
                "compatibleHosts": src["hosts"],
                "installability": METADATA_VERIFIED,
            }
            if install_hint:
                row["installHint"] = install_hint
            items.append(row)
            added += 1

            # anthropics/skills bundles expose individually installable skills.
            if src["key"] == "askills":
                for sp in p.get("skills", []) or []:
                    skill = base_slug(sp.split("/")[-1])
                    if not skill:
                        continue
                    skill_id = canonical_id("skill", "anthropics", skill)
                    skill_slug = f"askills-{skill}"
                    if skill_id in seen_ids or skill_slug in seen_slugs:
                        continue
                    seen_ids.add(skill_id)
                    seen_slugs.add(skill_slug)
                    items.append({
                        "id": skill_id,
                        "name": skill.replace("-", " ").title(),
                        "slug": f"askills-{skill}",
                        "kind": "skill",
                        "summary": f"Anthropic example skill '{skill}'.",
                        "category": "Agent Skills",
                        "publisher": publisher_obj(
                            "anthropics", f"{src['repo']}/tree/main/skills/{skill}", PROV_VENDOR),
                        "stars": None,
                        "version": None,
                        "installability": METADATA_VERIFIED,
                        "skillSource": f"{src['repo']}/tree/main/skills/{skill}",
                        "installHint": f"npx skills add {src['repo']} --skill {skill}",
                    })
                    added += 1
        print(f"  {src['key']}: +{added} rows")
        row_counts.append((src["url"], src["source_id"], added))

    # Finalize the snapshots this run ingested: status healthy + itemCount.
    # Only recordings that exist are updated -- a skipped source has no
    # snapshot and claims none (never fabricate an entry you did not fetch).
    for snap_url, snap_source_id, snap_rows in row_counts:
        updated, changed = snapshot_store.mark_ingested(snap_root, snap_url, snap_rows)
        if changed and updated is not None:
            print(f"  snapshot {updated['snapshot']['snapshotId']}: "
                  f"{snap_rows} rows ingested -> healthy")

    # --- No hand-curated "featured" plugins ---
    # An earlier revision injected three hand-written plugin rows here, each
    # carrying an invented star count (41,200 / 28,700 / 33,400) and one
    # attributing a 41,200-star plugin to this project's own repository, which
    # has none. Because the catalog was ranked by stars, those fabricated rows
    # occupied the top of the public index. Removed: the catalog lists only
    # capabilities that were actually ingested from a named upstream source.

    # Sort catalog deterministically.
    #
    # No popularity ranking: `stars` is null for every row (see the note at the
    # top of this file), so there is nothing to rank by. Ordering is
    # verified-publisher first, then kind, then name -- all measured facts, all
    # stable across runs and machines.
    kind_order = {"mcp": 0, "skill": 1, "plugin": 2}
    items.sort(key=lambda i: (
        0 if i.get("publisher", {}).get("verified", False) else 1,
        kind_order.get(i.get("kind", ""), 9),
        (i.get("name") or "").casefold(),
        i.get("id", ""),
    ))

    print(f"Total catalog capabilities indexed: {len(items)}")
    mcp_count = sum(1 for x in items if x["kind"] == "mcp")
    skill_count = sum(1 for x in items if x["kind"] == "skill")
    plugin_count = sum(1 for x in items if x["kind"] == "plugin")
    print(f"  MCP Servers: {mcp_count}")
    print(f"  Agent Skills: {skill_count}")
    print(f"  Plugins: {plugin_count}")

    # Write output to web/data/catalog.json
    out_dir = os.environ.get("CATALOG_OUT_DIR") or os.path.join(os.path.dirname(__file__), "..", "web", "data")
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, "catalog.json")
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(items, f, indent=2, ensure_ascii=False)
    
    file_size_mb = os.path.getsize(out_path) / (1024 * 1024)
    print(f"Written successfully to {out_path} ({file_size_mb:.2f} MB)")

    # Publish the release stats bundle.
    #
    # Ownership (single writer per key):
    #   - this script owns the DATASET STATS: itemCount/totalCapabilities,
    #     per-kind counts, hostCompatibility, datasetDigest. It merges them
    #     into web/data/release.json, which the site imports at BUILD time so
    #     the static html already shows real numbers instead of a "checking
    #     release manifest..." placeholder that only resolves once JS runs.
    #   - `litespm catalog build` (the Go builder) owns the RELEASE IDENTITY:
    #     releaseId, sequence, manifestDigest, createdAt. Those keys are
    #     preserved here and stamped by the builder, because the builder also
    #     owns the served pointer web/public/v1/current.json and the release
    #     tree its digest names. Two writers of one key is how digests drift.
    #
    # itemCount and totalCapabilities previously disagreed (5185 vs 5814) because
    # only one of them was maintained. They are now the same measurement.
    import hashlib
    import datetime

    catalog_path = out_path
    with open(catalog_path, "rb") as f:
        digest = hashlib.sha256(f.read()).hexdigest()

    # SOURCE_DATE_EPOCH keeps CI builds reproducible when set; otherwise the
    # stats are stamped with build time, the honest default.
    epoch = os.environ.get("SOURCE_DATE_EPOCH")
    created = (
        datetime.datetime.fromtimestamp(int(epoch), datetime.timezone.utc)
        if epoch
        else datetime.datetime.now(datetime.timezone.utc)
    )

    bundled_path = os.path.join(out_dir, "release.json")
    stats = {}
    seed_path = bundled_path if os.path.exists(bundled_path) else os.path.join(os.path.dirname(__file__), "..", "web", "data", "release.json")
    if os.path.exists(seed_path):
        with open(seed_path, "r", encoding="utf-8") as f:
            stats = json.load(f)

    stats.update({
        "itemCount": len(items),
        "totalCapabilities": len(items),
        "mcpServersCount": mcp_count,
        "agentSkillsCount": skill_count,
        "pluginsCount": plugin_count,
        "datasetDigest": f"sha256:{digest}",
        # Host compatibility is a property of the KIND, not of each row: an MCP
        # server is installable into every bridge adapter, a skill only into hosts
        # with a documented skills directory. Storing the 50-name list on each of
        # 4,079 rows cost 4.8 MB and said nothing the kind did not already say.
        "hostCompatibility": {
            "mcp": "all-bridge-adapters",
            "skill": "all-skill-targets",
            "plugin": "publisher-declared",
        },
        "statsUpdatedAt": created.strftime("%Y-%m-%dT%H:%M:%SZ"),
    })

    with open(bundled_path, "w", encoding="utf-8") as f:
        json.dump(stats, f, indent=2)
        f.write("\n")

    print(f"Published dataset stats: {len(items)} capabilities, sha256:{digest[:12]}")

if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(
        description="Build web/data/catalog.json from upstream awesome-lists and vendor marketplace manifests. "
        "Heuristic rows are discovery_only with no fabricated version/command/runtime; "
        "only vendor-manifest rows carry metadata_verified.",
    )
    parser.add_argument(
        "--output",
        default=None,
        help="Override the catalog.json output path (default: web/data/catalog.json next to this script).",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Syntax/provenance check only: verify no fabricated commands or hardcoded versions remain, without network fetches.",
    )
    parser.add_argument(
        "--refresh",
        action="store_true",
        help="Re-fetch upstream with conditional requests (If-None-Match) and replace a recorded "
             "snapshot only when the server reports a change. Default: replay recorded snapshots, "
             "fetching only what is missing (a re-run needs no network and is byte-reproducible).",
    )
    args = parser.parse_args()
    if args.check:
        import pathlib

        full = pathlib.Path(__file__).read_text(encoding="utf-8")
        # Only scan the builder body: everything before the CLI block.
        # (The check itself names the forbidden patterns, so scanning the
        # whole file would always self-match.)
        src = full.split('if __name__ == "__main__":', 1)[0]
        problems = []
        # R5: the hint variable must stay defined (it was once referenced but
        # never assigned, which aborted the run with a NameError), and it must
        # never be written into the `transport` field — it is a list author's
        # emoji claim, not proven metadata.
        import re as _re
        if _re.search(r'^\s*upstream_transport_hint\s*$', src, _re.M) and \
           not _re.search(r'^\s*upstream_transport_hint\s*=', src, _re.M):
            problems.append("upstream hint referenced without assignment (R5)")
        if _re.search(r'["\']transport["\']\s*:\s*upstream_transport_hint', src):
            problems.append("upstream hint emitted as transport (R5)")
        # T1: no fabricated launch-line assignments in the builder.
        for pat in ['f"{repo}-mcp"', 'f"@modelcontextprotocol/server-', 'cmd = "uvx"', 'cmd = "cargo"', 'cmd = "npx"', "args = [repo]"]:
            if pat in src:
                problems.append(f"fabricated command pattern still present: {pat}")
        # T1: no row may be assigned a literal hardcoded version. (A comment
        # describing the old behaviour is allowed; an assignment is not.)
        if _re.search(r'["\']version["\']\s*:\s*["\']1\.0\.0["\']', src) or \
           _re.search(r'version\s*=\s*["\']1\.0\.0["\']', src):
            problems.append('hardcoded version "1.0.0" still assigned (T1)')
        # T2: no hardcoded verified allowlists in the builder.
        if "in [" in src and "modelcontextprotocol\", \"anthropic\"" in src:
            problems.append("hardcoded verified allowlist still present (T2)")
        if problems:
            print("CHECK FAILED:")
            for p in problems:
                print(f"  - {p}")
            sys.exit(1)
        print("CHECK OK: no fabricated commands, no hardcoded versions, no undefined hint.")
    else:
        if args.output:
            # Honour an explicit output override: the builder writes
            # catalog.json (and the dataset stats) into CATALOG_OUT_DIR, so
            # point that directory at the requested path. A path that names a
            # directory (or lacks a .json suffix) is treated as the directory;
            # a catalog.json path contributes its parent directory.
            out_arg = os.path.abspath(args.output)
            if out_arg.endswith(".json") and not os.path.isdir(out_arg):
                out_arg = os.path.dirname(out_arg)
            os.environ["CATALOG_OUT_DIR"] = out_arg
        build_full_catalog(refresh=args.refresh)
