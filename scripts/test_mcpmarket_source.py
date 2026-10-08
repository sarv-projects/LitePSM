"""Offline regression checks for the mcpmarket.com catalog source.

Nothing here touches the network: the full source block is exercised with
canned index/sitemap/page bytes behind a patched `_http_get`, and every
policy check below is a pure function call or a mocked-DNS call. The style
mirrors scripts/test_catalog_ingestion_safety.py.
"""

import contextlib
import io
import os
import re
import sys
import tempfile
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import build_full_catalog as producer  # noqa: E402
import snapshot_store  # noqa: E402

SOURCE = "feed:mcpmarket-com"
INDEX_URL = "https://mcpmarket.com/sitemap.xml"
TOOLS_SMAP = "https://mcpmarket.com/sitemap/tools-0.xml"
SKILLS_SMAP = "https://mcpmarket.com/sitemap/skills-0.xml"
SERVER_PAGE = "https://mcpmarket.com/server/neon-1"

# domain.RegexSourceID / domain.RegexListingID (internal/domain/identifiers.go)
SOURCE_ID_RE = re.compile(r"^[a-z0-9_-]{3,32}:[a-z0-9_-]{3,64}$")
LISTING_ID_RE = re.compile(
    r"^(plugin|mcp|skill|connector|agent|rule|hook|tool|lsp):"
    r"[a-z0-9_:-]+:[A-Za-z0-9_.~%+-]+$")

INDEX_XML = """<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>https://mcpmarket.com/sitemap/static.xml</loc></sitemap>
  <sitemap><loc>https://mcpmarket.com/sitemap/tools-0.xml</loc></sitemap>
  <sitemap><loc>https://mcpmarket.com/sitemap/skills-0.xml</loc></sitemap>
</sitemapindex>
"""

EVIL_INDEX_XML = """<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>https://mcpmarket.com.evil.example/sitemap/tools-0.xml</loc></sitemap>
  <sitemap><loc>https://mcpmarket.com/sitemap/tools-0.xml?a=b</loc></sitemap>
</sitemapindex>
"""

TOOLS_SMAP_XML = """<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>https://mcpmarket.com/server/neon-1</loc></url>
<url><loc>https://mcpmarket.com/server/obsidian-connector-1</loc></url>
<url><loc>https://mcpmarket.com/server/needs-summary-1</loc></url>
</urlset>
"""

SKILLS_SMAP_XML = """<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>https://mcpmarket.com/tools/skills/orchestrator</loc></url>
</urlset>
"""

# JSON-LD page: name, description and author all declared by the page itself.
PAGE_JSONLD = (
    b"<html><head>"
    b"<title>Neon: Natural Language Database Management for Neon</title>"
    b'<meta name="description" content="Manage your Neon database."/>'
    b'<script type="application/ld+json">{"@context":"https://schema.org",'
    b'"@type":"SoftwareApplication","name":"Neon",'
    b'"description":"Enables natural language interaction with Neon.",'
    b'"author":{"@type":"Organization","name":"neondatabase-labs",'
    b'"url":"https://github.com/neondatabase-labs"}}</script>'
    b"</head><body></body></html>"
)

# No JSON-LD: <title> + meta description must carry the row, with the meta
# attributes in the reverse order and the site separator in the title.
PAGE_TITLE_META = (
    b"<html><head>"
    b"<title>Obsidian Connector | MCP Market</title>"
    b'<meta content="Sync your vault notes with Obsidian." name="description"/>'
    b'<script type="application/ld+json">not json at all</script>'
    b"</head><body></body></html>"
)

# Only a title: half a row is never published.
PAGE_TITLE_ONLY = (
    b"<html><head><title>Needs Summary | MCP Market</title>"
    b"</head><body></body></html>"
)

# Skill page whose author is a plain string.
PAGE_SKILL = (
    b"<html><head>"
    b"<title>Orchestrator: Manage AI Agent Sessions | Claude Code Skill</title>"
    b'<meta name="description" content="Manage specialized agent sessions."/>'
    b'<script type="application/ld+json">{"@type":"SoftwareApplication",'
    b'"name":"Orchestrator",'
    b'"description":"Orchestrates specialized agent sessions.",'
    b'"author":"rawe"}</script>'
    b"</head><body></body></html>"
)

PAGES = {
    SERVER_PAGE: PAGE_JSONLD,
    "https://mcpmarket.com/server/obsidian-connector-1": PAGE_TITLE_META,
    "https://mcpmarket.com/server/needs-summary-1": PAGE_TITLE_ONLY,
    "https://mcpmarket.com/tools/skills/orchestrator": PAGE_SKILL,
}

