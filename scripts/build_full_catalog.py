import urllib.error
import urllib.parse
import urllib.request
import contextlib
import http.client
import html
import ipaddress
import io
import re
import json
import os
import socket
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
# skills.sh and mcpservers.org are directories, not git manifests: the
# producer walks each site's sitemap and reads one record per capability
# (skills.sh: SKILL.md frontmatter through its download API; mcpservers.org:
# public Wayback Machine replays of the directory pages -- the site's own
# API paths are robots-disallowed, so pages are never fetched from
# mcpservers.org itself). Both sourceIds follow the domain.SourceID grammar.
FEED_SKILLSH = (
    "https://skills.sh/sitemap.xml",
    "feed:skills-sh",
)
FEED_MCPSERVERS = (
    "https://mcpservers.org/sitemap.xml",
    "feed:mcpservers-org",
)
# mcpmarket.com is a directory whose robots.txt PERMITS crawling (probed
# 2026-10-08, quoted verbatim):
#     User-Agent: *
#     Allow: /
#     Disallow: /api/
#     Crawl-delay: 1
#     Sitemap: https://mcpmarket.com/sitemap.xml
# so -- unlike mcpservers.org -- its listing pages are fetched straight from
# the origin, at the declared crawl delay, and NEVER from /api/ (the one
# robots-disallowed path). The sitemap index points at 12 tools and 75 skills
# sub-sitemaps whose <loc> entries are /server/<slug> and
# /tools/skills/<slug> listing pages (observed 2026-10-08: ~48.7k + ~368k,
# every loc on the apex host, no query strings). Row content comes from each
# page's own JSON-LD (name / description / author) with <title> + meta
# description as the fallback; the crawl is budgeted (see
# MCPMARKET_DEFAULT_MAX_PAGE_FETCHES) because Crawl-delay: 1 over ~417k pages
# would otherwise occupy a build run for days.
FEED_MCPMARKET = (
    "https://mcpmarket.com/sitemap.xml",
    "feed:mcpmarket-com",
)

# OFFICIAL MCP REGISTRY API (production ingest)
# Paginated GET of /v0/servers. The source id follows the domain.SourceID
# grammar (`^[a-z0-9_-]{3,32}:[a-z0-9_-]{3,64}$`) and is the id registered
# for this upstream in internal/source/sources.go (KnownSources), so dataset
# rows land in the namespace the Go consumers already expect:
# `mcp:builtin:mcp-registry:<upstream-name>`.
REGISTRY_API = "https://registry.modelcontextprotocol.io/v0/servers"
REGISTRY_SOURCE = "builtin:mcp-registry"
# The upstream answers 422 for limit > 100 (confirmed against the live API).
REGISTRY_PAGE_LIMIT = 100
REGISTRY_META_KEY = "io.modelcontextprotocol.registry/official"
REGISTRY_MAX_PAGES = 10000

# Source IDs map to the exact upstream hosts the producer is expected to use.
# Sitemap contents are untrusted input: a URL embedded in a feed must not turn
# the build into a fetch proxy for arbitrary hosts or private services.
SOURCE_ALLOWED_HOSTS = {
    "builtin:mcp-registry": frozenset({"registry.modelcontextprotocol.io"}),
    "feed:punkpeye-awesome-mcp-servers": frozenset({"raw.githubusercontent.com"}),
    # This feed's rows are verified at build time (see resolve_skill_row): the
    # raw host serves the SKILL.md probes, and officialskills.sh serves the
    # pages that publish the GitHub tree/repo link for this feed's rows. Both
    # are exact hostnames for this source id only -- a row can never steer a
    # fetch to another host.
    "feed:voltagent-awesome-agent-skills": frozenset({
        "raw.githubusercontent.com",
        "officialskills.sh",
        "www.officialskills.sh",
    }),
    "feed:modelcontextprotocol-servers": frozenset({"raw.githubusercontent.com"}),
    "feed:skills-sh": frozenset({"skills.sh", "www.skills.sh"}),
    "feed:mcpservers-org": frozenset({"mcpservers.org", "web.archive.org"}),
    # mcpmarket.com serves on the apex and on www (www answers 301 ->
    # https://mcpmarket.com/). Exact hostnames only, never substring matches.
    "feed:mcpmarket-com": frozenset({"mcpmarket.com", "www.mcpmarket.com"}),
    "git:claude-plugins-official": frozenset({"raw.githubusercontent.com"}),
    "git:knowledge-work-plugins": frozenset({"raw.githubusercontent.com"}),
    "git:anthropics-skills": frozenset({"raw.githubusercontent.com"}),
    "git:openai-plugins": frozenset({"raw.githubusercontent.com"}),
    "git:cursor-plugins": frozenset({"raw.githubusercontent.com"}),
    "git:xai-plugin-marketplace": frozenset({"raw.githubusercontent.com"}),
}
MAX_RESPONSE_BYTES = 16 * 1024 * 1024

# MCPMARKET.COM CRAWL BOUNDS
# robots.txt declares `Crawl-delay: 1` for every user agent and the sitemap
# index currently enumerates ~417k listing pages, so an uncapped first crawl
# would occupy a build run for days. Each run fetches at most
# MCPMARKET_DEFAULT_MAX_PAGE_FETCHES uncached pages; the limit is overridable
# with the MCPMARKET_MAX_PAGE_FETCHES environment variable (0 = uncapped, for
# a deliberate fully scoped run; a non-integer or negative value fails closed).
# Recorded pages replay offline and never consume the budget, so repeated runs
# grow coverage from the snapshot store; pages a run did not reach are counted
# and the source is marked incomplete -- an uncrawled page is never presented
# as a page that published nothing.
MCPMARKET_DEFAULT_MAX_PAGE_FETCHES = 2000
MCPMARKET_CRAWL_DELAY = 1.0
# Strict path shapes for this source: _is_source_sitemap_url fullmatches them
# against the parsed path (no query, exact allowlisted host), so a sitemap
# entry can never steer the build to another host or an unintended path.
MCPMARKET_SUBMAP_PATH = r"/sitemap/(?:tools|skills)-[0-9]+\.xml"
# The index also lists /sitemap/static.xml (site-wide pages, not capability
# listings): it is validated as a known sub-sitemap and deliberately not
# walked. It must not be counted as a rejected URL.
MCPMARKET_STATIC_PATH = r"/sitemap/static\.xml"
MCPMARKET_SERVER_PATH = r"/server/[A-Za-z0-9._~%+-]+"
MCPMARKET_SKILL_PATH = r"/tools/skills/[A-Za-z0-9._~%+-]+"

_INCOMPLETE_SOURCES = set()
_STALE_SOURCES = set()


def _mark_incomplete(source_id):
    _INCOMPLETE_SOURCES.add(source_id)


def _is_public_ip(value):
    try:
        addr = ipaddress.ip_address(value.split("%", 1)[0])
    except ValueError:
        return False
    if isinstance(addr, ipaddress.IPv6Address) and addr.ipv4_mapped:
        addr = addr.ipv4_mapped
    return addr.is_global


def _validate_request_url(url, source_id, check_dns=True):
    """Validate a source URL before snapshot lookup or network access.

    Hostnames are exact per-source allowlist entries; dynamic sitemap URLs
    cannot broaden the set of network destinations. When a request is made,
    all resolved addresses must also be globally routable. Redirects pass
    through the same check.
    """
    try:
        parsed = urllib.parse.urlsplit(url)
        host = parsed.hostname
        port = parsed.port
    except (TypeError, ValueError) as exc:
        raise ValueError(f"invalid source URL {url!r}: {exc}") from exc
    if parsed.scheme != "https" or not host or parsed.username is not None or parsed.password is not None:
        raise ValueError(f"refusing unsafe source URL {url!r}")
    if parsed.fragment or (port is not None and port != 443):
        raise ValueError(f"refusing unsafe source URL {url!r}")
    host = host.lower().rstrip(".")
    if host not in SOURCE_ALLOWED_HOSTS.get(source_id, frozenset()):
        raise ValueError(f"host {host!r} is not allowed for source {source_id!r}")
    if not check_dns:
        return
    try:
        addresses = socket.getaddrinfo(host, port or 443, type=socket.SOCK_STREAM)
    except OSError as exc:
        raise ValueError(f"DNS resolution failed for {host!r}: {exc}") from exc
    if not addresses:
        raise ValueError(f"DNS returned no addresses for {host!r}")
    for address in addresses:
        if not _is_public_ip(address[4][0]):
            raise ValueError(f"host {host!r} resolved to a non-public address")


def _is_source_sitemap_url(url, source_id, path_pattern):
    """Match a source sitemap URL by parsed origin and complete path."""
    try:
        _validate_request_url(url, source_id, check_dns=False)
        parsed = urllib.parse.urlsplit(url)
    except (TypeError, ValueError):
        return False
    return not parsed.query and re.fullmatch(path_pattern, parsed.path) is not None


def _resolve_public_addresses(host, port):
    """Resolve once, reject mixed/private answers, and return pinned sockets."""
    try:
        addresses = socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
    except OSError as exc:
        raise ValueError(f"DNS resolution failed for {host!r}: {exc}") from exc
    if not addresses:
        raise ValueError(f"DNS returned no addresses for {host!r}")
    if any(not _is_public_ip(address[4][0]) for address in addresses):
        raise ValueError(f"host {host!r} resolved to a non-public address")
    return addresses


def _source_https_connection(source_id):
    """Build a one-request HTTPS connection that dials validated DNS results.

    urllib's default HTTPSConnection resolves the hostname again after a
    separate policy lookup. This subclass connects to the exact validated
    sockaddr while retaining the original hostname for TLS SNI/certificate
    verification. Proxies are disabled by the opener because they would
    perform the destination lookup outside this boundary.
    """
    class SourceHTTPSConnection(http.client.HTTPSConnection):
        def connect(self):
            if self._tunnel_host:
                raise OSError("catalog source fetches do not permit proxy tunnels")
            # Source URL host allowlisting is checked before this connection;
            # resolve again here and pin precisely this validated answer set.
            if self.host.lower().rstrip(".") not in SOURCE_ALLOWED_HOSTS.get(source_id, frozenset()):
                raise ValueError(f"host {self.host!r} is not allowed for source {source_id!r}")
            addresses = _resolve_public_addresses(self.host, self.port)
            last_error = None
            for family, socktype, proto, _canonname, sockaddr in addresses:
                sock = socket.socket(family, socktype, proto)
                try:
                    if self.timeout is not socket._GLOBAL_DEFAULT_TIMEOUT:
                        sock.settimeout(self.timeout)
                    if self.source_address:
                        sock.bind(self.source_address)
                    sock.connect(sockaddr)
                    self.sock = self._context.wrap_socket(sock, server_hostname=self.host)
                    return
                except OSError as exc:
                    last_error = exc
                    sock.close()
            if last_error is not None:
                raise last_error
            raise OSError(f"DNS returned no usable addresses for {self.host!r}")

    return SourceHTTPSConnection


class _SourceHTTPSHandler(urllib.request.HTTPSHandler):
    def __init__(self, source_id):
        super().__init__()
        self.connection_class = _source_https_connection(source_id)

    def https_open(self, req):
        return self.do_open(self.connection_class, req, context=self._context)


class _SourceRedirectHandler(urllib.request.HTTPRedirectHandler):
    def __init__(self, source_id):
        super().__init__()
        self.source_id = source_id

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        _validate_request_url(newurl, self.source_id)
        return super().redirect_request(req, fp, code, msg, headers, newurl)

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
#                     servers README) OR an upstream that published metadata
#                     but no launch line (the Official MCP Registry publishes
#                     package coordinates and versions, never the command that
#                     starts them; a registry row whose only endpoint claim
#                     fails the publish policy stays here). The row is
#                     searchable metadata: `command`, `args`, `url` and
#                     `transport` are null and the installer refuses it
#                     (LPSM-NOT-INSTALLABLE). A `version` may still be present
#                     -- it appears only when the upstream itself published
#                     one, and a version without a proven launch line does not
#                     make a row installable.
#   metadata_verified a vendor's own marketplace manifest supplied the row
#                     AND, for MCP rows, the version AND (command OR url),
#                     with transport -- the command+args a future install
#                     would launch (stdio) or the endpoint it would connect
#                     to (remote; `url` carries the selected `remotes[]`
#                     endpoint after publishable_endpoint() sanitized it) --
#                     enforced fail-closed by catalogbuild.ParseDataset
#                     -- or, for a skill row, the
#                     build itself opened the SKILL.md at the path the row
#                     advertises and the frontmatter passed the installer's
#                     own gates (resolve_skill_row below). A promoted skill
#                     row still carries no version and no launch line.
# An earlier revision stamped version "1.0.0" on every row and guessed a launch
# line from an emoji ("npx -y {repo}-mcp", "uvx {repo}", "cargo run"). Those
# commands named packages that mostly do not exist; installing one would have
# executed an attacker-registerable npm/PyPI name. Nothing here guesses one now.
#
# `publisher.verified` is likewise no longer a hand-typed allowlist. It is true
# only when the row was read from a publisher-authored manifest: a vendor's own
# repository manifest (`publisher.provenance == "vendor-manifest"`) or the
# publisher's own server.json served by the Official MCP Registry API (the same
# self-declared class -- the publisher wrote and published that manifest;
# a registry entry is not a third party claiming on their behalf, and neither
# is a security audit). Awesome-list rows are `provenance == "awesome-list-claim"`
# and never verified. Neither class is a LiteSPM security audit.
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


