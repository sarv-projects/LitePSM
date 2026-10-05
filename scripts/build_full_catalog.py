import urllib.request
import re
import json
import os
import sys

sys.stdout.reconfigure(encoding='utf-8')

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

def build_full_catalog():
    print("Ingesting real registries...")
    
    # 1. Fetch punkpeye/awesome-mcp-servers
    print("1/3 Fetching punkpeye/awesome-mcp-servers...")
    req1 = urllib.request.Request(
        "https://raw.githubusercontent.com/punkpeye/awesome-mcp-servers/main/README.md",
        headers={"User-Agent": "Mozilla/5.0"}
    )
    with urllib.request.urlopen(req1, timeout=25) as resp:
        mcp_md = resp.read().decode("utf-8", errors="ignore")
    
    # 2. Fetch VoltAgent/awesome-agent-skills
    print("2/3 Fetching VoltAgent/awesome-agent-skills...")
    req2 = urllib.request.Request(
        "https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md",
        headers={"User-Agent": "Mozilla/5.0"}
    )
    with urllib.request.urlopen(req2, timeout=25) as resp:
        skills_md = resp.read().decode("utf-8", errors="ignore")
        
    # 3. Fetch modelcontextprotocol/servers
    print("3/3 Fetching modelcontextprotocol/servers...")
    try:
        req3 = urllib.request.Request(
            "https://raw.githubusercontent.com/modelcontextprotocol/servers/main/README.md",
            headers={"User-Agent": "Mozilla/5.0"}
        )
        with urllib.request.urlopen(req3, timeout=15) as resp:
            official_mcp_md = resp.read().decode("utf-8", errors="ignore")
    except Exception:
        official_mcp_md = ""

    items = []
    seen_ids = set()

    # --- Parse Official MCP Servers ---
    print("Parsing official MCP servers...")
    official_cat = "Official Core"
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
                        "publisher": {
                            "name": "modelcontextprotocol",
                            "verified": True,
                            "url": "https://github.com/modelcontextprotocol/servers"
                        },
                        "transport": "stdio",
                        "runtime": "typescript",
                        "stars": None,
                        "version": "1.0.0",
                        "command": "npx",
                        "args": ["-y", f"@modelcontextprotocol/server-{slug}"]
                    })

    # --- Parse punkpeye/awesome-mcp-servers ---
    print(f"Parsing punkpeye servers...")
    current_category = "Developer Tools"
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

                transport = "stdio"
                if "☁️" in rest and "🏠" not in rest:
                    transport = "sse"

                desc = clean_desc(rest)
                if not desc:
                    desc = f"Model Context Protocol server for {label}."

                verified = owner.lower() in [
                    "modelcontextprotocol", "anthropic", "github", "cloudflare", "google",
                    "microsoft", "aws", "docker", "sentry", "supabase", "neon", "redis",
                    "mongodb", "elastic", "brave", "slackapi", "qdrant", "weaviate"
                ]

                # Command formulation
                if runtime == "python":
                    cmd = "uvx"
                    args = [repo]
                elif runtime == "go":
                    cmd = "go"
                    args = ["run", f"github.com/{owner}/{repo}"]
                elif runtime == "rust":
                    cmd = "cargo"
                    args = ["run", "--release"]
                else:
                    cmd = "npx"
                    args = ["-y", f"{repo}-mcp" if not repo.endswith("-mcp") else repo]

                items.append({
                    "id": item_id,
                    "name": label if "/" not in label else repo,
                    "slug": slug,
                    "kind": "mcp",
                    "summary": desc,
                    "category": current_category,
                    "publisher": {
                        "name": owner,
                        "verified": verified,
                        "url": f"https://github.com/{owner}"
                    },
                    "transport": transport,
                    "runtime": runtime,
                    "stars": None,
                    "version": "1.0.0",
                    "command": cmd,
                    "args": args
                })

    # --- Parse VoltAgent/awesome-agent-skills ---
    print(f"Parsing VoltAgent skills...")
    skill_category = "Agent Skills"
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


                verified = owner.lower() in ["anthropics", "openai", "google", "voltagent", "vercel", "cursor", "microsoft"]

                items.append({
                    "id": item_id,
                    "name": slug_name.replace("-", " ").title(),
                    "slug": slug,
                    "kind": "skill",
                    "summary": desc,
                    "category": skill_category if skill_category != "General" else "Workflow & Playbooks",
                    "publisher": {
                        "name": owner,
                        "verified": verified,
                        "url": url
                    },
                    "stars": None,
                    "version": "1.0.0",
                    "skillSource": url
                })

    # --- Parse vendor plugin marketplaces (registered in internal/source/sources.go) ---
    # Each manifest is fetched live; entries map to plugin rows (and, for
    # anthropics/skills, one skill row per bundled skill). Slugs are namespaced
    # per source so they can never collide with awesome-list rows.
    print("Parsing vendor plugin marketplaces...")
    MARKETPLACE_SOURCES = [
        {
            "key": "claude", "id_owner": "claude-official",
            "url": "https://raw.githubusercontent.com/anthropics/claude-plugins-official/main/.claude-plugin/marketplace.json",
            "repo": "https://github.com/anthropics/claude-plugins-official",
            "publisher": "Anthropic", "family": "claude",
            "marketplace": "claude-plugins-official", "hosts": ["Claude Code"],
        },
        {
            "key": "knowledge", "id_owner": "knowledge-work",
            "url": "https://raw.githubusercontent.com/anthropics/knowledge-work-plugins/main/.claude-plugin/marketplace.json",
            "repo": "https://github.com/anthropics/knowledge-work-plugins",
            "publisher": "Anthropic", "family": "claude",
            "marketplace": "knowledge-work-plugins", "hosts": ["Claude Code"],
        },
        {
            "key": "askills", "id_owner": "anthropic-skills",
            "url": "https://raw.githubusercontent.com/anthropics/skills/main/.claude-plugin/marketplace.json",
            "repo": "https://github.com/anthropics/skills",
            "publisher": "Anthropic", "family": "claude",
            "marketplace": "anthropic-agent-skills", "hosts": ["Claude Code"],
        },
        {
            "key": "codex", "id_owner": "openai-plugins",
            "url": "https://raw.githubusercontent.com/openai/plugins/main/.agents/plugins/marketplace.json",
            "repo": "https://github.com/openai/plugins",
            "publisher": "OpenAI", "family": "codex",
            "marketplace": "", "hosts": ["Codex"],
        },
        {
            "key": "codex", "id_owner": "openai-plugins",
            "url": "https://raw.githubusercontent.com/openai/plugins/main/.agents/plugins/api_marketplace.json",
            "repo": "https://github.com/openai/plugins",
            "publisher": "OpenAI", "family": "codex",
            "marketplace": "", "hosts": ["Codex"],
        },
        {
            "key": "cursor", "id_owner": "cursor-plugins",
            "url": "https://raw.githubusercontent.com/cursor/plugins/main/.cursor-plugin/marketplace.json",
            "repo": "https://github.com/cursor/plugins",
            "publisher": "Cursor", "family": "cursor",
            "marketplace": "", "hosts": ["Cursor"],
        },
        {
            "key": "xai", "id_owner": "xai-plugins",
            "url": "https://raw.githubusercontent.com/xai-org/plugin-marketplace/main/.grok-plugin/marketplace.json",
            "repo": "https://github.com/xai-org/plugin-marketplace",
            "publisher": "xAI", "family": "grok",
            "marketplace": "", "hosts": ["Grok Build"],
        },
    ]

    def fetch_json(url):
        req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=25) as resp:
            return json.loads(resp.read().decode("utf-8", errors="ignore"))

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
            manifest = fetch_json(src["url"])
        except Exception as e:
            print(f"  {src['key']}: fetch failed ({e}), skipped")
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

            row = {
                "id": item_id,
                "name": display,
                "slug": slug,
                "kind": "plugin",
                "summary": desc or f"Plugin '{name}' from {src['publisher']}.",
                "category": category,
                "publisher": {"name": src["publisher"], "verified": True, "url": upstream},
                # The vendor manifests publish no star counts. null means
                # "not published"; 0 would read as a measured zero.
                "stars": None,
                "version": "1.0.0",
                "compatibleHosts": src["hosts"],
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
                        "publisher": {
                            "name": "anthropics",
                            "verified": True,
                            "url": f"{src['repo']}/tree/main/skills/{skill}",
                        },
                        "stars": None,
                        "version": "1.0.0",
                        "skillSource": f"{src['repo']}/tree/main/skills/{skill}",
                        "installHint": f"npx skills add {src['repo']} --skill {skill}",
                    })
                    added += 1
        print(f"  {src['key']}: +{added} rows")

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
    out_path = os.path.join(os.path.dirname(__file__), "..", "web", "data", "catalog.json")
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

    catalog_path = os.path.join(os.path.dirname(__file__), "..", "web", "data", "catalog.json")
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

    bundled_path = os.path.join(os.path.dirname(__file__), "..", "web", "data", "release.json")
    stats = {}
    if os.path.exists(bundled_path):
        with open(bundled_path, "r", encoding="utf-8") as f:
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
    build_full_catalog()