CANNED = {
    INDEX_URL: INDEX_XML.encode(),
    TOOLS_SMAP: TOOLS_SMAP_XML.encode(),
    SKILLS_SMAP: SKILLS_SMAP_XML.encode(),
    **PAGES,
}


def _public_dns(host, port, type=None):
    return [(None, None, None, None, ("93.184.216.34", port))]


def run_producer(root, index=CANNED[INDEX_URL], canned=None, env=None,
                 refresh=False):
    """Run the real builder source-scoped to mcpmarket.com, fully offline.

    `_http_get` serves only the canned URLs (anything else is recorded as an
    unexpected request), DNS answers are pinned to a public test IP, sleeps
    are captured so the robots crawl delay can be asserted, and the snapshot
    store is rooted in `root`. `only_source` keeps the run from writing any
    dataset.
    """
    bodies = dict(canned or CANNED)
    bodies[INDEX_URL] = index.encode() if isinstance(index, str) else index
    requested = []
    unexpected = []

    def fake_http_get(url, timeout, etag=None, source_id=None):
        requested.append(url)
        if url not in bodies:
            unexpected.append(url)
            raise ValueError(f"unexpected request for {url}")
        return 200, {}, bodies[url]

    sleeps = []
    out = io.StringIO()
    out_dir = os.path.join(root, "out")
    old_env = os.environ.pop("MCPMARKET_MAX_PAGE_FETCHES", None)
    os.environ["CATALOG_OUT_DIR"] = out_dir
    try:
        with mock.patch.dict(os.environ, env or {}), \
                mock.patch.object(producer, "_http_get", side_effect=fake_http_get), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root), \
                contextlib.redirect_stdout(out):
            producer.build_full_catalog(refresh=refresh,
                                        skip_sources=frozenset(),
                                        only_source=SOURCE)
    finally:
        os.environ.pop("CATALOG_OUT_DIR", None)
        if old_env is not None:
            os.environ["MCPMARKET_MAX_PAGE_FETCHES"] = old_env
    return out.getvalue(), requested, unexpected, sleeps, out_dir


def snapshot_meta(root, url):
    _body, meta = snapshot_store.load_snapshot(root, url)
    return meta["snapshot"]