# OFFICIAL MCP REGISTRY -- element helpers
#
# /v0/servers wraps each server.json as {"server": <server.json>,
# "_meta": {"io.modelcontextprotocol.registry/official": {...}}}; the
# official _meta carries status / isLatest for that (name, version) record.

def registry_server_of(element):
    """The server.json of one /v0/servers element, or None if undocumented."""
    if not isinstance(element, dict):
        return None
    srv = element.get("server")
    return srv if isinstance(srv, dict) else None


def registry_official_meta(element, srv):
    """The official _meta block of one record (element-level, else server-level)."""
    for meta in (element.get("_meta"), srv.get("_meta")):
        if isinstance(meta, dict) and isinstance(meta.get(REGISTRY_META_KEY), dict):
            return meta[REGISTRY_META_KEY]
    return {}


def registry_page_parts(payload):
    """Decode one documented registry envelope and its optional next cursor."""
    envelope = json.loads(payload)
    if not isinstance(envelope, dict):
        raise TypeError("registry page is not an object")
    elements = envelope.get("servers")
    if not isinstance(elements, list):
        raise TypeError("'servers' is not a list")
    metadata = envelope.get("metadata")
    if not isinstance(metadata, dict):
        raise TypeError("'metadata' is not an object")
    cursor = metadata.get("nextCursor")
    if cursor is not None and (not isinstance(cursor, str) or not cursor):
        raise TypeError("'metadata.nextCursor' must be a non-empty string when present")
    if cursor is not None and len(cursor) > 4096:
        raise TypeError("'metadata.nextCursor' exceeds the 4096 character limit")
    return elements, cursor


def registry_status_is_active(status):
    """Only the registry's explicit active state is eligible for publication."""
    return status == "active"


def registry_latest_flag(value):
    """Decode the optional official isLatest flag without truthy coercion."""
    if value is None:
        return False
    if not isinstance(value, bool):
        raise TypeError("official metadata 'isLatest' must be a boolean when present")
    return value


def registry_version_key(version):
    """Numeric tuple for version comparison; unparsable versions sort lowest."""
    try:
        return tuple(int(x) for x in str(version).split("."))
    except (ValueError, TypeError):
        return (0,)


def registry_publisher(srv_name, srv):
    """(name, url) for the publisher a registry name is namespaced under.

    The registry namespaces every server under a reverse-DNS root the
    publisher proved control of (`io.github.<owner>/<repo>`, `com.acme/x`).
    For the dominant `io.github.` namespace that root IS the GitHub owner;
    any other root is published verbatim (no profile URL is invented for it).
    The URL is the repository the server.json itself declares, else its
    website, else the GitHub profile the io.github namespace proves -- and
    nothing else: a missing URL stays empty rather than being guessed.
    """
    ns = srv_name.split("/", 1)[0] if "/" in srv_name else srv_name
    owner = ns
    gh = ""
    if ns.startswith("io.github."):
        owner = ns[len("io.github."):] or ns
        gh = f"https://github.com/{owner}"
    for candidate in ("repository", "websiteUrl"):
        value = srv.get(candidate)
        if isinstance(value, str) and value.startswith("https://"):
            return owner, value
    return owner, gh


def _is_loopback_host(host):
    """localhost / 127.0.0.0/8 / ::1 -- the host class internal/egress lets
    plain http through (Policy.AllowLoopbackHTTP), never a production origin."""
    h = str(host or "").strip().lower().rstrip(".")
    if h == "localhost":
        return True
    try:
        return ipaddress.ip_address(h).is_loopback
    except ValueError:
        return False


def select_remote_endpoint(remotes):
    """Select the one endpoint published from a server.json `remotes[]` list.

    Selection rule: the first entry whose type is ``streamable-http``, else
    the first entry. The registry vocabulary is ``streamable-http`` and
    ``sse``, and multiple endpoints per server are the norm upstream (a live
    sample carried 41 entries across 38 servers), while a dataset row carries
    exactly one -- so the unchosen entries are counted, never silently lost.

    Returns ``(entry, extras)``: the chosen mapping (None when the document
    declares no usable endpoints) and how many declared entries were not
    chosen. The caller logs the number of rows where ``extras`` is > 0.
    """
    if not isinstance(remotes, list):
        return None, 0
    entries = [e for e in remotes if isinstance(e, dict)]
    if not entries:
        return None, 0
    chosen = None
    for entry in entries:
        if str(entry.get("type") or "").strip() == "streamable-http":
            chosen = entry
            break
    if chosen is None:
        chosen = entries[0]
    return chosen, len(entries) - 1


def publishable_endpoint(url):
    """Whether a remote endpoint URL may be published in a dataset row.

    Static rules only (no DNS, no fetch: an endpoint is a connection target
    the guard checks at connect time, not a source this build reads). The
    policy mirrors _validate_request_url minus the per-source host
    allowlist -- https only, a required host, no embedded credentials, no
    fragment, default port -- plus the one carve-out internal/egress makes:
    plain http to a loopback host (Policy.AllowLoopbackHTTP, a local dev
    endpoint). A non-public IP literal is refused outright (link-local /
    metadata, RFC1918, CGNAT: the ranges the guard refuses with no policy
    knob). An endpoint this function refuses would be refused by the guard
    later, so it is dropped here instead of being published as something an
    install would then fail on; the caller logs that as a refusal.
    """
    if not isinstance(url, str):
        return False
    try:
        parsed = urllib.parse.urlsplit(url)
        host = parsed.hostname
        port = parsed.port
    except (TypeError, ValueError):
        return False
    if not host or parsed.username is not None or parsed.password is not None:
        return False
    if parsed.fragment:
        return False
    if _is_loopback_host(host):
        return True
    if port is not None and port != 443:
        return False
    if parsed.scheme != "https":
        return False
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return True  # hostname: resolved and classified by the guard at dial time
    return _is_public_ip(host)


def registry_row(item_id, display, slug, category, summary, pub_name, pub_url, srv):
    """Build the dataset row for one active, described registry server.

    Returns ``(row, extras, refused)``:
      row     -- the payload appended to ``items``;
      extras  -- declared ``remotes[]`` endpoints beyond the published one
                 (0 for a single-endpoint document); the caller counts the
                 rows where this is > 0 into the registry summary line;
      refused -- True when the document declared at least one endpoint but
                 none was publishable (sanitization or a missing transport
                 type); also counted into the summary, never silent.

    Launch line: ``command``/``args`` stay null because the registry
    publishes package coordinates and never the command that starts them,
    and this build never derives one (T1). What a remote server.json
    publishes is the endpoint itself -- publisher-declared launch data -- so
    a selected endpoint that passes publishable_endpoint() is emitted as
    ``url`` + ``transport`` and the row classifies metadata_verified on the
    strength of the publisher's own manifest (B1 Decision 7). With no
    publishable endpoint both stay null and the row remains discovery_only.
    ``version`` is the document's top-level server.json version: kept only
    when upstream itself published one.
    """
    version = srv.get("version")
    if not isinstance(version, str) or not version.strip():
        version = None
    else:
        version = version.strip()

    chosen, extras = select_remote_endpoint(srv.get("remotes"))
    url = transport = None
    refused = False
    if chosen is not None:
        endpoint = chosen.get("url")
        endpoint = endpoint.strip() if isinstance(endpoint, str) else ""
        declared = chosen.get("type")
        declared = declared.strip() if isinstance(declared, str) else ""
        if declared and endpoint and publishable_endpoint(endpoint):
            url, transport = endpoint, declared
        else:
            refused = True

    row = {
        "id": item_id,
        "name": display,
        "slug": slug,
        "kind": "mcp",
        "summary": summary,
        "category": category,
        # server.json is the publisher's own declaration: the same
        # vendor-manifest class as a marketplace manifest (see the
        # provenance note at the top of this file).
        "publisher": publisher_obj(pub_name, pub_url, PROV_VENDOR),
        # Launch line is never derived (T1): command/args/runtime stay
        # null. The only launch data the registry can publish for a remote
        # server is its endpoint, and only after publishable_endpoint()
        # accepted it -- otherwise url/transport are null too and the row
        # stays discovery_only rather than advertising an endpoint the
        # egress guard would refuse at connect time.
        "url": url,
        "transport": transport,
        "runtime": None,
        "stars": None,
        "version": version,
        "command": None,
        "args": None,
        "installability": METADATA_VERIFIED if url else DISCOVERY_ONLY,
        "source": REGISTRY_SOURCE,
    }
    return row, extras, refused


def registry_summary(added, servers, pages, inactive, nodesc, status_missing,
                     invalid, dup, extra_endpoint_rows, refused_endpoint_rows):
    """The registry block's one progress line.

    The two endpoint counts keep endpoint handling visible: multi-endpoint
    documents (extra_endpoint_rows) had all but the selected entry dropped,
    and refused_endpoint_rows declared an endpoint that was not publishable
    (non-https/non-loopback, credentials, fragment, non-default port, or a
    missing transport type) and therefore published none.
    """
    return (f"  registry: +{added} rows from {servers} servers "
            f"over {pages} page(s) ({inactive} not active, "
            f"{nodesc} without description, {status_missing} missing status, "
            f"{invalid} malformed records, {dup} duplicate ids, "
            f"{extra_endpoint_rows} row(s) dropped extra remote endpoints, "
            f"{refused_endpoint_rows} row(s) declared an endpoint that was not publishable)")

def _http_get(url, timeout, etag=None, source_id=None):
    """GET `url`; return (status, headers, body).

    A conditional request answering 304 comes back as status 304 with an empty
    body whether urllib raises it (HTTPError) or returns it directly. The URL,
    every redirect, resolved destination addresses, and response size are
    bounded before the response reaches the snapshot layer.
    """
    _validate_request_url(url, source_id)
    headers = {"User-Agent": "Mozilla/5.0"}
    if etag:
        headers["If-None-Match"] = etag
    req = urllib.request.Request(url, headers=headers)
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({}),
        _SourceHTTPSHandler(source_id),
        _SourceRedirectHandler(source_id),
    )
    try:
        resp = opener.open(req, timeout=timeout)
    except urllib.error.HTTPError as exc:
        if exc.code == 304:
            try:
                return 304, exc.headers, b""
            finally:
                exc.close()
        exc.close()
        raise
    try:
        status = resp.getcode()
        if status == 304:
            return 304, resp.headers, b""
        length = resp.headers.get("Content-Length")
        if length is not None:
            try:
                declared_length = int(length)
            except (TypeError, ValueError) as exc:
                raise ValueError(f"invalid Content-Length from {url}: {length!r}") from exc
            if declared_length < 0:
                raise ValueError(f"invalid Content-Length from {url}: {length!r}")
            if declared_length > MAX_RESPONSE_BYTES:
                raise ValueError(
                    f"response from {url} exceeds the {MAX_RESPONSE_BYTES} byte limit")
        body = resp.read(MAX_RESPONSE_BYTES + 1)
        if len(body) > MAX_RESPONSE_BYTES:
            raise ValueError(f"response from {url} exceeds the {MAX_RESPONSE_BYTES} byte limit")
        return status, resp.headers, body
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
    # Validate even on replay: an old recording must not let a newly supplied
    # sitemap URL bypass the source's destination policy. DNS is checked only
    # when opening a network connection, so a cache-only replay stays offline.
    _validate_request_url(url, source_id, check_dns=False)
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
            status, headers, body = _http_get(url, timeout, source_id=source_id)
        except Exception as exc:
            snapshot_store.log_failure(root, url, "fetch", str(exc))
            _mark_incomplete(source_id)
            raise
        _record_snapshot(root, url, source_id, status, headers, body, started_at)
        return body

    etag = rec_meta["fetch"].get("etag") if rec_meta is not None else None
    started_at = time.time()
    try:
        status, headers, body = _http_get(url, timeout, etag, source_id=source_id)
    except Exception as exc:
        if rec_meta is None:
            snapshot_store.log_failure(root, url, "refresh", str(exc))
            _mark_incomplete(source_id)
            raise
        print(f"    {label}: STALE -- refresh failed ({exc}); keeping snapshot "
              f"recorded {rec_meta['fetch']['fetchedAt']}")
        _STALE_SOURCES.add(source_id)
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