def main():
    checks = 0

    def ok(cond, message):
        nonlocal checks
        if not cond:
            raise AssertionError(message)
        checks += 1

    # --- id grammar -----------------------------------------------------
    ok(SOURCE_ID_RE.fullmatch(SOURCE) is not None,
       "feed:mcpmarket-com violates the domain SourceID grammar")
    lid = producer.canonical_id("mcp", SOURCE,
                                producer.canonical_slug("neon-1"))
    ok(lid == "mcp:feed:mcpmarket-com:neon-1",
       f"unexpected canonical listing id: {lid}")
    ok(LISTING_ID_RE.fullmatch(lid) is not None,
       "mcpmarket listing id violates RegexListingID")
    ok(LISTING_ID_RE.fullmatch(
        producer.canonical_id("skill", SOURCE, "Orchestrator's v2?tab=readme"))
       is not None, "skill listing id with upstream junk left the grammar")
    ok(producer.canonical_id("mcp", SOURCE, "Neon's Server")
       == "mcp:feed:mcpmarket-com:Neons-Server",
       "canonical_id did not normalize an upstream name")

    # --- destination policy --------------------------------------------
    with mock.patch.object(producer.socket, "getaddrinfo",
                           side_effect=_public_dns):
        producer._validate_request_url(INDEX_URL, SOURCE)
        producer._validate_request_url(
            "https://www.mcpmarket.com/sitemap/tools-0.xml", SOURCE)
        producer._validate_request_url(SERVER_PAGE, SOURCE)
    for url in (
        "https://mcpmarket.com.evil.example/sitemap.xml",
        "https://evil-mcpmarket.com/sitemap.xml",
        "https://user:pass@mcpmarket.com/sitemap.xml",
        "http://mcpmarket.com/sitemap.xml",
        "https://mcpmarket.com:8443/sitemap.xml",
        "https://mcpmarket.com/sitemap.xml#frag",
        "https://skills.sh/sitemap.xml",               # other source's host
    ):
        try:
            producer._validate_request_url(url, SOURCE, check_dns=False)
        except ValueError:
            ok(True, f"unsafe request URL rejected: {url}")
        else:
            ok(False, f"unsafe request URL accepted: {url}")
    with mock.patch.object(producer.socket, "getaddrinfo", return_value=[
            (None, None, None, None, ("169.254.169.254", 443))]):
        try:
            producer._validate_request_url(INDEX_URL, SOURCE)
        except ValueError:
            ok(True, "allowlisted hostname resolving to link-local IP rejected")
        else:
            ok(False, "allowlisted hostname resolving to link-local IP accepted")

    # Redirects pass through the same policy (mirrors the ingestion-safety
    # check, for this source's allowlist).
    handler = producer._SourceRedirectHandler(SOURCE)
    request = producer.urllib.request.Request(INDEX_URL)
    with mock.patch.object(producer.socket, "getaddrinfo",
                           side_effect=_public_dns):
        ok(handler.redirect_request(
            request, None, 302, "Found", {},
            "https://www.mcpmarket.com/sitemap/tools-0.xml") is not None,
           "approved same-source redirect was not followed")
    try:
        handler.redirect_request(
            request, None, 302, "Found", {}, "https://169.254.169.254/x")
    except ValueError:
        ok(True, "private redirect destination rejected")
    else:
        ok(False, "private redirect destination accepted")

    # --- strict path shapes --------------------------------------------
    ok(producer._is_source_sitemap_url(
        TOOLS_SMAP, SOURCE, producer.MCPMARKET_SUBMAP_PATH),
       "tools sub-sitemap rejected")
    ok(producer._is_source_sitemap_url(
        SKILLS_SMAP, SOURCE, producer.MCPMARKET_SUBMAP_PATH),
       "skills sub-sitemap rejected")
    ok(producer._is_source_sitemap_url(
        "https://mcpmarket.com/sitemap/static.xml", SOURCE,
        producer.MCPMARKET_STATIC_PATH),
       "known static sitemap rejected")
    for loc, pattern in (
        ("https://mcpmarket.com/sitemap/static.xml",
         producer.MCPMARKET_SUBMAP_PATH),          # not a listing sitemap
        ("https://mcpmarket.com/sitemap/tools-x.xml",
         producer.MCPMARKET_SUBMAP_PATH),          # not a numbered sitemap
        ("https://mcpmarket.com/sitemap/nested/tools-0.xml",
         producer.MCPMARKET_SUBMAP_PATH),          # nested path
        ("https://mcpmarket.com/sitemap/tools-0.xml?next=bad",
         producer.MCPMARKET_SUBMAP_PATH),          # query string
        ("https://mcpmarket.com.evil.example/sitemap/tools-0.xml",
         producer.MCPMARKET_SUBMAP_PATH),          # other host
        ("https://mcpmarket.com/server/neon-1",
         producer.MCPMARKET_SUBMAP_PATH),          # page under sitemap pattern
        ("https://mcpmarket.com/server/neon-1?x=1",
         producer.MCPMARKET_SERVER_PATH),          # page with query
    ):
        ok(not producer._is_source_sitemap_url(loc, SOURCE, pattern),
           f"invalid URL accepted: {loc}")

    ok(producer.mcpmarket_listing_parts(SERVER_PAGE)
       == ("mcp", "/server/neon-1", "neon-1"),
       "server listing URL not recognized")
    ok(producer.mcpmarket_listing_parts(
        "https://mcpmarket.com/tools/skills/orchestrator")
       == ("skill", "/tools/skills/orchestrator", "orchestrator"),
       "skill listing URL not recognized")
    for url in (
        "https://mcpmarket.com/server/",             # no slug
        "https://mcpmarket.com/server/a/b",          # nested path
        "https://mcpmarket.com/zh/server/neon-1",    # locale-prefixed
        "https://mcpmarket.com/tools/skills/categories/design-tools",
        "https://mcpmarket.com/api/servers",         # robots-disallowed path
        "https://mcpmarket.com.evil.example/server/neon-1",
        "http://mcpmarket.com/server/neon-1",
        "https://mcpmarket.com/server/neon-1?a=b",
    ):
        ok(producer.mcpmarket_listing_parts(url) == (None, "", ""),
           f"listing URL wrongly accepted: {url}")

    # --- page parsing ---------------------------------------------------
    name, summary, pub_name, pub_url = producer.mcpmarket_page_fields(
        PAGE_JSONLD)
    ok(name == "Neon", f"JSON-LD name not used: {name!r}")
    ok(summary == "Enables natural language interaction with Neon.",
       f"JSON-LD description not used: {summary!r}")
    ok(pub_name == "neondatabase-labs" and
       pub_url == "https://github.com/neondatabase-labs",
       f"JSON-LD author not used: {pub_name!r} {pub_url!r}")

    name, summary, pub_name, pub_url = producer.mcpmarket_page_fields(
        PAGE_TITLE_META)
    ok(name == "Obsidian Connector",
       f"title fallback kept the site separator: {name!r}")
    ok(summary == "Sync your vault notes with Obsidian.",
       f"meta description fallback failed: {summary!r}")
    ok(pub_name == "" and pub_url == "",
       "a page with no author must not invent one")

    name, summary, pub_name, pub_url = producer.mcpmarket_page_fields(
        PAGE_SKILL)
    ok(name == "Orchestrator" and pub_name == "rawe" and pub_url == "",
       "string author was not read as the publisher name")

    fields = producer.mcpmarket_page_fields(PAGE_TITLE_ONLY)
    ok(fields[0] is None and fields[1] is None,
       "a page without a summary published half a row")

    # --- row shape ------------------------------------------------------
    row = producer.mcpmarket_row(
        "mcp", "neon-1", lid, SERVER_PAGE, "Neon", "Some summary",
        "neondatabase-labs", "https://github.com/neondatabase-labs")
    ok(row["installability"] == producer.DISCOVERY_ONLY,
       "directory row is not discovery_only")
    for key in ("command", "args", "version", "transport", "runtime", "stars"):
        ok(row.get(key) is None,
           f"directory row fabricated {key}: {row.get(key)!r}")
    ok(row["source"] == SOURCE, "row source id is wrong")
    ok(row["slug"] == "neon-1", "row slug is wrong")
    ok(row["category"] == "Developer Tools", "server category is wrong")
    ok(row["publisher"]["verified"] is False and
       row["publisher"]["provenance"] == "awesome-list-claim",
       "directory row must stay an unverified list claim")
    ok(row["publisher"]["name"] == "neondatabase-labs",
       "declared author was not used as publisher")
    ok(LISTING_ID_RE.fullmatch(row["id"]) is not None,
       "row id violates the listing grammar")

    anon = producer.mcpmarket_row(
        "mcp", "x-1", "mcp:feed:mcpmarket-com:x-1",
        "https://mcpmarket.com/server/x-1", "X", "Summary", "", "")
    ok(anon["publisher"]["name"] == "mcpmarket.com" and
       anon["publisher"]["url"] == "https://mcpmarket.com/server/x-1",
       "an author-less row must attribute to the directory, not invent a publisher")

    skill_row = producer.mcpmarket_row(
        "skill", "orchestrator",
        "skill:feed:mcpmarket-com:orchestrator",
        "https://mcpmarket.com/tools/skills/orchestrator",
        "Orchestrator", "Summary", "rawe", "")
    ok(skill_row["category"] == "Agent Skills", "skill category is wrong")
    ok(skill_row.get("skillSource") ==
       "https://mcpmarket.com/tools/skills/orchestrator",
       "skill row lost its listing-page source")
    ok("command" not in skill_row and skill_row.get("version") is None,
       "skill row carries a fabricated launch line or version")

    # --- crawl budget ---------------------------------------------------
    os.environ.pop("MCPMARKET_MAX_PAGE_FETCHES", None)
    ok(producer.mcpmarket_max_page_fetches() ==
       producer.MCPMARKET_DEFAULT_MAX_PAGE_FETCHES,
       "default fetch budget not applied")
    for raw, want in (("0", 0), ("7", 7), (" 42 ", 42)):
        with mock.patch.dict(os.environ, {"MCPMARKET_MAX_PAGE_FETCHES": raw}):
            ok(producer.mcpmarket_max_page_fetches() == want,
               f"fetch budget {raw!r} did not parse to {want}")
    for raw in ("lots", "-1", "1.5"):
        with mock.patch.dict(os.environ, {"MCPMARKET_MAX_PAGE_FETCHES": raw}):
            try:
                producer.mcpmarket_max_page_fetches()
            except ValueError:
                ok(True, f"bad fetch budget rejected: {raw!r}")
            else:
                ok(False, f"bad fetch budget accepted: {raw!r}")

    # --- needs-fetch accounting (recorded pages cost no budget) ---------
    with tempfile.TemporaryDirectory() as root, \
            mock.patch.object(snapshot_store, "default_root",
                              return_value=root):
        ok(producer._mcpmarket_needs_fetch(SERVER_PAGE, False) is True,
           "missing snapshot reported as recorded")
        ok(producer._mcpmarket_needs_fetch(SERVER_PAGE, True) is True,
           "refresh mode must budget every URL")
        snapshot_store.write_snapshot(
            root, SERVER_PAGE, SOURCE, None, 200, PAGE_JSONLD)
        ok(producer._mcpmarket_needs_fetch(SERVER_PAGE, False) is False,
           "recorded snapshot reported as needing a fetch")
        body_path = os.path.join(snapshot_store.snapshot_dir(root, SERVER_PAGE),
                                 snapshot_store.BODY_FILE)
        with open(body_path, "ab") as f:
            f.write(b"tampered")
        ok(producer._mcpmarket_needs_fetch(SERVER_PAGE, False) is True,
           "corrupt recording must be re-fetched, not trusted")

    # --- full source block, offline -------------------------------------
    with tempfile.TemporaryDirectory() as root:
        out, requested, unexpected, sleeps, out_dir = run_producer(root)
        ok(not unexpected,
           f"block requested URLs outside the canned set: {unexpected}")
        ok(all(u.startswith("https://mcpmarket.com/") and "/api/" not in u
               for u in requested),
           f"block requested a host or path outside the source policy: {requested}")
        ok("7/7 mcpmarket.com..." in out, "mcpmarket block did not run")
        ok("ingestion failed" not in out, "mcpmarket block failed its run")
        ok("+3 rows" in out and "1 without name+summary" in out,
           f"unexpected row accounting: {out.splitlines()[-8:]}")
        ok("0 deferred by the fetch budget" in out,
           "run deferred pages although the budget was untouched")
        ok("2 sub-sitemaps" in out, "listing sub-sitemaps were not walked")
        ok(not any(u.endswith("/sitemap/static.xml") for u in requested),
           "the site-wide static sitemap was fetched as a listing set")
        ok('"incompleteSources": []' in out,
           "a clean run reported an incomplete source")
        ok("BUG" not in out,
           "consumed snapshots were not finalized")
        ok("7 promoted to healthy" in out and "3 rows attributed" in out,
           "snapshot finalization counts are wrong")
        ok("Scoped run complete: 3 row(s)" in out,
           "scoped run reported the wrong row count")
        ok(sleeps and all(s >= producer.MCPMARKET_CRAWL_DELAY for s in sleeps),
           f"robots Crawl-delay not honoured: {sleeps}")
        ok(not os.path.exists(out_dir),
           "a source-scoped run wrote a dataset")
        for url, rows in (
            (INDEX_URL, 0), (TOOLS_SMAP, 0), (SKILLS_SMAP, 0),
            (SERVER_PAGE, 1),
            ("https://mcpmarket.com/server/obsidian-connector-1", 1),
            ("https://mcpmarket.com/server/needs-summary-1", 0),
            ("https://mcpmarket.com/tools/skills/orchestrator", 1),
        ):
            meta = snapshot_meta(root, url)
            ok(meta["status"] == "healthy",
               f"{url} was not finalized healthy: {meta['status']}")
            ok(meta.get("itemCount") == rows,
               f"{url} attributed {meta.get('itemCount')} rows, expected {rows}")

    # A sitemap entry that fails the strict shape is reported as an
    # incomplete source and is never fetched.
    with tempfile.TemporaryDirectory() as root:
        out, requested, unexpected, _sleeps, _out_dir = run_producer(
            root, index=EVIL_INDEX_XML)
        ok(not unexpected,
           f"rejected sitemap URL was requested: {unexpected}")
        ok('"incompleteSources": ["feed:mcpmarket-com"]' in out,
           "a rejected sub-sitemap URL was not reported incomplete")
        ok("+0 rows" in out, "rejected sitemap URLs produced rows")

    # A spent budget defers pages, marks the source incomplete, and still
    # publishes exactly what it fetched.
    with tempfile.TemporaryDirectory() as root:
        out, requested, unexpected, _sleeps, _out_dir = run_producer(
            root, env={"MCPMARKET_MAX_PAGE_FETCHES": "1"})
        ok(not unexpected, f"unexpected request: {unexpected}")
        ok("+1 rows" in out and "3 deferred by the fetch budget" in out,
           f"budget accounting is wrong: {out.splitlines()[-8:]}")
        ok('"incompleteSources": ["feed:mcpmarket-com"]' in out,
           "deferred pages were not reported incomplete")

    print(f"MCPMARKET SOURCE TEST OK ({checks} checks)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