def _obtain_quiet(url, source_id, timeout, refresh, label):
    """obtain() without its per-fetch progress lines.

    The bulk sources below record tens of thousands of snapshots; one
    printed line per fetch would bury the run's real progress. Snapshots
    and failure records are still written exactly as obtain() writes them.
    """
    with contextlib.redirect_stdout(io.StringIO()):
        return obtain(url, source_id, timeout, refresh, label)


def _obtain_bulk(url, source_id, timeout, refresh, label, attempts=4,
                 base_delay=3.0, fatal=False):
    """_obtain_quiet with bounded exponential backoff for transient errors.

    fatal=True re-raises after the retries: it is for the few requests the
    whole block depends on (a CDX page, a sitemap), where silently losing
    one would understate coverage. fatal=False returns None so a single
    flaky per-item fetch skips that item instead of failing a multi-hour
    run. SnapshotIntegrityError is never retried -- a corrupt recording is
    a local bug, not a transient network condition.
    """
    delay = base_delay
    for attempt in range(1, attempts + 1):
        try:
            return _obtain_quiet(url, source_id, timeout, refresh, label)
        except snapshot_store.SnapshotIntegrityError:
            raise
        except Exception as exc:
            if attempt == attempts:
                snapshot_store.log_failure(snapshot_store.default_root(),
                                           url, label, str(exc))
                _mark_incomplete(source_id)
                if fatal:
                    raise
                return None
            time.sleep(delay)
            delay *= 2
    return None


# SNAPSHOT LIFECYCLE -- WHAT THIS RUN CONSUMED
#
# Fresh snapshots are recorded `partial` (snapshot_store.write_snapshot) and
# are only promoted to `healthy` + `completedAt` + `itemCount` by
# mark_ingested AFTER their bytes were actually parsed. The completion step
# used to run over one URL per source, which left
# every record-level snapshot -- skills.sh /api/download records, Wayback
# page replays, registry cursor pages, sitemap and CDX pagination documents --
# stuck at `partial` forever even though the build parsed them. The ledger
# below records exactly what the run parsed (registered at each parse site,
# not at fetch time: bytes that failed to decode are never "consumed"), and
# finalize_consumed_snapshots() promotes them once the build has completed.
#
# A run that dies before finalization leaves its records `partial` -- never a
# false `healthy`.
_CONSUMED = []        # ordered [(url, source_id), ...] for reproducible output
_CONSUMED_URLS = set()  # membership set (the list alone would be O(n^2))
_ROW_ATTRIB = {}      # url -> dataset rows this run attributed to that url


def _reset_run_ledger():
    _CONSUMED.clear()
    _CONSUMED_URLS.clear()
    _ROW_ATTRIB.clear()
    _INCOMPLETE_SOURCES.clear()
    _STALE_SOURCES.clear()
    _PROBE_CACHE.clear()


def _print_ingestion_status(refresh, skip_sources, only_source):
    """Emit machine-readable completeness/freshness facts for this run."""
    partial_scope = bool(skip_sources) or only_source is not None
    if _INCOMPLETE_SOURCES:
        completeness = "incomplete"
    elif partial_scope:
        completeness = "partial"
    else:
        completeness = "complete"
    report = {
        "format": "litespm-catalog-ingestion-status/v1",
        "completeness": completeness,
        "freshnessChecked": bool(refresh),
        "incompleteSources": sorted(_INCOMPLETE_SOURCES),
        "staleSources": sorted(_STALE_SOURCES),
        "skippedSources": sorted(skip_sources),
        "onlySource": only_source,
    }
    print("CATALOG_INGESTION_STATUS " + json.dumps(report, sort_keys=True))


def _consume(url, source_id):
    """Register `url` as parsed by this run (called after a successful decode).

    Registration happens where the parse runs, never inside obtain(): a
    recorded body whose JSON/HTML decode raises is a fetch that did not
    produce usable content, and its snapshot must stay `partial`.
    """
    if url not in _CONSUMED_URLS:
        _CONSUMED_URLS.add(url)
        _CONSUMED.append((url, source_id))


def attribute_rows(url, rows):
    """Attribute `rows` dataset rows to the snapshot that carried their bytes.

    `itemCount` is per-document: a README/manifest/per-record response gets
    the rows it contributed; navigation documents (sitemap index, sub-sitemaps,
    CDX pages, the registry cursor chain) get 0 because they enumerated URLs,
    not rows. Counts accumulate if a URL is attributed at several sites.
    """
    _ROW_ATTRIB[url] = _ROW_ATTRIB.get(url, 0) + rows


def finalize_consumed_snapshots(root):
    """Promote every snapshot this run parsed to `healthy` with its real rows.

    Returns `(promoted, promoted_rows, unchanged, missing)`. Only snapshots
    that exist are updated (mark_ingested never creates an entry), a record
    already healthy with the same count is left byte-for-byte untouched, and
    a consumed URL with no recorded snapshot is reported as `missing` rather
    than fabricated.
    """
    promoted = promoted_rows = unchanged = missing = 0
    for url, _source_id in list(_CONSUMED):
        rows = _ROW_ATTRIB.get(url, 0)
        updated, changed = snapshot_store.mark_ingested(root, url, rows)
        if updated is None:
            missing += 1
            continue
        if changed:
            promoted += 1
            promoted_rows += rows
        else:
            unchanged += 1
    return promoted, promoted_rows, unchanged, missing


def _frontmatter_block(body):
    """The raw YAML frontmatter text of a SKILL.md document, or "" .

    Shared by the two frontmatter readers below: the skills.sh row summary
    (fold-aware, a display field) and the verification parse (Go-mirroring,
    an installability gate). A document that does not open with `---` has no
    frontmatter and therefore no publishable description.
    """
    if not isinstance(body, str):
        return ""
    if not body.startswith("---"):
        return ""
    end = body.find("\n---", 3)
    return body[3:end] if end > 0 else ""


def _frontmatter_value(front, key):
    """Single-line or folded value of `key` inside a frontmatter block.

    A folded/literal YAML block carries its text on the following indented
    lines; those are collected instead of the indicator itself. Quoted
    scalars are unquoted only when the quotes match.
    """
    m = re.search(rf"^{re.escape(key)}:[ \t]*(.*)$", front, re.M)
    if not m:
        return None
    value = m.group(1).strip()
    if value in ("", ">", ">-", "|", "|-") or re.fullmatch(r"[>|][+-]?\d?", value or ""):
        parts = []
        for line in front[m.end():].splitlines():
            if not line.strip():
                continue
            if not line.startswith((" ", "\t")):
                break
            parts.append(line.strip())
        value = " ".join(parts).strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", chr(34)):
        value = value[1:-1].strip()
    value = re.sub(r"\s+", " ", value).strip()
    return value or None


def skill_md_description(payload):
    """Pull `description` from the SKILL.md inside a skills.sh download
    payload, or None when the payload carries no usable description.

    A row without a summary would be a thin row and the dataset has none,
    so an item whose SKILL.md lacks a frontmatter description is skipped.
    """
    try:
        data = json.loads(payload)
    except (ValueError, TypeError):
        return None
    files = data.get("files") if isinstance(data, dict) else None
    if not isinstance(files, list):
        return None
    main = None
    for f in files:
        if isinstance(f, dict) and f.get("path") == "SKILL.md":
            main = f
            break
    if main is None:
        for f in files:
            if isinstance(f, dict) and str(f.get("path", "")).endswith("/SKILL.md"):
                main = f
                break
    if main is None:
        return None
    return _frontmatter_value(_frontmatter_block(main.get("contents") or ""),
                              "description")


# SKILL VERIFICATION -- WHAT THE BUILD ACTUALLY READ
#
# A skill row may only claim `metadata_verified` when this run opened the
# SKILL.md at the path the row advertises. Everything else stays
# `discovery_only`: the catalog publishes no version, no command and no
# verification upstream did not publish. The rules below mirror the install
# side exactly, because the row promises an install the CLI must be able to
# complete:
#
#   internal/skills/install.go:74-181   ParseSkillSource   -> skill_source_parses_git
#   internal/skills/install.go:219-228  splitGitTreeBrowsePath -> _github_tree_path
#   internal/skills/install.go:258-294  DiscoverSkills     -> skill_md_frontmatter
#   internal/skills/install.go:297-303  SanitizeSkillName  -> sanitize_skill_name
#   internal/skills/loader.go:30-143    ParseSkillMD       -> skill_md_frontmatter
#   cmd/litespm/install_skill.go:85-98  Source.URL + Git gate (publisher.url)
#   cmd/litespm/install_skill.go:289-305 selectSkillForListing name match
#
# Residual risk this deliberately does not re-litigate (research §4.4): a
# multi-skill repository whose listing name cannot be sanitized falls through
# to `selectSkillForListing`'s single-skill branch, and a skill tree larger
# than 32 MiB, a `git clone --branch <sha>` pin, a missing agent skill
# directory or a policy refusal still stop the install after the catalog's
# claim. Those are install-time conditions, not producer facts.
_SKILLMD_MAX_LINE_BYTES = 64 * 1024  # bufio.Scanner's MaxScanTokenSize
_SKILL_NAME_RE = re.compile(r"[a-z0-9][a-z0-9-]{0,63}\Z")

# Exact hostnames, mirroring internal/skills knownGitHosts (install.go:60-68).
_KNOWN_GIT_HOSTS = frozenset({
    "github.com", "www.github.com", "gitlab.com", "bitbucket.org",
    "codeberg.org", "git.sr.ht", "gitea.com",
})
_GITHUB_HOSTS = frozenset({"github.com", "www.github.com"})
_VALID_OWNER_REPO = re.compile(r"[A-Za-z0-9_.-]+\Z")

# officialskills.sh publishes one page per skill for this feed's rows.
OFFICIALSKILLS_HOSTS = frozenset({"officialskills.sh", "www.officialskills.sh"})


def skill_md_frontmatter(body):
    """(name, description) from a SKILL.md's frontmatter, or (None, None).

    Mirrors internal/skills (loader.go:30-143, install.go:283-285) far enough
    that the producer never verifies more than the installer would accept:
      * the file must open with a `---` line -- a body with no frontmatter
        can carry no `description`, which is the DiscoverSkills gate;
      * a key is the text before the FIRST colon of the trimmed line and its
        value is trimmed of spaces and of `\"'` backticks (loader.go:82-87);
      * the block ends at the closing `---`, so a `description:` line in the
        body is not a description;
      * BOTH fields must be non-empty. ParseSkillMD falls back to the first
        `# ` header and then to "unnamed-skill" for a missing name, but a
        promoted row promises a named skill -- the producer demands the
        frontmatter publish the name instead of adopting that fallback;
      * a line longer than bufio.Scanner's 64 KiB token limit makes the Go
        parse fail outright, so that skill is not discoverable (loader.go:32-44).
    """
    if isinstance(body, (bytes, bytearray)):
        text = bytes(body).decode("utf-8", errors="replace")
    elif isinstance(body, str):
        text = body
    else:
        return None, None
    lines = [ln.rstrip("\r") for ln in text.split("\n")]
    if lines and lines[-1] == "":
        lines.pop()  # a trailing newline is a terminator, not an empty line
    if not lines:
        return None, None
    for line in lines:
        if len(line.encode("utf-8")) > _SKILLMD_MAX_LINE_BYTES:
            return None, None
    if lines[0].strip() != "---":
        return None, None
    name = description = ""
    for line in lines[1:]:
        trimmed = line.strip()
        if trimmed == "---":
            break
        colon = trimmed.find(":")
        if colon == -1:
            continue
        key = trimmed[:colon].lower().strip()
        value = trimmed[colon + 1:].strip().strip("\"'`")
        if key == "name":
            name = value
        elif key == "description":
            description = value
    if not name or not description:
        return None, None
    return name, description


def sanitize_skill_name(name):
    """The skill directory name an install would use, or None if illegal.

    Mirrors skills.SanitizeSkillName (install.go:297-303): the name is
    lowercased, trimmed, and must match ^[a-z0-9][a-z0-9-]{0,63}$.
    """
    if not isinstance(name, str):
        return None
    value = name.strip().lower()
    if _SKILL_NAME_RE.fullmatch(value):
        return value
    return None


def _split_at_ref(s):
    """`base@ref` split on the last '@' (install.go:185-191)."""
    idx = s.rfind("@")
    if idx <= 0 or idx == len(s) - 1:
        return s, ""
    return s[:idx], s[idx + 1:]


def _github_tree_path(path):
    """(repo_path, ref, subpath) for a GitHub browse path, else None.

    Mirror of splitGitTreeBrowsePath (install.go:219-228): at least four
    segments, the third is `tree` or `blob`, and owner/repo/ref are non-empty.
    """
    parts = path.strip("/").split("/")
    if len(parts) < 4 or not parts[0] or not parts[1] or not parts[3]:
        return None
    if parts[2] not in ("tree", "blob"):
        return None
    return f"{parts[0]}/{parts[1]}", parts[3], "/".join(parts[4:])


def _skill_source_parts(raw):
    """Pins (host, path, pin) shared by the source parsers below.

    Query pins (`ref`/`pin`/`commit`/`version`) are stripped first, then a
    path `@ref`, then trailing slashes -- the same precedence
    ParseSkillSource applies (install.go:116-136).
    """
    s = str(raw or "").strip()
    if not s or "://" not in s:
        return None
    if not s.startswith("https://"):
        return None
    try:
        u = urllib.parse.urlsplit(s)
    except ValueError:
        return None
    if not u.hostname or u.username is not None or u.password is not None:
        return None
    pin = ""
    query = urllib.parse.parse_qs(u.query, keep_blank_values=True)
    for key in ("ref", "pin", "commit", "version"):
        values = query.get(key)
        value = (values[0] if values else "").strip()
        if value:
            pin = value
            break
    path = u.path
    if not pin:
        idx = path.rfind("@")
        if idx > 0:
            path = path[:idx]
    return u.netloc.lower(), path.rstrip("/"), pin


def skill_source_parses_git(raw):
    """True when skills.ParseSkillSource(raw) would report Git == true.

    Python mirror of install.go:74-181 for the one decision that decides
    whether `litespm install` can clone at all: https only, owner/repo
    shorthand or a recognized git host (which gets the inferred `.git`
    suffix), a GitHub /tree/ or /blob/ browse path resolving to its
    repository, and otherwise only a path that already ends in `.git`.
    An unparsable source is False: an unverified row is never promoted.
    """
    s = str(raw or "").strip()
    if not s:
        return False
    if "://" not in s:
        base, _pin = _split_at_ref(s)
        parts = base.split("/")
        return (len(parts) == 2
                and bool(_VALID_OWNER_REPO.fullmatch(parts[0]))
                and bool(_VALID_OWNER_REPO.fullmatch(parts[1])))
    parts = _skill_source_parts(s)
    if parts is None:
        return False
    host, path, _pin = parts
    if host in _GITHUB_HOSTS and _github_tree_path(path) is not None:
        return True
    if host in _KNOWN_GIT_HOSTS:
        return True  # `.git` is inferred for a known host (install.go:167-169)
    return path.endswith(".git")


def github_tree_facts(raw):
    """(owner/repo, ref, subpath) for a GitHub /tree/ or /blob/ URL.

    The ref is the browse ref unless an explicit @ref / ?ref= pin wins
    (install.go:143-163). Returns None for anything else.
    """
    parts = _skill_source_parts(raw)
    if parts is None:
        return None
    host, path, pin = parts
    if host not in _GITHUB_HOSTS:
        return None
    tree = _github_tree_path(path)
    if tree is None:
        return None
    owner_repo, tree_ref, subpath = tree
    return owner_repo, (pin or tree_ref), subpath


def github_repo_facts(raw):
    """(owner/repo, pin) for a plain GitHub repository URL, else None.

    Only a two-segment path counts: a deeper path that is not a browse URL
    addresses something other than a repository, and guessing its clone root
    would advertise a source the installer may not be able to fetch.
    """
    parts = _skill_source_parts(raw)
    if parts is None:
        return None
    host, path, pin = parts
    if host not in _GITHUB_HOSTS:
        return None
    if _github_tree_path(path) is not None:
        return None
    segments = path.strip("/").split("/")
    if len(segments) != 2 or not all(_VALID_OWNER_REPO.fullmatch(seg)
                                     for seg in segments):
        return None
    owner, repo = segments
    if repo.endswith(".git"):
        repo = repo[: -len(".git")]
    if not owner or not repo:
        return None
    return f"{owner}/{repo}", pin


_GH_LINK_RE = re.compile(r"https://github\.com/[^\s)\"'<>\]]+")
_NPX_SKILLS_ADD_RE = re.compile(r"npx\s+skills\s+add\s+([^\s]+)")


def officialskills_page_source(page_text, skill_slug):
    """The GitHub tree/repo link an officialskills.sh page publishes.

    Preference follows what the page puts forward for this skill:
      1. a /tree/ browse link whose last path segment is the listing slug --
         exactly the shape ParseSkillSource turns into CloneURL + Pin +
         Subpath (install.go:143-163);
      2. the bare repository named by the page's own
         `npx skills add <repo>` command, which then has to prove its skill
         path (resolve_skill_row probes depth <= 2 from the repo root).
    Anything else on the page -- navigation, footers, other skills -- is not
    this skill's source, and None means the page published nothing usable.
    """
    if isinstance(page_text, (bytes, bytearray)):
        text = bytes(page_text).decode("utf-8", errors="replace")
    elif isinstance(page_text, str):
        text = page_text
    else:
        return None
    slug = str(skill_slug or "").strip()
    if slug:
        for match in _GH_LINK_RE.finditer(text):
            url = match.group(0).rstrip(".,;:")
            facts = github_tree_facts(url)
            if facts is None:
                continue
            _repo, _ref, subpath = facts
            if subpath and subpath.split("/")[-1] == slug:
                return url
    match = _NPX_SKILLS_ADD_RE.search(text)
    if match:
        token = match.group(1).strip("\"'`,;")
        link = _GH_LINK_RE.match(token)
        if link:
            url = link.group(0).rstrip(".,;:")
            if github_repo_facts(url) is not None or github_tree_facts(url) is not None:
                return url
    return None


# Verification probes answer one question per URL (does SKILL.md exist and
# parse at the claimed path?) through the same machinery every other fetch
# uses: _validate_request_url (source allowlist + public-IP + redirect
# policy), _http_get (pinned DNS, 16 MiB cap), and the snapshot store (a 200
# is recorded once and replayed offline afterwards). HTTP 404/410 is a
# definitive answer for this run rather than a recording -- the store refuses
# non-200 bodies, so an absent path is re-asked on the next run instead of
# being frozen as a permanent negative.
_VERIFY_ATTEMPTS = 2
_VERIFY_DELAY = 0.05  # seconds of pacing per network attempt
_PROBE_CACHE = {}     # url -> (status, (name, description) | None)


def verify_fetch(url, source_id, timeout, refresh, label):
    """Fetch one verification probe; return ("ok"|"absent"|"error", body|None).

    * recorded + replay      -> ("ok", recorded bytes), no network;
    * recorded + --refresh   -> conditional request; 304 or a digest match
                                keeps the recording, a failure keeps it too
                                (STALE, logged) exactly as obtain() does;
    * 200                    -> recorded and returned;
    * 404 / 410              -> ("absent", None): the path does not exist, so
                                no row may be promoted from it. Nothing is
                                recorded and nothing is attributed;
    * anything else          -> bounded retries, a failures.jsonl entry, the
                                source marked incomplete, ("error", None):
                                an unreachable upstream proves nothing, so it
                                never promotes.
    """
    # Validate before the snapshot lookup as obtain() does: a recorded probe
    # must not let a stale or hostile URL bypass this source's policy.
    _validate_request_url(url, source_id, check_dns=False)
    root = snapshot_store.default_root()
    rec_body = rec_meta = None
    try:
        rec_body, rec_meta = snapshot_store.load_snapshot(root, url)
    except snapshot_store.SnapshotMissing:
        pass
    except snapshot_store.SnapshotIntegrityError as exc:
        if not refresh:
            raise
        print(f"    {label}: recorded probe REFUSED ({exc}); re-verifying")
        snapshot_store.log_failure(root, url, "probe-integrity", str(exc))
        rec_body = rec_meta = None

    if rec_meta is not None and not refresh:
        return "ok", rec_body

    etag = rec_meta["fetch"].get("etag") if rec_meta is not None else None
    started_at = time.time()
    time.sleep(_VERIFY_DELAY)  # pacing: probes are per-row, never a crawl
    last_error = "probe not attempted"
    for attempt in range(1, _VERIFY_ATTEMPTS + 1):
        if attempt > 1:
            time.sleep(_VERIFY_DELAY * (attempt - 1))
        try:
            status, headers, body = _http_get(url, timeout, etag,
                                              source_id=source_id)
        except urllib.error.HTTPError as exc:
            code = exc.code
            exc.close()
            if code in (404, 410):
                if rec_meta is not None and refresh:
                    # Upstream dropped the file since it was recorded: this
                    # run's honest answer is "absent", not the stale 200.
                    snapshot_store.log_failure(root, url, "probe",
                                               f"HTTP {code} on refresh")
                return "absent", None
            last_error = f"HTTP {code}"
            continue
        except Exception as exc:  # noqa: BLE001 - every failure is a refusal
            last_error = f"{type(exc).__name__}: {exc}"
            continue
        if status == 304:
            if rec_meta is None:
                last_error = "upstream answered 304 but nothing is recorded"
                continue
            return "ok", rec_body
        if status != 200:
            last_error = f"HTTP {status}"
            continue
        with contextlib.redirect_stdout(io.StringIO()):
            _record_snapshot(root, url, source_id, status, headers, body,
                             started_at)
        return "ok", body

    snapshot_store.log_failure(root, url, "probe", str(last_error))
    _mark_incomplete(source_id)
    return "error", None


def probe_frontmatter(url, source_id, refresh):
    """(status, (name, description) | None) for a raw SKILL.md probe URL.

    Cached per run so a repository several rows point at is read once; every
    parsed body is registered in the run ledger, and the parse result is the
    one the installer's own rules would produce.
    """
    hit = _PROBE_CACHE.get(url)
    if hit is not None:
        return hit
    status, body = verify_fetch(url, source_id, 25, refresh, "SKILL.md probe")
    if status != "ok":
        result = (status, None)
    else:
        # The bytes were parsed here, so this snapshot is consumed here.
        _consume(url, source_id)
        result = ("ok", skill_md_frontmatter(body))
    _PROBE_CACHE[url] = result
    return result


def skill_probe_urls(raw, slug):
    """The SKILL.md URLs that prove `raw` (at most three, depth <= 2).

    A /tree/ (or /blob/) source is scoped to one directory by the installer
    (install_skill.go:110-114 joins src.Subpath onto the clone root), so a
    single probe at that path is the whole claim. A bare repository is
    discovered with DiscoverSkills at depth <= 2 from the repo root
    (install.go:269), so the three probes are exactly the layouts that
    depth can reach: `<slug>/SKILL.md`, `skills/<slug>/SKILL.md` and a root
    `SKILL.md`. Only raw.githubusercontent.com can answer them; an `?ref=`
    or `@ref` pin is queried at that ref, otherwise at `HEAD`.
    """
    tree = github_tree_facts(raw)
    if tree is not None:
        owner_repo, ref, subpath = tree
        if subpath.lower().endswith((".md", ".markdown")):
            rel = subpath          # the browse URL already names the file
        else:
            rel = f"{subpath}/SKILL.md" if subpath else "SKILL.md"
        return [f"https://raw.githubusercontent.com/{owner_repo}/{ref}/{rel}"]
    repo = github_repo_facts(raw)
    if repo is None:
        return []
    owner_repo, pin = repo
    ref = pin or "HEAD"
    candidates = []
    if slug:
        candidates.append(f"{slug}/SKILL.md")
        candidates.append(f"skills/{slug}/SKILL.md")
    candidates.append("SKILL.md")
    urls, seen = [], set()
    for rel in candidates:
        url = f"https://raw.githubusercontent.com/{owner_repo}/{ref}/{rel}"
        if url not in seen:
            seen.add(url)
            urls.append(url)
    return urls


_VERIFY_TALLY_KEYS = (
    "considered", "promoted", "pages", "page_link", "page_nolink",
    "page_absent", "page_error", "notgit", "unprobeable", "probe_absent",
    "bad_frontmatter", "mismatch", "probe_error", "nosource",
)


def new_verify_tally():
    return {key: 0 for key in _VERIFY_TALLY_KEYS}


def print_verify_tally(tally, label):
    """One line of honest accounting for a verification block."""
    if not tally["considered"]:
        return
    unverifiable = tally["notgit"] + tally["unprobeable"] + tally["nosource"]
    print(f"  {label}: {tally['promoted']} of {tally['considered']} promoted "
          f"({tally['page_link']} officialskills.sh link(s) read, "
          f"{tally['page_nolink']} page(s) publishing no repository link, "
          f"{tally['probe_absent']} without SKILL.md at the claimed path, "
          f"{tally['bad_frontmatter']} without usable frontmatter, "
          f"{tally['mismatch']} name mismatch(es), "
          f"{unverifiable} with no verifiable git source, "
          f"{tally['page_error'] + tally['probe_error']} fetch failure(s))")


def resolve_skill_row(row, source_id, refresh, tally):
    """Promote a skill row only on evidence this run actually read.

    Returns True when `row` now claims `metadata_verified`. Every other
    outcome leaves the row exactly as it was (`discovery_only`) and is
    counted in `tally`. The steps, in order:

      1. an officialskills.sh row first adopts the GitHub tree/repo link its
         own page publishes (A1 ②); a page that publishes nothing usable
         keeps its page URL as skillSource, which is not a git source;
      2. the source must parse as a git source the installer can clone
         (`skill_source_parses_git`, the ParseSkillSource mirror);
      3. SKILL.md must answer 200 with installable frontmatter (a name the
         installer's own sanitizer accepts and a description -- the
         DiscoverSkills gate);
      4. no positive name mismatch: when BOTH the listing name and the
         document name sanitize, they must agree, or
         `selectSkillForListing` would refuse this install
         (install_skill.go:289-305).

    On promotion `publisher.url` is set to the git source, because
    convertRow (internal/catalogbuild/dataset.go:206-209) takes
    Publisher.URL FIRST for Source.URL and install_skill.go:85-94 parses
    exactly that: promoting a row while leaving a page URL behind would show
    an install that fails with "not a git repository". No version, no
    command, no ref and no runtime is ever written here.
    """
    tally["considered"] += 1
    raw = str(row.get("skillSource") or "").strip()
    if not raw:
        tally["nosource"] += 1
        return False

    try:
        host = (urllib.parse.urlsplit(raw).hostname or "").lower()
    except ValueError:
        host = ""

    if host in OFFICIALSKILLS_HOSTS:
        tally["pages"] += 1
        status, body = verify_fetch(raw, source_id, 25, refresh,
                                    "officialskills.sh skill page")
        if status != "ok":
            tally["page_absent" if status == "absent" else "page_error"] += 1
            return False
        # The page was parsed here: consumed here, with the rows it changed.
        _consume(raw, source_id)
        link = officialskills_page_source(body, row.get("slug") or "")
        if not link:
            tally["page_nolink"] += 1
            attribute_rows(raw, 0)
            return False
        row["skillSource"] = link
        attribute_rows(raw, 1)
        tally["page_link"] += 1
        raw = link

    if not skill_source_parses_git(raw):
        tally["notgit"] += 1
        return False
    try:
        host = (urllib.parse.urlsplit(raw).hostname or "").lower()
    except ValueError:
        host = ""
    if host not in _GITHUB_HOSTS:
        # Clonable, perhaps -- but no allowlisted host can serve a content
        # probe for it, so this run cannot prove the SKILL.md exists.
        tally["unprobeable"] += 1
        return False

    probes = skill_probe_urls(raw, row.get("slug") or "")
    if not probes:
        tally["unprobeable"] += 1
        return False
    listing_name = sanitize_skill_name(row.get("name") or "")
    for probe in probes:
        status, front = probe_frontmatter(probe, source_id, refresh)
        if status == "error":
            tally["probe_error"] += 1
            return False
        if status == "absent":
            tally["probe_absent"] += 1
            continue
        name, description = front if front else (None, None)
        got = sanitize_skill_name(name or "")
        if not got or not description:
            tally["bad_frontmatter"] += 1
            continue
        if listing_name is not None and got != listing_name:
            tally["mismatch"] += 1
            continue
        row["installability"] = METADATA_VERIFIED
        publisher = row.setdefault("publisher", {})
        if isinstance(publisher, dict):
            publisher["url"] = raw
        attribute_rows(probe, 1)
        tally["promoted"] += 1
        return True
    return False


def mcpservers_page_fields(page):
    """(name, summary) parsed from a captured mcpservers.org directory page.

    Both must be present: a title without a description (or the reverse) is
    not enough to publish, and inventing the missing half is forbidden.
    """
    text = page.decode("utf-8", errors="replace")
    name = None
    m = re.search(r"<title[^>]*>(.*?)</title>", text, re.S | re.I)
    if m:
        title = html.unescape(re.sub(r"\s+", " ", m.group(1))).strip()
        # Observed shape: "<Server> | Awesome MCP Servers" -- take the
        # segment before the site separator, never the site's own name.
        name = title.split("|")[0].strip() or None
        if name and name.lower().startswith("mcpservers.org"):
            name = None
    summary = None
    dq, sq = chr(34), chr(39)
    for pattern in (
        r'<meta[^>]+name=' + dq + r'description' + dq + r'[^>]+content=' + dq + r'([^' + dq + r']*)' + dq,
        r'<meta[^>]+content=' + dq + r'([^' + dq + r']*)' + dq + r'[^>]+name=' + dq + r'description' + dq,
    ):
        m = re.search(pattern, text, re.S | re.I)
        if m and m.group(1).strip():
            summary = html.unescape(re.sub(r"\s+", " ", m.group(1))).strip()
            break
    if not name or not summary:
        return None, None
    return name, summary


# --- mcpmarket.com: page parsing, row construction, crawl budget ---

def mcpmarket_listing_parts(url, source_id=FEED_MCPMARKET[1]):
    """("mcp"|"skill", path, slug) for a strict mcpmarket listing URL.

    Returns (None, "", "") for anything else. The URL is re-validated against
    the source's exact host allowlist (HTTPS, port 443, no credentials, no
    fragment, no query -- DNS is not needed to decide shape) and the parsed
    path must fullmatch one of the two listing shapes, so a sitemap entry can
    only ever name /server/<slug> or /tools/skills/<slug> on an allowed host.
    """
    if _is_source_sitemap_url(url, source_id, MCPMARKET_SERVER_PATH):
        path = urllib.parse.urlsplit(url).path
        return "mcp", path, path[len("/server/"):]
    if _is_source_sitemap_url(url, source_id, MCPMARKET_SKILL_PATH):
        path = urllib.parse.urlsplit(url).path
        return "skill", path, path[len("/tools/skills/"):]
    return None, "", ""


def _mcpmarket_jsonld_app(text):
    """First JSON-LD SoftwareApplication object on the page, or None.

    The block is the page's own structured declaration of the item; an
    unparsable or differently typed block is skipped, never repaired.
    """
    for block in re.findall(r"<script[^>]*ld\+json[^>]*>(.*?)</script>",
                            text, re.S | re.I):
        try:
            data = json.loads(block)
        except (ValueError, TypeError):
            continue
        entries = data if isinstance(data, list) else [data]
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            if str(entry.get("@type", "")).strip() != "SoftwareApplication":
                continue
            return entry
    return None


def _mcpmarket_meta_description(text):
    """The meta description content attribute, whichever order it appears in."""
    for tag in re.findall(r"<meta[^>]*>", text, re.I):
        if re.search(r"name=[\"']description[\"']", tag, re.I):
            m = re.search(r"content=[\"']([^\"']*)[\"']", tag, re.I)
            if m and m.group(1).strip():
                return m.group(1)
    return None


def mcpmarket_page_fields(page):
    """(name, summary, publisher_name, publisher_url) from a listing page.

    Everything returned is read out of the page itself: the JSON-LD
    SoftwareApplication name/description/author when it parses, else the
    <title> (segment before the site separator) and the meta description.
    name AND summary must both be present -- a page publishing only half of a
    row is reported as (None, None, ...) and the caller skips and counts it,
    never completing it from the other half. publisher_name/publisher_url are
    empty when the page declares no author; the caller then attributes the
    row to the directory that published the listing.
    """
    text = page.decode("utf-8", errors="replace")
    name = summary = pub_name = pub_url = ""
    app = _mcpmarket_jsonld_app(text)
    if app is not None:
        jname = app.get("name")
        if isinstance(jname, str) and jname.strip():
            name = re.sub(r"\s+", " ", jname).strip()
        jdesc = app.get("description")
        if isinstance(jdesc, str) and jdesc.strip():
            summary = re.sub(r"\s+", " ", jdesc).strip()
        author = app.get("author")
        if isinstance(author, dict):
            aname = author.get("name")
            if isinstance(aname, str) and aname.strip():
                pub_name = re.sub(r"\s+", " ", aname).strip()
            aurl = author.get("url")
            if isinstance(aurl, str) and aurl.startswith("https://"):
                pub_url = aurl
        elif isinstance(author, str) and author.strip():
            pub_name = re.sub(r"\s+", " ", author).strip()
    if not name:
        m = re.search(r"<title[^>]*>(.*?)</title>", text, re.S | re.I)
        if m:
            title = html.unescape(re.sub(r"\s+", " ", m.group(1))).strip()
            # Observed shapes: "<Name>: <tagline>" and
            # "<Name>: <tagline> | Claude Code Skill" -- take the segment
            # before the site separator, never the site's own name.
            title = title.split("|")[0].strip()
            if title and not title.lower().startswith("mcpmarket"):
                name = title
    if not summary:
        meta = _mcpmarket_meta_description(text)
        if meta:
            summary = html.unescape(re.sub(r"\s+", " ", meta)).strip()
    if not name or not summary:
        return None, None, "", ""
    return name, summary, pub_name, pub_url


def mcpmarket_row(kind, slug, item_id, page_url, name, summary,
                  pub_name, pub_url):
    """One discovery_only dataset row built only from a page's own content.

    A directory page carries no launch line, no version, no transport and no
    star signal: every one of those fields is null and the row is
    discovery_only, exactly like the other directory sources (the installer
    refuses it with LPSM-NOT-INSTALLABLE). The publisher is the page's
    declared author when it publishes one, else the directory that published
    the listing; provenance stays a third-party list claim, so `verified` is
    false either way.
    """
    row = {
        "id": item_id,
        "name": name,
        "slug": canonical_slug(slug.lower()),
        "kind": kind,
        "summary": summary,
        # The listing pages carry no domain category signal (the JSON-LD
        # applicationCategory is a client type such as "ServerApplication"):
        # the same defaults the other directory sources use, not a claim
        # about the capability's domain.
        "category": "Agent Skills" if kind == "skill" else "Developer Tools",
        "publisher": publisher_obj(pub_name or "mcpmarket.com",
                                   pub_url or page_url, PROV_LIST),
        "stars": None,
        "version": None,
        "installability": DISCOVERY_ONLY,
        "source": FEED_MCPMARKET[1],
    }
    if kind == "skill":
        # Mirror of the skills.sh skill rows: the listing page is where the
        # record was read (the directory publishes no git manifest).
        row["skillSource"] = page_url
    else:
        row.update({"transport": None, "runtime": None,
                    "command": None, "args": None})
    return row


def mcpmarket_max_page_fetches():
    """Per-run fetch budget for mcpmarket.com listing pages (see the note at
    MCPMARKET_DEFAULT_MAX_PAGE_FETCHES). Fails closed on a bad override."""
    raw = os.environ.get("MCPMARKET_MAX_PAGE_FETCHES", "").strip()
    if not raw:
        return MCPMARKET_DEFAULT_MAX_PAGE_FETCHES
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(
            f"MCPMARKET_MAX_PAGE_FETCHES must be an integer, got {raw!r}") from exc
    if value < 0:
        raise ValueError(
            "MCPMARKET_MAX_PAGE_FETCHES must be >= 0 (0 means uncapped)")
    return value


def _mcpmarket_needs_fetch(url, refresh):
    """True when fetching `url` would touch the network during this run.

    Replay mode fetches only what the snapshot store has not recorded (a
    corrupt recording still counts, so obtain() can refuse or replace it
    exactly as its own contract says). Refresh mode issues a conditional
    request for every URL, so every URL consumes crawl budget.
    """
    if refresh:
        return True
    try:
        snapshot_store.load_snapshot(snapshot_store.default_root(), url)
    except snapshot_store.SnapshotError:
        return True
    return False


def build_full_catalog(refresh=False, skip_sources=frozenset(), only_source=None):
    print("Ingesting real registries...")
    snap_root = snapshot_store.default_root()
    _reset_run_ledger()
    if refresh:
        print("Mode: refresh -- conditional requests; recorded snapshots are "
              "replaced only when upstream content changes")
    else:
        print("Mode: replay -- recorded snapshots are used as-is; the network "
              "is touched only for missing entries")
    print(f"Snapshot store: {snap_root}")
    if skip_sources:
        print(f"Scope: skipping {', '.join(sorted(skip_sources))} -- not fetched, "
              "no rows from those sources this run")
    if only_source is not None:
        print(f"Scope: source-scoped run ({only_source}) -- snapshots are recorded "
              "and finalized, but catalog.json and release stats are NOT written "
              "(a single-source row set is not a catalog)")

    def runs(source_id):
        """Does this source block execute under the scope flags?"""
        if only_source is not None:
            return source_id == only_source
        return source_id not in skip_sources

    mcp_url, mcp_source = FEED_AWESOME_MCP
    skills_url, skills_source = FEED_AWESOME_SKILLS
    official_url, official_source = FEED_OFFICIAL_SERVERS

    items = []
    seen_ids = set()
    # Build-time skill verification ledger (A1/A3): one dict for every block
    # that resolves a skill row, printed once after the marketplaces run.
    verify_tally = new_verify_tally()

    # --- Official MCP Registry API (paginated /v0/servers) ---
    # Fetched through the snapshot layer like every other source: one
    # snapshot per page URL (the cursor rides in the query string, so each
    # page keys distinctly), replayed byte-for-byte when --refresh is not
    # given. Envelope confirmed against the live v0.1 preview API:
    #   {"servers": [{"server": <server.json>, "_meta": {...}}, ...],
    #    "metadata": {"nextCursor": "<name:version>", "count": N}}
    # elements are WRAPPED (not bare server.json), the cursor key is
    # camelCase, limit>100 answers 422, and pagination ends when nextCursor
    # is absent. A failed or malformed page stops pagination honestly (the
    # failure is logged and the rows cover exactly the pages fetched) -- a
    # partial registry is published as what it is, never silently completed.
    #
    # Dedup precedence: this block runs FIRST. The cross-source rule stays
    # the builder's existing first-wins-by-listing-id (seen_ids); registry
    # rows are namespaced `builtin:mcp-registry` and awesome-list rows by
    # GitHub owner, so collisions are rare -- but where one happens the
    # registry wins, because server.json is the publisher's own declaration
    # while a list row is a third-party claim about the same namespace.
    # Within the registry, repeated names collapse to the record flagged
    # `isLatest` (one row per server); non-active records and records with
    # no description are skipped and counted, never filled in.
    print("1/7 Official MCP Registry API...")
    if runs(REGISTRY_SOURCE):
        registry_latest = {}   # server name -> (element, page_url)
        registry_pages = 0
        registry_elements = 0
        registry_invalid = 0
        registry_incomplete = None
        seen_cursors = set()
        cursor = None
        while True:
            if cursor is None:
                page_url = f"{REGISTRY_API}?limit={REGISTRY_PAGE_LIMIT}"
            else:
                page_url = (f"{REGISTRY_API}?limit={REGISTRY_PAGE_LIMIT}&"
                            + urllib.parse.urlencode({"cursor": cursor}))
            page_bytes = _obtain_bulk(page_url, REGISTRY_SOURCE, 30, refresh,
                                      "MCP registry page", attempts=4, fatal=False)
            if page_bytes is None:
                # _obtain_bulk already logged the failure to failures.jsonl.
                registry_incomplete = "page fetch failed (see failures.jsonl)"
                _mark_incomplete(REGISTRY_SOURCE)
                break
            try:
                elements, nxt = registry_page_parts(page_bytes)
            except (ValueError, TypeError, KeyError) as exc:
                # Recorded but not consumed: this page's snapshot stays
                # `partial` (a body the parser refused is not an ingestion).
                snapshot_store.log_failure(snap_root, page_url, "registry-parse", str(exc))
                registry_incomplete = f"malformed page ({exc})"
                _mark_incomplete(REGISTRY_SOURCE)
                break
            _consume(page_url, REGISTRY_SOURCE)
            registry_pages += 1
            registry_elements += len(elements)
            for element in elements:
                srv = registry_server_of(element)
                if srv is None or not isinstance(srv.get("name"), str) or not srv["name"]:
                    registry_invalid += 1
                    _mark_incomplete(REGISTRY_SOURCE)
                    continue
                try:
                    latest_flag = registry_latest_flag(
                        registry_official_meta(element, srv).get("isLatest"))
                except TypeError:
                    registry_invalid += 1
                    _mark_incomplete(REGISTRY_SOURCE)
                    continue
                name = srv["name"]
                current = registry_latest.get(name)
                if current is None:
                    registry_latest[name] = (element, page_url)
                    continue
                cur_el = current[0]
                cur_is_latest = registry_latest_flag(
                    registry_official_meta(cur_el, registry_server_of(cur_el)).get("isLatest"))
                el_is_latest = latest_flag
                if el_is_latest and not cur_is_latest:
                    registry_latest[name] = (element, page_url)
                elif el_is_latest == cur_is_latest and \
                        registry_version_key(srv.get("version")) > \
                        registry_version_key(registry_server_of(cur_el).get("version")):
                    registry_latest[name] = (element, page_url)
            if nxt is None:
                break
            if nxt in seen_cursors:
                registry_incomplete = "upstream cursor did not advance"
                _mark_incomplete(REGISTRY_SOURCE)
                break
            if registry_pages >= REGISTRY_MAX_PAGES:
                registry_incomplete = f"page limit reached ({REGISTRY_MAX_PAGES})"
                _mark_incomplete(REGISTRY_SOURCE)
                break
            seen_cursors.add(nxt)
            cursor = nxt
            if registry_pages % 25 == 0:
                print(f"  registry: {registry_pages} pages, {registry_elements} records, "
                      f"{len(registry_latest)} servers so far")
            time.sleep(0.1)  # pacing against the registry API
        if registry_incomplete:
            print(f"  registry: PAGINATION INCOMPLETE after {registry_pages} page(s): "
                  f"{registry_incomplete}")

        registry_added = registry_inactive = registry_nodesc = registry_dup = 0
        registry_status_missing = 0
        registry_remote_extra_rows = registry_remote_refused_rows = 0
        registry_rows_by_page = {}
        for name in sorted(registry_latest):
            element, page_url = registry_latest[name]
            srv = registry_server_of(element)
            status = registry_official_meta(element, srv).get("status")
            if not isinstance(status, str) or not status.strip():
                registry_status_missing += 1
                _mark_incomplete(REGISTRY_SOURCE)
                continue
            if not registry_status_is_active(status):
                registry_inactive += 1
                continue
            desc = srv.get("description")
            if not isinstance(desc, str) or not desc.strip():
                registry_nodesc += 1
                continue
            title = srv.get("title") if isinstance(srv.get("title"), str) else ""
            display = title.strip() or name
            tail = name.rsplit("/", 1)[-1] if "/" in name else name
            slug = canonical_slug(tail.lower().replace(".", "-").replace("_", "-"))
            item_id = canonical_id("mcp", REGISTRY_SOURCE, name)
            if item_id in seen_ids:
                registry_dup += 1
                continue
            # Category: server.json's own `categories` when the record
            # publishes one (mapped through the same category table every
            # other source uses), else the same default an unsectioned
            # awesome-list entry gets -- not a claim about the domain.
            category = "Developer Tools"
            cats = srv.get("categories")
            if isinstance(cats, list):
                for cat in cats:
                    if isinstance(cat, str) and cat.strip():
                        category = clean_category(cat)
                        break
            pub_name, pub_url = registry_publisher(name, srv)
            seen_ids.add(item_id)
            row, remote_extras, remote_refused = registry_row(
                item_id, display, slug, category, desc.strip(),
                pub_name, pub_url, srv)
            if remote_extras:
                registry_remote_extra_rows += 1
            if remote_refused:
                registry_remote_refused_rows += 1
            items.append(row)
            registry_added += 1
            registry_rows_by_page[page_url] = registry_rows_by_page.get(page_url, 0) + 1
        for page_url, page_rows in registry_rows_by_page.items():
            attribute_rows(page_url, page_rows)
        print(registry_summary(
            registry_added, len(registry_latest), registry_pages,
            registry_inactive, registry_nodesc, registry_status_missing,
            registry_invalid, registry_dup,
            registry_remote_extra_rows, registry_remote_refused_rows))
    else:
        print("  skipped (scoped out of this run)")

    # 2. punkpeye/awesome-mcp-servers
    print("2/7 punkpeye/awesome-mcp-servers...")
    if runs(mcp_source):
        mcp_md = obtain(mcp_url, mcp_source, 25, refresh,
                        "awesome-mcp-servers").decode("utf-8", errors="ignore")
        # decode(errors="ignore") cannot fail: these bytes are handed straight
        # to the parse below, so this feed is consumed here.
        _consume(mcp_url, mcp_source)
    else:
        mcp_md = ""
        print("    skipped (scoped out of this run)")

    # 3. VoltAgent/awesome-agent-skills
    print("3/7 VoltAgent/awesome-agent-skills...")
    if runs(skills_source):
        skills_md = obtain(skills_url, skills_source, 25, refresh,
                           "awesome-agent-skills").decode("utf-8", errors="ignore")
        _consume(skills_url, skills_source)
    else:
        skills_md = ""
        print("    skipped (scoped out of this run)")

    # 4. modelcontextprotocol/servers -- tolerating an upstream failure on a
    #    FIRST fetch (published behaviour: this feed may be absent), but never
    #    tolerating a corrupt recording: a refused snapshot aborts the build.
    print("4/7 modelcontextprotocol/servers...")
    official_raw = None
    if runs(official_source):
        try:
            official_raw = obtain(official_url, official_source, 15, refresh,
                                  "official servers")
        except snapshot_store.SnapshotIntegrityError:
            raise
        except Exception:
            official_raw = None
            _mark_incomplete(official_source)
    else:
        print("    skipped (scoped out of this run)")
    official_mcp_md = official_raw.decode("utf-8", errors="ignore") if official_raw else ""
    if official_raw is not None:
        _consume(official_url, official_source)

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
                        "source": official_source,
                    })
    attribute_rows(official_url, len(items) - rows_before)

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
                    "source": mcp_source,
                })
    attribute_rows(mcp_url, len(items) - rows_before)

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


                row = {
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
                    "source": skills_source,
                }
                items.append(row)
                # A1: this row claims a skill source. Read the SKILL.md the
                # claim points at before any installability class is earned;
                # an officialskills.sh row first adopts the GitHub link its
                # own page publishes. Everything else stays discovery_only.
                resolve_skill_row(row, skills_source, refresh, verify_tally)
    attribute_rows(skills_url, len(items) - rows_before)

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
        if not runs(src["source_id"]):
            print(f"  {src['key']}: skipped (scoped out of this run)")
            continue
        try:
            manifest_bytes = obtain(src["url"], src["source_id"], 25, refresh, src["key"])
            manifest = json.loads(manifest_bytes.decode("utf-8", errors="ignore"))
        except snapshot_store.SnapshotIntegrityError:
            raise
        except Exception as e:
            # Recorded but refused by the parser (or never recorded): the
            # snapshot stays `partial`; nothing about it is claimed.
            print(f"  {src['key']}: fetch failed ({e}), skipped")
            _mark_incomplete(src["source_id"])
            snapshot_store.log_failure(snap_root, src["url"], "obtain", str(e))
            continue
        _consume(src["url"], src["source_id"])
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
                "source": src["source_id"],
            }
            if install_hint:
                row["installHint"] = install_hint
            items.append(row)
            added += 1

            # anthropics/skills bundles expose individually installable skills.
            if src["key"] == "askills":
                for sp in p.get("skills", []) or []:
                    # The manifest publishes the skill's real path inside the
                    # repository (entries look like `./skills/<name>`), so the
                    # claimed source is derived from the manifest instead of
                    # being guessed from the plugin name.
                    rel = str(sp).replace("\\", "/").strip()
                    while rel.startswith("./"):
                        rel = rel[2:]
                    rel = rel.strip("/")
                    skill = base_slug(rel.split("/")[-1]) if rel else ""
                    if not skill:
                        continue
                    skill_id = canonical_id("skill", "anthropics", skill)
                    # A3: the skill slug must differ from the plugin slug.
                    # Single-skill bundles (claude-api, academy-guide,
                    # discernment-nudge) used to share `askills-<name>` with
                    # the plugin row appended moments earlier and were
                    # dropped by first-wins slug dedup -- the manifest's 19
                    # skills produced 0 rows.
                    skill_slug = canonical_slug(f"{src['key']}-skill-{skill}")
                    if skill_id in seen_ids or skill_slug in seen_slugs:
                        continue
                    seen_ids.add(skill_id)
                    seen_slugs.add(skill_slug)
                    skill_tree = f"{src['repo']}/tree/main/{rel}"
                    row = {
                        "id": skill_id,
                        "name": skill.replace("-", " ").title(),
                        "slug": skill_slug,
                        "kind": "skill",
                        "summary": f"Anthropic example skill '{skill}'.",
                        "category": "Agent Skills",
                        "publisher": publisher_obj(
                            "anthropics", skill_tree, PROV_VENDOR),
                        "stars": None,
                        "version": None,
                        "installability": DISCOVERY_ONLY,
                        "skillSource": skill_tree,
                        "installHint": f"npx skills add {src['repo']} --skill {skill}",
                        "source": src["source_id"],
                    }
                    items.append(row)
                    # A manifest entry names a path, not a verified file:
                    # promote only if this run read SKILL.md at that path.
                    resolve_skill_row(row, src["source_id"], refresh,
                                      verify_tally)
                    added += 1
        print(f"  {src['key']}: +{added} rows")
        attribute_rows(src["url"], added)

    print_verify_tally(verify_tally, "skill verification")

    # --- skills.sh: directory of agent skills (sitemap + SKILL.md records) ---
    # The sitemap lists ~20k skill pages; for ids the rest of the catalog
    # does not already carry, the download API returns the skill's files and
    # the row is built from SKILL.md frontmatter. Nothing is invented when
    # the frontmatter has no description (skipped and counted below).
    # Snapshot lifecycle: the index and sub-sitemaps are navigation documents
    # (consumed, 0 rows attributed); every per-skill record is consumed after
    # its payload parses and gets the 0/1 rows it actually contributed, so a
    # completed run finalizes ALL of them -- not just the sitemap index.
    print("5/7 skills.sh...")
    skillsh_source = FEED_SKILLSH[1]
    skillsh_index = FEED_SKILLSH[0]
    skillsh_added = skillsh_seen = skillsh_nodesc = skillsh_failed = 0
    skillsh_enriched = skillsh_enrich_fail = 0
    if not runs(skillsh_source):
        print("  skills.sh: skipped (scoped out of this run)")
    else:
        try:
            index_bytes = _obtain_bulk(skillsh_index, skillsh_source, 25, refresh,
                                       "skills.sh sitemap index", fatal=True)
            index_text = index_bytes.decode("utf-8", errors="replace")
            submaps = []
            for loc in re.findall(r"<loc>([^<]+)</loc>", index_text):
                if "sitemap-skills-" not in loc:
                    continue
                if _is_source_sitemap_url(
                        loc, skillsh_source, r"/sitemap-skills-[A-Za-z0-9._-]+\.xml"):
                    submaps.append(loc)
                else:
                    _mark_incomplete(skillsh_source)
            submaps.sort()
            _consume(skillsh_index, skillsh_source)
            # A3: an id another feed already published used to be counted
            # `seen` and dropped, discarding the SKILL.md content this block
            # had just fetched. It now feeds the row's summary instead: the
            # skill's own frontmatter is the strongest description available
            # for it. The row's id, source and installability are NOT
            # rewritten -- skills.sh content proves a description, not a
            # clonable git path, so enrichment never promotes a row.
            rows_by_id = {item["id"]: item for item in items}
            skill_urls = []
            enrich_urls = []
            for sm in submaps:
                sm_bytes = _obtain_bulk(sm, skillsh_source, 30, refresh,
                                        "skills.sh skills sitemap", fatal=True)
                sm_text = sm_bytes.decode("utf-8", errors="replace")
                _consume(sm, skillsh_source)
                for loc in re.findall(r"<loc>([^<]+)</loc>", sm_text):
                    for prefix in ("https://www.skills.sh/", "https://skills.sh/"):
                        if loc.startswith(prefix):
                            break
                    else:
                        continue
                    parts = loc[len(prefix):].split("/")
                    if len(parts) != 3 or not all(parts):
                        continue
                    owner, repo, sslug = parts
                    item_id = canonical_id("skill", owner, canonical_slug(sslug.lower()))
                    if item_id in seen_ids:
                        skillsh_seen += 1
                        existing = rows_by_id.get(item_id)
                        if existing is not None:
                            enrich_urls.append((owner, repo, sslug, existing))
                        continue
                    skill_urls.append((loc, owner, repo, sslug, item_id))
            total = len(skill_urls)
            print(f"  skills.sh: {total} skills not yet in the catalog "
                  f"({skillsh_seen} already present, "
                  f"{len(enrich_urls)} to enrich from their SKILL.md)")
            for n, (loc, owner, repo, sslug, item_id) in enumerate(skill_urls, 1):
                payload_url = f"https://skills.sh/api/download/{owner}/{repo}/{sslug}"
                payload = _obtain_bulk(payload_url, skillsh_source, 25, refresh,
                                       "skills.sh skill record")
                if payload is None:
                    skillsh_failed += 1
                else:
                    # The payload parses here (skill_md_description decides
                    # row or no-row), so this record is consumed either way.
                    _consume(payload_url, skillsh_source)
                    desc = skill_md_description(payload)
                    if not desc:
                        skillsh_nodesc += 1
                    else:
                        seen_ids.add(item_id)
                        items.append({
                            "id": item_id,
                            "name": sslug.replace("-", " ").title(),
                            "slug": canonical_slug(sslug.lower()),
                            "kind": "skill",
                            "summary": desc,
                            "category": "Agent Skills",
                            "publisher": publisher_obj(owner, loc, PROV_LIST),
                            "stars": None,
                            "version": None,
                            "skillSource": loc,
                            "installability": DISCOVERY_ONLY,
                            "source": skillsh_source,
                        })
                        skillsh_added += 1
                        attribute_rows(payload_url, 1)
                if n % 500 == 0:
                    print(f"  skills.sh: {n}/{total} processed ({skillsh_added} added)")
                time.sleep(0.03)  # pacing: one polite request at a time
            # Enrichment pass over ids the rest of the catalog already owns.
            # The record created no row, so it is consumed with 0 rows
            # attributed: the row belongs to whichever feed made it.
            for n, (owner, repo, sslug, existing) in enumerate(enrich_urls, 1):
                payload_url = f"https://skills.sh/api/download/{owner}/{repo}/{sslug}"
                payload = _obtain_bulk(payload_url, skillsh_source, 25, refresh,
                                       "skills.sh skill record")
                if payload is None:
                    skillsh_enrich_fail += 1
                    continue
                _consume(payload_url, skillsh_source)
                attribute_rows(payload_url, 0)
                desc = skill_md_description(payload)
                if desc and desc != existing.get("summary"):
                    existing["summary"] = desc
                    skillsh_enriched += 1
                if n % 500 == 0:
                    print(f"  skills.sh: {n}/{len(enrich_urls)} enriched")
                time.sleep(0.03)
            print(f"  skills.sh: +{skillsh_added} rows "
                  f"({skillsh_seen} already present, {skillsh_enriched} summaries "
                  f"refreshed from SKILL.md, {skillsh_nodesc} without a "
                  f"SKILL.md description, {skillsh_failed + skillsh_enrich_fail} "
                  f"fetch failures)")
        except snapshot_store.SnapshotIntegrityError:
            raise
        except Exception as e:
            # First-fetch tolerance mirrors the official-servers feed: an absent
            # upstream must not abort the build and nothing is fabricated in its
            # place. A corrupt recording remains fatal (SnapshotIntegrityError).
            print(f"  skills.sh: ingestion failed ({e}); source skipped")
            _mark_incomplete(skillsh_source)
            snapshot_store.log_failure(snapshot_store.default_root(),
                                       skillsh_index, "ingest", str(e))

    # --- mcpservers.org: directory pages, content via the Wayback Machine ---
    # The sitemap supplies the URL list (locale-prefixed variants are
    # filtered out); page content comes from public Wayback captures, never
    # from mcpservers.org itself (its API paths are robots-disallowed).
    # Pages with no capture, and captures lacking title+description, are
    # skipped and counted -- never filled in. Snapshot lifecycle: the
    # sitemap/CDX documents are navigation (consumed, 0 rows); each page
    # replay is consumed with the 0/1 rows its parse produced.
    print("6/7 mcpservers.org...")
    mcps_source = FEED_MCPSERVERS[1]
    mcps_index = FEED_MCPSERVERS[0]
    mcps_added = mcps_seen = mcps_nocapture = mcps_nodesc = mcps_failed = 0
    if not runs(mcps_source):
        print("  mcpservers.org: skipped (scoped out of this run)")
    else:
        try:
            mcps_index_bytes = _obtain_bulk(mcps_index, mcps_source, 25, refresh,
                                            "mcpservers sitemap index", fatal=True)
            mcps_index_text = mcps_index_bytes.decode("utf-8", errors="replace")
            server_sitemaps = set()
            for loc in re.findall(r"<loc>([^<]+)</loc>", mcps_index_text):
                if "/sitemaps/servers/" not in loc:
                    continue
                if _is_source_sitemap_url(
                        loc, mcps_source, r"/sitemaps/servers/[A-Za-z0-9._-]+\.xml"):
                    server_sitemaps.add(loc)
                else:
                    _mark_incomplete(mcps_source)
            server_sitemaps = sorted(server_sitemaps)
            _consume(mcps_index, mcps_source)
            new_pages = []
            for sm in server_sitemaps:
                sm_bytes = _obtain_bulk(sm, mcps_source, 40, refresh,
                                        "mcpservers servers sitemap", fatal=True)
                sm_text = sm_bytes.decode("utf-8", errors="replace")
                _consume(sm, mcps_source)
                for loc in re.findall(r"<loc>([^<]+)</loc>", sm_text):
                    if not loc.startswith("https://mcpservers.org/"):
                        continue
                    path = loc[len("https://mcpservers.org"):]  # /servers/{owner}/{slug}
                    seg = [t for t in path.split("/") if t]
                    if len(seg) != 2 or seg[0] != "servers":
                        continue  # locale-prefixed paths open with a locale code
                    owner, sslug = seg
                    item_id = canonical_id("mcp", owner, canonical_slug(sslug.lower()))
                    if item_id in seen_ids:
                        mcps_seen += 1
                        continue
                    new_pages.append((loc, path, owner, sslug, item_id))
            print(f"  mcpservers.org: {len(new_pages)} servers not yet in the catalog "
                  f"({mcps_seen} already present); loading capture index...")

            # Capture index: paginate the CDX query (collapse is applied in
            # Python below -- collapsing inside the query shrinks each page and
            # loses coverage), keeping the latest 200-status capture per path.
            cdx_root = ("https://web.archive.org/cdx/search/cdx"
                        "?url=mcpservers.org/servers/*")
            cdx_base = cdx_root + "&output=json&fl=original,timestamp,statuscode"
            # showNumPages answers a PLAIN-TEXT page count and is IGNORED when
            # output=json rides along: the API then returns an empty JSON table
            # whose null cell made int() throw, which skipped the whole source
            # on its first real fetch (2026-10-07). The count is therefore
            # requested on its own, without output=json.
            cdx_count_url = cdx_root + "&showNumPages=true"
            pages_doc = _obtain_bulk(cdx_count_url, mcps_source,
                                     40, refresh, "CDX page count", fatal=True)
            n_pages = int(pages_doc.decode("utf-8", errors="replace").strip())
            if n_pages < 0 or n_pages > REGISTRY_MAX_PAGES:
                raise ValueError(f"CDX page count {n_pages} exceeds the safe bound")
            _consume(cdx_count_url, mcps_source)
            captures = {}
            for page_no in range(n_pages):
                cdx_page_url = f"{cdx_base}&page={page_no}"
                rows_doc = _obtain_bulk(cdx_page_url, mcps_source,
                                        60, refresh, f"CDX page {page_no}", fatal=True)
                rows = json.loads(rows_doc)
                _consume(cdx_page_url, mcps_source)
                for original, ts, status in rows[1:]:
                    if status != "200" or "?" in original or "#" in original:
                        continue
                    if not original.startswith("https://mcpservers.org/servers/"):
                        continue
                    path = original[len("https://mcpservers.org"):]
                    prev = captures.get(path)
                    if prev is None or ts > prev:
                        captures[path] = ts
                print(f"  mcpservers.org: capture index page {page_no + 1}/{n_pages} "
                      f"({len(captures)} unique captured pages)")
                time.sleep(1.0)  # pacing against archive.org
            for n, (loc, path, owner, sslug, item_id) in enumerate(new_pages, 1):
                ts = captures.get(path)
                if ts is None:
                    mcps_nocapture += 1
                    continue
                replay_url = f"https://web.archive.org/web/{ts}id_/{loc}"
                page = _obtain_bulk(replay_url, mcps_source, 30, refresh,
                                    "mcpservers page replay")
                if page is None:
                    mcps_failed += 1
                    continue
                # The replayed page parses below either way: consumed here.
                _consume(replay_url, mcps_source)
                name, summary = mcpservers_page_fields(page)
                if not name or not summary:
                    mcps_nodesc += 1
                    continue
                seen_ids.add(item_id)
                items.append({
                    "id": item_id,
                    "name": name,
                    "slug": canonical_slug(sslug.lower()),
                    "kind": "mcp",
                    "summary": summary,
                    # The directory page carries no category signal; this is the
                    # same default the awesome-list parser uses for an unsectioned
                    # entry -- not a claim about the server's domain.
                    "category": "Developer Tools",
                    "publisher": publisher_obj(owner, loc, PROV_LIST),
                    "transport": None,
                    "runtime": None,
                    "stars": None,
                    "version": None,
                    "command": None,
                    "args": None,
                    "installability": DISCOVERY_ONLY,
                    "source": mcps_source,
                })
                mcps_added += 1
                attribute_rows(replay_url, 1)
                if n % 500 == 0:
                    print(f"  mcpservers.org: {n}/{len(new_pages)} processed "
                          f"({mcps_added} added)")
                time.sleep(0.1)  # pacing against archive.org
            print(f"  mcpservers.org: +{mcps_added} rows "
                  f"({mcps_seen} already present, {mcps_nocapture} not archived, "
                  f"{mcps_nodesc} without title+description, {mcps_failed} fetch failures)")
        except snapshot_store.SnapshotIntegrityError:
            raise
        except Exception as e:
            print(f"  mcpservers.org: ingestion failed ({e}); source skipped")
            _mark_incomplete(mcps_source)
            snapshot_store.log_failure(snapshot_store.default_root(),
                                       mcps_index, "ingest", str(e))

    # --- mcpmarket.com: directory pages read directly from the origin ---
    # robots.txt (probed 2026-10-08) allows `User-Agent: *` everywhere except
    # /api/ and sets `Crawl-delay: 1`, so the listing pages themselves are
    # fetched straight from mcpmarket.com at that delay -- the API is never
    # touched, and every URL (index, sub-sitemaps, pages, any redirect) still
    # passes the source host allowlist, the public-IP check and the 16 MiB
    # response cap inside the shared fetch machinery above. Ledger discipline
    # matches the two directory blocks above: navigation documents (index,
    # sub-sitemaps) are consumed with 0 rows, each page is consumed with the
    # 0/1 rows its own content produced, a page whose name+summary do not
    # both parse is skipped and counted (never completed from the other
    # half), and a layout change or an uncrawled page marks the source
    # incomplete instead of passing for coverage. The page crawl is budgeted
    # (Crawl-delay: 1 over ~417k pages would occupy a run for days); recorded
    # pages replay offline without consuming budget, so repeated runs grow
    # coverage from the snapshot store.
    print("7/7 mcpmarket.com...")
    mcpm_source = FEED_MCPMARKET[1]
    mcpm_index = FEED_MCPMARKET[0]
    mcpm_added = mcpm_seen = mcpm_nodesc = mcpm_failed = 0
    mcpm_ignored = mcpm_deferred = 0
    if not runs(mcpm_source):
        print("  mcpmarket.com: skipped (scoped out of this run)")
    else:
        try:
            if _mcpmarket_needs_fetch(mcpm_index, refresh):
                time.sleep(MCPMARKET_CRAWL_DELAY)  # robots Crawl-delay: 1
            mcpm_index_bytes = _obtain_bulk(mcpm_index, mcpm_source, 25, refresh,
                                            "mcpmarket sitemap index", fatal=True)
            mcpm_index_text = mcpm_index_bytes.decode("utf-8", errors="replace")
            mcpm_submaps = []
            for loc in re.findall(r"<loc>([^<]+)</loc>", mcpm_index_text):
                if "/sitemap/" not in loc:
                    continue
                if _is_source_sitemap_url(loc, mcpm_source, MCPMARKET_SUBMAP_PATH):
                    if loc not in mcpm_submaps:
                        mcpm_submaps.append(loc)
                elif _is_source_sitemap_url(loc, mcpm_source, MCPMARKET_STATIC_PATH):
                    continue  # known site-wide sitemap; not a listing page set
                else:
                    # A sub-sitemap URL that fails the strict shape (another
                    # host, a query, an unexpected path) is a coverage gap --
                    # reported, never silently followed.
                    _mark_incomplete(mcpm_source)
            _consume(mcpm_index, mcpm_source)
            print(f"  mcpmarket.com: {len(mcpm_submaps)} sub-sitemaps "
                  f"(tools + skills listing pages)")
            budget = mcpmarket_max_page_fetches()
            for sm_no, sm in enumerate(mcpm_submaps, 1):
                if _mcpmarket_needs_fetch(sm, refresh):
                    time.sleep(MCPMARKET_CRAWL_DELAY)
                sm_bytes = _obtain_bulk(sm, mcpm_source, 60, refresh,
                                        "mcpmarket listing sitemap", fatal=True)
                sm_text = sm_bytes.decode("utf-8", errors="replace")
                _consume(sm, mcpm_source)
                sm_locs = sm_parsed = sm_added = 0
                for loc in re.findall(r"<loc>([^<]+)</loc>", sm_text):
                    sm_locs += 1
                    kind, _path, page_slug = mcpmarket_listing_parts(loc, mcpm_source)
                    if kind is None:
                        mcpm_ignored += 1
                        continue
                    sm_parsed += 1
                    item_id = canonical_id(kind, mcpm_source,
                                           canonical_slug(page_slug.lower()))
                    if item_id in seen_ids:
                        mcpm_seen += 1
                        continue
                    needs_fetch = _mcpmarket_needs_fetch(loc, refresh)
                    if needs_fetch:
                        if budget <= 0:
                            mcpm_deferred += 1
                            continue
                        budget -= 1
                        time.sleep(MCPMARKET_CRAWL_DELAY)
                    page = _obtain_bulk(loc, mcpm_source, 30, refresh,
                                        "mcpmarket page")
                    if page is None:
                        mcpm_failed += 1
                        continue
                    # The page parses below either way: consumed here.
                    _consume(loc, mcpm_source)
                    name, summary, pub_name, pub_url = mcpmarket_page_fields(page)
                    if not name or not summary:
                        mcpm_nodesc += 1
                        continue
                    seen_ids.add(item_id)
                    items.append(mcpmarket_row(kind, page_slug, item_id, loc,
                                               name, summary, pub_name, pub_url))
                    mcpm_added += 1
                    sm_added += 1
                    attribute_rows(loc, 1)
                if sm_locs and not sm_parsed:
                    # The sub-sitemap published URLs but none in either
                    # listing shape: the upstream layout changed under us.
                    _mark_incomplete(mcpm_source)
                    print(f"  mcpmarket.com: sitemap {sm_no}/{len(mcpm_submaps)} "
                          f"yielded no listing URLs ({sm_locs} entries); layout "
                          "may have changed -- marked incomplete")
                else:
                    print(f"  mcpmarket.com: sitemap {sm_no}/{len(mcpm_submaps)} "
                          f"(+{sm_added} rows, {mcpm_deferred} deferred so far)")
            if mcpm_deferred:
                _mark_incomplete(mcpm_source)
            print(f"  mcpmarket.com: +{mcpm_added} rows "
                  f"({mcpm_seen} already present, {mcpm_nodesc} without name+summary, "
                  f"{mcpm_failed} fetch failures, {mcpm_ignored} non-listing URLs, "
                  f"{mcpm_deferred} deferred by the fetch budget)")
            if mcpm_deferred:
                print("  mcpmarket.com: crawl budget reached -- run again to continue "
                      "from recorded snapshots, or raise MCPMARKET_MAX_PAGE_FETCHES "
                      "(0 = uncapped) for a deliberate full crawl")
        except snapshot_store.SnapshotIntegrityError:
            raise
        except Exception as e:
            print(f"  mcpmarket.com: ingestion failed ({e}); source skipped")
            _mark_incomplete(mcpm_source)
            snapshot_store.log_failure(snapshot_store.default_root(),
                                       mcpm_index, "ingest", str(e))

    # Finalize every snapshot this run CONSUMED (parsed): status healthy +
    # itemCount = the rows that snapshot's bytes actually contributed
    # (navigation documents get 0). Only recordings that exist are updated,
    # an already-healthy record with the same count is byte-stable, and a run
    # that never reaches this point leaves its records `partial` -- never a
    # false `healthy`.
    promoted, promoted_rows, unchanged, missing = finalize_consumed_snapshots(snap_root)
    print(f"Snapshot finalization: {promoted} promoted to healthy "
          f"({promoted_rows} rows attributed), {unchanged} already healthy"
          + (f", {missing} consumed without a recording (BUG)" if missing else ""))
    _print_ingestion_status(refresh, skip_sources, only_source)

    if only_source is not None:
        # A source-scoped pass exists to fetch/finalize one source's
        # snapshots (first fetch, scoped refresh). Its row set holds only
        # that source's rows, so it must not overwrite the published
        # dataset -- that would silently drop every other source.
        print(f"Scoped run complete: {len(items)} row(s) from {only_source} in "
              "this pass; catalog.json and release stats NOT written.")
        return

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
        "metadata_verified appears only on vendor-manifest rows and on skill rows whose "
        "SKILL.md the build itself read at the claimed path.",
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
    parser.add_argument(
        "--skip-source",
        default=None,
        metavar="SOURCE_ID[,SOURCE_ID...]",
        help="Comma-separated domain.SourceID(s) whose blocks do not run: not fetched, no rows "
             "from them, and their snapshots are left untouched. For rebuilding the dataset while "
             "a source's crawl is still in flight (the run's row set then honestly lacks them).",
    )
    parser.add_argument(
        "--only-source",
        default=None,
        metavar="SOURCE_ID",
        help="Run exactly one source block (fetch/ingest/finalize its snapshots) and write NO "
             "dataset: a single-source row set is not a catalog. Use for a first fetch or a "
             "scoped refresh of one feed.",
    )
    args = parser.parse_args()
    if args.skip_source and args.only_source:
        parser.error("--skip-source and --only-source are mutually exclusive")
    skip_sources = frozenset(
        s.strip() for s in args.skip_source.split(",") if s.strip()
    ) if args.skip_source else frozenset()
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
        build_full_catalog(refresh=args.refresh, skip_sources=skip_sources,
                           only_source=args.only_source)
