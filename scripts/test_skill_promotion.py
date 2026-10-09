"""Offline regression checks for producer-side skill installability (A1/A3).

A skill row may only claim `metadata_verified` when this build opened the
SKILL.md at the path the row advertises; everything else stays
`discovery_only`. Nothing here touches the network: every block runs against
canned bytes behind a patched `_http_get`, with DNS pinned to a public test
IP, sleeps captured, and the snapshot store rooted in a temp directory.

Six groups of checks:

  (i)    fail-closed    -- an unverified row keeps `discovery_only` and emits
                           no version, command, args or transport;
  (ii)   promotion      -- a row promoted on build-time evidence is
                           version-less, command-less `metadata_verified`
                           with `publisher.url == skillSource`, and the whole
                           promoted set satisfies the ParseSkillSource git
                           rule the installer enforces;
  (iii)  officialskills -- the page's published GitHub link becomes
                           `skillSource` (a /tree/ link for this slug wins
                           over a bare `npx skills add <repo>`), a page that
                           publishes nothing usable promotes nothing, and the
                           page hosts are allowed for this source only;
  (iv)   Go mirror      -- `skill_source_parses_git` reproduces the Go test
                           tables in internal/skills/source_test.go and
                           source_tree_test.go, including their rejections;
  (v)    A3             -- a single-skill marketplace bundle emits BOTH the
                           plugin row and its skill row under distinct slugs,
                           and skills.sh content never promotes a row;
  (vi)   evidence kept  -- a skills.sh id another feed already published
                           enriches that row instead of being dropped, a
                           200-verified probe replays from its recording on a
                           second run, and a 404 is re-asked rather than
                           frozen into a permanent negative.

Run: python3 scripts/test_skill_promotion.py
"""

import contextlib
import io
import json
import os
import re
import sys
import tempfile
import urllib.error
from datetime import datetime, timezone
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import build_full_catalog as producer  # noqa: E402
import snapshot_store  # noqa: E402

VOLT = "feed:voltagent-awesome-agent-skills"
SKILLSH = "feed:skills-sh"
ASKILLS = "git:anthropics-skills"
README_URL = producer.FEED_AWESOME_SKILLS[0]

ALL_SOURCES = frozenset(producer.SOURCE_ALLOWED_HOSTS)

SOURCE_ID_RE = re.compile(r"^[a-z0-9_-]{3,32}:[a-z0-9_-]{3,64}$")
LISTING_ID_RE = re.compile(
    r"^(plugin|mcp|skill|connector|agent|rule|hook|tool|lsp):"
    r"[a-z0-9_:-]+:[A-Za-z0-9_.~%+-]+$")

PROMOTED = producer.METADATA_VERIFIED
UNPROMOTED = producer.DISCOVERY_ONLY


def _public_dns(host, port, type=None):
    return [(None, None, None, None, ("93.184.216.34", port))]


def skill_md(name, description):
    """A SKILL.md whose frontmatter passes the installer's own gates."""
    return (f"---\nname: {name}\ndescription: {description}\n---\n"
            f"# {name}\n\nInstructions.\n").encode()


def skills_sh_page(description, meta_description=None):
    """A public skills.sh page with its published JSON-LD description."""
    data = json.dumps({
        "@context": "https://schema.org",
        "@type": "SoftwareApplication",
        "description": description,
    })
    meta = (f'<meta name="description" content="{meta_description}">'
            if meta_description else "")
    return (f"<html><head>{meta}<script type=\"application/ld+json\">"
            f"{data}</script></head><body></body></html>").encode()


# --- canned fixtures -------------------------------------------------------

VOLT_README = b"""# Awesome Agent Skills

### Agent Skills

- **[acme/tree-ok](https://github.com/acme/skills/tree/main/skills/tree-ok)** - Verified tree skill
- **[acme/tree-missing](https://github.com/acme/skills/tree/main/skills/tree-missing)** - Missing tree skill
- **[acme/tree-clash](https://github.com/acme/skills/tree/main/skills/tree-clash)** - Clash tree skill
- **[acme/mismatch](https://github.com/acme/skills/tree/main/skills/mismatch)** - Mismatched tree skill
- **[acme/bare-ok](https://github.com/acme/bare-skills)** - Bare repository skill
- **[acme/listed](https://officialskills.sh/acme/skills/listed)** - Official page with a tree link
- **[acme/npxbare](https://officialskills.sh/acme/skills/npxbare)** - Official page with an npx command
- **[acme/nolink](https://officialskills.sh/acme/skills/nolink)** - Official page with no link
- **[acme/gone](https://officialskills.sh/acme/skills/gone)** - Official page that is gone
- **[acme/redhat](https://catalog.redhat.com/en/ai/skills/detail/x)** - Not a git host
"""

PAGE_LISTED = (
    b"<html><body><h1>listed</h1>"
    b'<a href="https://github.com/acme/official-skills/tree/main/skills/listed">'
    b"Source</a></body></html>"
)
PAGE_NPXBARE = (
    b"<html><body><code>npx skills add https://github.com/acme/npx-skills"
    b" --skill npxbare</code></body></html>"
)
PAGE_NOLINK = b"<html><body><p>No repository is published here.</p></body></html>"

TREE_OK_PROBE = (
    "https://raw.githubusercontent.com/acme/skills/main/skills/tree-ok/SKILL.md")
TREE_MISSING_PROBE = (
    "https://raw.githubusercontent.com/acme/skills/main/skills/tree-missing/SKILL.md")
TREE_CLASH_PROBE = (
    "https://raw.githubusercontent.com/acme/skills/main/skills/tree-clash/SKILL.md")
TREE_MISMATCH_PROBE = (
    "https://raw.githubusercontent.com/acme/skills/main/skills/mismatch/SKILL.md")

# raw SKILL.md / page probes: bytes -> HTTP 200, int -> that HTTP status.
VOLT_CANNED = {
    README_URL: VOLT_README,
    TREE_OK_PROBE: skill_md("tree-ok", "A verified tree skill."),
    TREE_MISSING_PROBE: 404,
    # A name SanitizeSkillName rejects: DiscoverSkills would still list the
    # skill (ParseSkillMD falls back to a header), but a promoted row promises
    # a named skill, so this one must not be promoted.
    TREE_CLASH_PROBE: skill_md("Some Other Skill",
                               "Frontmatter the installer cannot name."),
    # Both names are legal, but they disagree: selectSkillForListing would
    # refuse this install (when more than one skill is in scope), so the row
    # must not be promoted.
    TREE_MISMATCH_PROBE: skill_md("other-thing",
                                  "Frontmatter that disagrees with the row."),
    "https://raw.githubusercontent.com/acme/bare-skills/HEAD/bare-ok/SKILL.md": 404,
    "https://raw.githubusercontent.com/acme/bare-skills/HEAD/skills/bare-ok/SKILL.md": 404,
    "https://raw.githubusercontent.com/acme/bare-skills/HEAD/SKILL.md":
        skill_md("bare-ok", "Skill sitting at the repository root."),
    "https://raw.githubusercontent.com/acme/official-skills/main/skills/listed/SKILL.md":
        skill_md("listed", "Skill published by an officialskills.sh page."),
    "https://raw.githubusercontent.com/acme/npx-skills/HEAD/npxbare/SKILL.md": 404,
    "https://raw.githubusercontent.com/acme/npx-skills/HEAD/skills/npxbare/SKILL.md":
        skill_md("npxbare", "Skill found through the published bare repository."),
    "https://officialskills.sh/acme/skills/listed": PAGE_LISTED,
    "https://officialskills.sh/acme/skills/npxbare": PAGE_NPXBARE,
    "https://officialskills.sh/acme/skills/nolink": PAGE_NOLINK,
    "https://officialskills.sh/acme/skills/gone": 404,
}

ASKILLS_MANIFEST_URL = (
    "https://raw.githubusercontent.com/anthropics/skills/main/"
    ".claude-plugin/marketplace.json")
ASKILLS_MANIFEST = json.dumps({
    "name": "anthropic-agent-skills",
    "plugins": [
        {"name": "example-skills",
         "description": "Bundle of example skills",
         "source": {"source": "git",
                    "url": "https://github.com/anthropics/skills"},
         "skills": ["./skills/docx", "./skills/canvas-design"]},
        # Single-skill bundle: its plugin slug used to swallow its skill slug.
        {"name": "academy-guide",
         "description": "Single-skill bundle",
         "source": {"source": "git",
                    "url": "https://github.com/anthropics/skills"},
         "skills": ["./skills/academy-guide"]},
    ],
}).encode()
ASKILLS_CANNED = {
    ASKILLS_MANIFEST_URL: ASKILLS_MANIFEST,
    "https://raw.githubusercontent.com/anthropics/skills/main/skills/docx/SKILL.md":
        404,
    "https://raw.githubusercontent.com/anthropics/skills/main/skills/canvas-design/SKILL.md":
        skill_md("canvas-design", "Skill read from the manifest path."),
    "https://raw.githubusercontent.com/anthropics/skills/main/skills/academy-guide/SKILL.md":
        skill_md("academy-guide", "Skill read from the manifest path."),
}

MIN_README = b"""# Awesome Agent Skills

### Agent Skills

- **[acme/existing](https://github.com/acme/skills/tree/main/skills/existing)** - Summary from the awesome list
"""
SKILLSH_INDEX = (
    b'<?xml version="1.0" encoding="UTF-8"?>\n'
    b'<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
    b"  <sitemap><loc>https://skills.sh/sitemap-skills-1.xml</loc></sitemap>\n"
    b"</sitemapindex>\n"
)
SKILLSH_SUBMAP = (
    b'<?xml version="1.0" encoding="UTF-8"?>\n'
    b'<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
    b"<url><loc>https://skills.sh/acme/repo/existing</loc></url>\n"
    b"<url><loc>https://skills.sh/brand/new-skill/thing</loc></url>\n"
    # Duplicate sitemap locations must not cause duplicate row appends or
    # repeated requests within one build.
    b"<url><loc>https://skills.sh/brand/new-skill/thing</loc></url>\n"
    b"</urlset>\n"
)
SKILLSH_ENRICH_URL = "https://skills.sh/acme/repo/existing"
SKILLSH_NEW_URL = "https://skills.sh/brand/new-skill/thing"
ENRICH_DESC = "Summary published in the skills.sh page JSON-LD."

SKILLSH_CANNED = {
    README_URL: MIN_README,
    "https://raw.githubusercontent.com/acme/skills/main/skills/existing/SKILL.md": 404,
    producer.FEED_SKILLSH[0]: SKILLSH_INDEX,
    "https://skills.sh/sitemap-skills-1.xml": SKILLSH_SUBMAP,
    SKILLSH_ENRICH_URL: skills_sh_page(ENRICH_DESC),
    SKILLSH_NEW_URL: skills_sh_page("A brand new skill."),
}


def run_producer(root, canned, keep, refresh=False):
    """Run the real builder offline against `canned` bytes.

    `keep` names the source ids that execute; every other block is skipped,
    so the run is reproducible and scoped to the fixture. Any URL outside
    `canned` is recorded and refused, so a block that starts fetching
    something new fails the test instead of quietly touching the network.
    The dataset IS written -- into a temp CATALOG_OUT_DIR -- which is how the
    produced rows are inspected.
    """
    keep = frozenset(keep)
    assert keep and not (keep - ALL_SOURCES), keep
    skip_sources = ALL_SOURCES - keep
    requested = []
    unexpected = []
    sleeps = []
    out = io.StringIO()
    out_dir = os.path.join(root, "out")

    def fake_http_get(url, timeout, etag=None, source_id=None):
        requested.append(url)
        if url not in canned:
            unexpected.append(url)
            raise ValueError(f"unexpected request for {url}")
        value = canned[url]
        if isinstance(value, int):  # a bare status code: 404 and friends
            raise urllib.error.HTTPError(url, value, "error", {},
                                         io.BytesIO(b""))
        return 200, {}, value

    old_out = os.environ.get("CATALOG_OUT_DIR")
    os.environ["CATALOG_OUT_DIR"] = out_dir
    try:
        with mock.patch.dict(os.environ, {}), \
                mock.patch.object(producer, "_http_get",
                                  side_effect=fake_http_get), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root), \
                contextlib.redirect_stdout(out):
            producer.build_full_catalog(refresh=refresh,
                                        skip_sources=skip_sources,
                                        only_source=None)
    finally:
        if old_out is None:
            os.environ.pop("CATALOG_OUT_DIR", None)
        else:
            os.environ["CATALOG_OUT_DIR"] = old_out
    return out.getvalue(), requested, unexpected, out_dir, sleeps


def read_catalog(out_dir):
    with open(os.path.join(out_dir, "catalog.json"), encoding="utf-8") as f:
        return json.load(f)


def row_by_id(rows, row_id):
    for row in rows:
        if row["id"] == row_id:
            return row
    return None


def snapshot_status(root, url):
    _body, meta = snapshot_store.load_snapshot(root, url)
    return meta["snapshot"]


def check_rate_limit_contract(ok):
    """429s wait once, then open a source circuit instead of retrying each row."""
    fixed_now = datetime(2015, 10, 21, 7, 27, 53, tzinfo=timezone.utc)
    ok(producer._retry_after_delay("7", now=fixed_now) == 7,
       "Retry-After delay-seconds were not parsed")
    ok(producer._retry_after_delay(
        "Wed, 21 Oct 2015 07:28:00 GMT", now=fixed_now) == 7,
       "Retry-After HTTP-date was not parsed")
    ok(producer._retry_after_delay("not-a-date", now=fixed_now) is None,
       "invalid Retry-After was accepted")

    refresh_url = "https://skills.sh/acme/repo/refresh-429"
    refresh_body = skills_sh_page("Refresh fixture.")
    refresh_calls = []
    refresh_sleeps = []

    def seed_snapshot(url, timeout, etag=None, source_id=None):
        return 200, {"ETag": '"v1"'}, refresh_body

    def rate_limit_then_not_modified(url, timeout, etag=None, source_id=None):
        refresh_calls.append((url, etag))
        if len(refresh_calls) == 1:
            raise urllib.error.HTTPError(
                url, 429, "Too Many Requests", {"Retry-After": "5"}, io.BytesIO())
        return 304, {"ETag": '"v1"'}, b""

    with tempfile.TemporaryDirectory() as root:
        producer._reset_run_ledger()
        with mock.patch.object(producer, "_http_get",
                               side_effect=seed_snapshot), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root):
            seeded = producer._obtain_bulk(refresh_url, SKILLSH, 25, False,
                                           "skills.sh refresh seed")
        producer._reset_run_ledger()
        with mock.patch.object(producer, "_http_get",
                               side_effect=rate_limit_then_not_modified), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=refresh_sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root):
            refreshed = producer._obtain_bulk(refresh_url, SKILLSH, 25, True,
                                              "skills.sh refresh 429 test")
        ok(seeded == refresh_body and refreshed == seeded,
           "refresh-mode 429 recovery did not retain the recorded response")
        ok(len(refresh_calls) == 2 and
           [call[1] for call in refresh_calls] == ['"v1"', '"v1"'] and
           refresh_sleeps == [5],
           "refresh-mode 429 was swallowed instead of retried after Retry-After")
        ok(SKILLSH not in producer._STALE_SOURCES,
           "successful refresh after 429 was incorrectly marked stale")

    rate_url = "https://skills.sh/acme/repo/rate-limited"
    requests = []
    sleeps = []

    def always_rate_limited(url, timeout, etag=None, source_id=None):
        requests.append(url)
        raise urllib.error.HTTPError(
            url, 429, "Too Many Requests", {"Retry-After": "7"}, io.BytesIO())

    recover_url = "https://skills.sh/acme/repo/recover-after-wait"
    recover_requests = []
    recover_sleeps = []

    def recover_once(url, timeout, etag=None, source_id=None):
        recover_requests.append(url)
        if len(recover_requests) == 1:
            raise urllib.error.HTTPError(
                url, 429, "Too Many Requests", {"Retry-After": "7"}, io.BytesIO())
        return 200, {"ETag": '"stable"'}, skills_sh_page("Recovered.")

    with tempfile.TemporaryDirectory() as root:
        producer._reset_run_ledger()
        with mock.patch.object(producer, "_http_get",
                               side_effect=recover_once), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=recover_sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root):
            body = producer._obtain_bulk(recover_url, SKILLSH, 25, False,
                                         "skills.sh recover test")
            replay = producer._obtain_bulk(recover_url, SKILLSH, 25, False,
                                           "skills.sh replay test")

        ok(body == skills_sh_page("Recovered.") and replay == body,
           "the bounded Retry-After retry did not produce a replayable snapshot")
        ok(len(recover_requests) == 2,
           f"the successful retry/replay made {len(recover_requests)} requests")
        ok(recover_sleeps == [7],
           f"successful Retry-After delay was not honored: {recover_sleeps}")

    # The allowance is source-wide, not one retry per URL: if the same feed
    # rate-limits again later in this run, stop immediately rather than retrying.
    later_url = "https://skills.sh/acme/repo/later-429"
    later_requests = []

    def later_rate_limit(url, timeout, etag=None, source_id=None):
        later_requests.append(url)
        raise urllib.error.HTTPError(
            url, 429, "Too Many Requests", {"Retry-After": "7"}, io.BytesIO())

    with tempfile.TemporaryDirectory() as root:
        caught = None
        with mock.patch.object(producer, "_http_get",
                               side_effect=later_rate_limit), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=recover_sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root):
            try:
                producer._obtain_bulk(later_url, SKILLSH, 25, False,
                                      "skills.sh later 429 test")
            except Exception as exc:
                caught = exc
        ok(type(caught).__name__ == "RateLimitExceeded" and
           len(later_requests) == 1,
           "a later skills.sh 429 did not immediately open the source circuit")
        ok(recover_sleeps == [7],
           "a later source-level 429 caused an extra wait/retry")

    with tempfile.TemporaryDirectory() as root:
        producer._reset_run_ledger()
        caught = None
        with mock.patch.object(producer, "_http_get",
                               side_effect=always_rate_limited), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root):
            try:
                producer._obtain_bulk(rate_url, SKILLSH, 25, False,
                                      "skills.sh rate-limit test")
            except Exception as exc:  # the source circuit must fail closed
                caught = exc

        ok(type(caught).__name__ == "RateLimitExceeded",
           f"persistent 429 did not stop the source: {caught!r}")
        ok(len(requests) == 2,
           f"429 handling made {len(requests)} requests; expected one retry")
        ok(sleeps == [7],
           f"Retry-After was not honored exactly once: {sleeps}")
        ok(SKILLSH in producer._INCOMPLETE_SOURCES,
           "rate-limited source was not marked incomplete")

    # Missing Retry-After uses a conservative fallback; an excessive server
    # wait is not shortened into an early retry.
    fallback_url = "https://skills.sh/acme/repo/no-retry-after"
    fallback_requests = []
    fallback_sleeps = []

    def no_retry_after(url, timeout, etag=None, source_id=None):
        fallback_requests.append(url)
        raise urllib.error.HTTPError(
            url, 429, "Too Many Requests", {}, io.BytesIO())

    with tempfile.TemporaryDirectory() as root:
        producer._reset_run_ledger()
        caught = None
        with mock.patch.object(producer, "_http_get",
                               side_effect=no_retry_after), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=fallback_sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root):
            try:
                producer._obtain_bulk(fallback_url, SKILLSH, 25, False,
                                      "skills.sh missing Retry-After test")
            except Exception as exc:
                caught = exc
        ok(type(caught).__name__ == "RateLimitExceeded",
           f"missing Retry-After did not stop after its bounded retry: {caught!r}")
        ok(len(fallback_requests) == 2 and
           fallback_sleeps == [producer.RATE_LIMIT_FALLBACK_DELAY_SECONDS],
           "missing Retry-After did not use the conservative fallback exactly once")

    too_long_url = "https://skills.sh/acme/repo/too-long-wait"
    too_long_requests = []
    too_long_sleeps = []

    def excessive_retry_after(url, timeout, etag=None, source_id=None):
        too_long_requests.append(url)
        raise urllib.error.HTTPError(
            url, 429, "Too Many Requests", {"Retry-After": "3601"}, io.BytesIO())

    with tempfile.TemporaryDirectory() as root:
        producer._reset_run_ledger()
        caught = None
        with mock.patch.object(producer, "_http_get",
                               side_effect=excessive_retry_after), \
                mock.patch.object(producer.socket, "getaddrinfo",
                                  side_effect=_public_dns), \
                mock.patch.object(producer.time, "sleep",
                                  side_effect=too_long_sleeps.append), \
                mock.patch.object(snapshot_store, "default_root",
                                  return_value=root):
            try:
                producer._obtain_bulk(too_long_url, SKILLSH, 25, False,
                                      "skills.sh excessive Retry-After test")
            except Exception as exc:
                caught = exc
        ok(type(caught).__name__ == "RateLimitExceeded",
           f"excessive Retry-After was not refused: {caught!r}")
        ok(len(too_long_requests) == 1 and not too_long_sleeps,
           "an excessive Retry-After was shortened or retried early")


# --- (iv) the Go ParseSkillSource mirror -----------------------------------

# internal/skills/source_test.go TestParseSkillSourceURLForms (and Rejects),
# plus source_tree_test.go TestParseSkillSourceGitHubTreeURLs.
GO_SOURCE_TABLE = [
    ("https://example.com/skills.tar.gz", False),
    ("https://example.com/skills", False),
    ("https://skills.sh/anthropics/skills", False),
    ("https://example.com/skills?foo=bar", False),
    ("https://github.com/mattpocock/skills", True),
    ("https://github.com/mattpocock/skills.git", True),
    ("https://github.com/mattpocock/skills/", True),
    ("https://gitlab.com/group/repo", True),
    ("anthropics/skills@v1.2.3", True),
    ("https://github.com/anthropics/skills@abc123", True),
    ("https://github.com/anthropics/skills?ref=abc123", True),
    ("https://example.com/skills@deadbeef", False),
    # TestParseSkillSourceRejectsNonHTTPS -- rejected by Go, False here.
    ("http://example.com/x.git", False),
    ("git@github.com:x/y.git", False),
    ("ftp://example.com/x", False),
    # GitHub browse URLs resolve to a repository clone target.
    ("https://github.com/coreyhaines31/marketingskills/tree/main/skills/ab-testing", True),
    ("https://github.com/google/skills/tree/main/skills/cloud/agent-platform-skill-registry", True),
    ("https://github.com/owner/repo/tree/v1.2.3", True),
    ("https://github.com/owner/repo/blob/main/skills/thing/SKILL.md", True),
    ("https://github.com/owner/repo/tree/main/skills/x?ref=abc123", True),
    ("", False),
]

GO_TREE_TABLE = [
    # raw -> (owner/repo, ref, subpath); an explicit pin wins over the tree ref
    ("https://github.com/coreyhaines31/marketingskills/tree/main/skills/ab-testing",
     ("coreyhaines31/marketingskills", "main", "skills/ab-testing")),
    ("https://github.com/owner/repo/tree/main/skills/x?ref=abc123",
     ("owner/repo", "abc123", "skills/x")),
    ("https://github.com/owner/repo/tree/v1.2.3", ("owner/repo", "v1.2.3", "")),
    ("https://github.com/owner/repo", None),
    ("https://gitlab.com/group/repo/tree/main/x", None),
]


def main():
    checks = 0

    def ok(cond, message):
        nonlocal checks
        if not cond:
            raise AssertionError(message)
        checks += 1

    ok(len(ALL_SOURCES) >= 13,
       f"source registry shrank: {sorted(ALL_SOURCES)}")
    check_rate_limit_contract(ok)

    # --- (iv) Python mirror of internal/skills ----------------------------
    for raw, want in GO_SOURCE_TABLE:
        got = producer.skill_source_parses_git(raw)
        ok(got is want,
           f"ParseSkillSource mirror disagrees on {raw!r}: got {got}, want {want}")
    for raw, want in GO_TREE_TABLE:
        got = producer.github_tree_facts(raw)
        ok(got == want,
           f"splitGitTreeBrowsePath mirror disagrees on {raw!r}: got {got}, want {want}")
    for raw in ("", "   ", None):
        ok(producer.skill_source_parses_git(raw) is False,
           f"empty source {raw!r} must not parse as a git source")
    ok(producer.sanitize_skill_name("Ab Test Analysis") is None,
       "a spacey name must not sanitize (SanitizeSkillName mirror)")
    ok(producer.sanitize_skill_name("ab-test-analysis") == "ab-test-analysis",
       "a legal skill name did not sanitize")
    ok(producer.sanitize_skill_name("") is None,
       "an empty name must not sanitize")
    # Frontmatter gate: name + description, block-scoped, line-length capped.
    ok(producer.skill_md_frontmatter(
        "---\nname: x\ndescription: y\n---\nbody") == ("x", "y"),
       "valid frontmatter not parsed")
    ok(producer.skill_md_frontmatter("---\nname: x\n---\nbody") == (None, None),
       "frontmatter without a description must not verify (DiscoverSkills)")
    ok(producer.skill_md_frontmatter("---\ndescription: y\n---\n") == (None, None),
       "frontmatter without a name must not verify")
    ok(producer.skill_md_frontmatter("# heading\ndescription: y\n") == (None, None),
       "a body without a frontmatter block must not verify")
    ok(producer.skill_md_frontmatter(
        "---\nname: x\ndescription: y\n---\ndescription: not-a-key\n") == ("x", "y"),
       "a description line outside the frontmatter block was accepted")
    ok(producer.skill_md_frontmatter(
        "---\nname: x\ndescription: y\n---\n" + "z" * 70000) == (None, None),
       "a line beyond bufio.Scanner's token limit must not verify")
    # Public skills.sh pages expose the catalog summary via JSON-LD, with a
    # metadata fallback. The /api path is disallowed by robots.txt.
    long_desc = "A full public-page description that is not truncated."
    ok(producer.skills_sh_page_description(skills_sh_page(long_desc)) == long_desc,
       "skills.sh JSON-LD description was not parsed")
    ok(producer.skills_sh_page_description(
        b'<meta property="og:description" content="Page summary">') == "Page summary",
       "skills.sh page metadata fallback was not parsed")
    ok(producer.skills_sh_page_description(
        b'<script type="application/ld+json">not json</script>') is None,
       "malformed skills.sh JSON-LD produced a description")
    ok(producer.skills_sh_listing_parts(
        "https://www.skills.sh/anthropics/skills/pdf")
       == ("anthropics", "skills", "pdf"),
       "a sitemap-listed public skill page was not accepted")
    for forbidden in (
            "https://skills.sh/api/repo/slug",
            "https://skills.sh/internal/repo/slug",
            "https://skills.sh/debug-security/repo/slug",
            "https://skills.sh/search/repo/slug",
            "https://elsewhere.example/a/b/c",
            "https://skills.sh/a/b/c?query=1",
            "https://skills.sh/a/b/../c"):
        ok(producer.skills_sh_listing_parts(forbidden) is None,
           f"unsafe or non-public skills.sh URL was accepted: {forbidden}")
    for forbidden in (
            "https://skills.sh/api/repo/slug",
            "https://skills.sh/internal/repo/slug",
            "https://skills.sh/debug-security/repo/slug",
            "https://skills.sh/search/repo/slug",
            "https://elsewhere.example/a/b/c",
            "https://skills.sh/a/b/../c"):
        try:
            producer._validate_request_url(
                forbidden, SKILLSH, check_dns=False)
        except ValueError:
            pass
        else:
            raise AssertionError(
                f"robots-disallowed or unsafe skills.sh URL was accepted: {forbidden}")
    # Probe URLs: one for a tree source, at most three (depth <= 2) for a
    # bare repository, none for a host nothing can verify.
    ok(producer.skill_probe_urls(
        "https://github.com/acme/skills/tree/main/skills/x", "x")
       == ["https://raw.githubusercontent.com/acme/skills/main/skills/x/SKILL.md"],
       "a tree source must probe exactly one path")
    ok(len(producer.skill_probe_urls("https://github.com/acme/repo", "x")) == 3,
       "a bare repository must probe at most depth-2 paths")
    ok(producer.skill_probe_urls("https://catalog.redhat.com/x/detail/y", "x") == [],
       "a non-GitHub source must offer no probe")

    # --- (iii) destination policy ----------------------------------------
    with mock.patch.object(producer.socket, "getaddrinfo",
                           side_effect=_public_dns):
        producer._validate_request_url(
            "https://officialskills.sh/acme/skills/listed", VOLT)
        producer._validate_request_url(
            "https://raw.githubusercontent.com/acme/skills/main/x/SKILL.md", VOLT)
        producer._validate_request_url(
            "https://raw.githubusercontent.com/anthropics/skills/main/x/SKILL.md",
            ASKILLS)
    for url, source in (
        ("https://officialskills.sh/acme/skills/listed", ASKILLS),
        ("https://evil.example/officialskills.sh/x", VOLT),
        ("https://officialskills.sh.evil.example/x", VOLT),
        ("https://skills.sh/x", VOLT),
        ("http://officialskills.sh/x", VOLT),
        ("https://mcpmarket.com/x", VOLT),
    ):
        try:
            producer._validate_request_url(url, source, check_dns=False)
        except ValueError:
            ok(True, f"unallowed verification host rejected: {url} ({source})")
        else:
            ok(False, f"unallowed verification host accepted: {url} ({source})")

    # Page-source preference: a /tree/ link for this slug, else the bare
    # repository the page's own npx command names, else nothing.
    ok(producer.officialskills_page_source(PAGE_LISTED, "listed")
       == "https://github.com/acme/official-skills/tree/main/skills/listed",
       "the page's /tree/ link was not adopted")
    ok(producer.officialskills_page_source(PAGE_LISTED, "some-other-skill")
       == "https://github.com/acme/official-skills/tree/main/skills/listed"
       or producer.officialskills_page_source(PAGE_LISTED, "some-other-skill")
       is None,
       "a /tree/ link must not match a different slug")
    ok(producer.officialskills_page_source(PAGE_NPXBARE, "npxbare")
       == "https://github.com/acme/npx-skills",
       "the page's published bare repository was not adopted")
    ok(producer.officialskills_page_source(PAGE_NOLINK, "nolink") is None,
       "a page with no repository link produced a source")

    # ======================================================================
    # (i)/(ii)/(iii) the VoltAgent block, offline
    # ======================================================================
    with tempfile.TemporaryDirectory() as root:
        out, requested, unexpected, out_dir, sleeps = run_producer(
            root, VOLT_CANNED, keep=[VOLT])
        ok(not unexpected, f"block fetched URLs outside the fixture: {unexpected}")
        ok("skill verification:" in out, "verification tally was not printed")
        ok("BUG" not in out, "a consumed snapshot had no recording")
        ok(not sleeps or all(s <= producer._VERIFY_DELAY for s in sleeps),
           f"verification pacing ran away: {sorted(set(sleeps))}")

        rows = read_catalog(out_dir)
        ok(len(rows) == 10, f"expected 10 rows, got {len(rows)}")
        by_id = {row["id"]: row for row in rows}

        promoted = ["skill:acme:tree-ok", "skill:acme:bare-ok",
                    "skill:acme:listed", "skill:acme:npxbare"]
        unpromoted = ["skill:acme:tree-missing", "skill:acme:tree-clash",
                      "skill:acme:mismatch", "skill:acme:nolink",
                      "skill:acme:gone", "skill:acme:redhat"]

        # (ii) promotion evidence and the row shape a promoted row must keep.
        for row_id in promoted:
            row = by_id.get(row_id)
            ok(row is not None, f"promoted row missing: {row_id}")
            ok(row["installability"] == PROMOTED,
               f"{row_id} was not promoted: {row['installability']}")
            ok(row.get("version") is None,
               f"promoted row {row_id} carries a version: {row.get('version')!r}")
            for key in ("command", "args", "transport", "runtime"):
                ok(key not in row,
                   f"promoted row {row_id} carries a fabricated {key}")
            ok(row["publisher"]["url"] == row["skillSource"],
               f"promoted row {row_id} would install from "
               f"{row['publisher']['url']!r} instead of {row['skillSource']!r}")
            ok(producer.skill_source_parses_git(row["skillSource"]),
               f"promoted row {row_id} has a non-git source "
               f"{row['skillSource']!r}")

        # (i) an unverified row stays discovery_only and keeps its source.
        for row_id in unpromoted:
            row = by_id.get(row_id)
            ok(row is not None, f"row missing: {row_id}")
            ok(row["installability"] == UNPROMOTED,
               f"{row_id} was promoted without evidence: "
               f"{row['installability']}")
            ok(row.get("version") is None and "command" not in row
               and "args" not in row,
               f"unverified row {row_id} carries a fabricated claim")

        ok(by_id["skill:acme:listed"]["skillSource"]
           == "https://github.com/acme/official-skills/tree/main/skills/listed",
           "the published /tree/ link did not become skillSource")
        ok(by_id["skill:acme:npxbare"]["skillSource"]
           == "https://github.com/acme/npx-skills",
           "the published bare repository did not become skillSource")
        ok(by_id["skill:acme:nolink"]["publisher"]["url"]
           == "https://officialskills.sh/acme/skills/nolink",
           "an unpromoted officialskills row must keep its page as publisher.url")
        ok(by_id["skill:acme:nolink"]["skillSource"]
           == "https://officialskills.sh/acme/skills/nolink",
           "a page with no link must keep its page URL as skillSource")
        ok(by_id["skill:acme:npxbare"]["installability"] == PROMOTED
           and by_id["skill:acme:nolink"]["installability"] == UNPROMOTED,
           "a bare repository promoted only when its skill path was verified")

        # Every row of this run is version-less and command-less.
        for row in rows:
            ok(LISTING_ID_RE.fullmatch(row["id"]) is not None,
               f"id outside the listing grammar: {row['id']}")
            ok(SOURCE_ID_RE.fullmatch(row["source"]) is not None,
               f"source outside the SourceID grammar: {row['source']!r}")
            ok(row.get("version") is None,
               f"row {row['id']} carries a version")

        # (vi) second run: recorded 200s replay offline, 404s are re-asked.
        out2, requested2, unexpected2, out_dir2, _ = run_producer(
            root, VOLT_CANNED, keep=[VOLT])
        ok(not unexpected2,
           f"second run fetched URLs outside the fixture: {unexpected2}")
        recorded = {url for url, value in VOLT_CANNED.items()
                    if not isinstance(value, int)}
        refetched = [url for url in requested2 if url in recorded]
        ok(not refetched,
           f"recorded snapshots were fetched again: {refetched}")
        absent = {url for url, value in VOLT_CANNED.items()
                  if isinstance(value, int)}
        ok(set(requested2) <= absent,
           f"second run fetched something other than an absent path: "
           f"{sorted(set(requested2) - absent)}")
        rows2 = read_catalog(out_dir2)
        ok([(r["id"], r["installability"]) for r in rows]
           == [(r["id"], r["installability"]) for r in rows2],
           "a replay run changed the promoted set")
        snap = snapshot_status(root, TREE_OK_PROBE)
        ok(snap["status"] == "healthy" and snap.get("itemCount") == 1,
           f"verified probe not finalized with its row: {snap}")

    # ======================================================================
    # (v) the askills marketplace: A3 slug fix + manifest-path verification
    # ======================================================================
    with tempfile.TemporaryDirectory() as root:
        out, requested, unexpected, out_dir, _ = run_producer(
            root, ASKILLS_CANNED, keep=[ASKILLS])
        ok(not unexpected,
           f"marketplace fetched URLs outside the fixture: {unexpected}")
        rows = read_catalog(out_dir)
        plugin = row_by_id(rows, "plugin:anthropic-skills:academy-guide")
        skill = row_by_id(rows, "skill:anthropics:academy-guide")
        ok(plugin is not None, "the single-skill bundle's plugin row is missing")
        ok(skill is not None,
           "A3: the single-skill bundle's skill row was swallowed by the "
           "plugin slug")
        ok(plugin["slug"] != skill["slug"],
           f"A3: plugin and skill share a slug: {plugin['slug']!r}")
        ok(skill["slug"] == "askills-skill-academy-guide",
           f"unexpected skill slug: {skill['slug']!r}")
        ok(skill["skillSource"].endswith("/skills/academy-guide"),
           f"skillSource is not the manifest's path: {skill['skillSource']!r}")
        ok(skill["installability"] == PROMOTED,
           "a skill whose SKILL.md was read at the manifest path was not promoted")
        ok(skill.get("version") is None and "command" not in skill,
           "a promoted askills row carries a fabricated claim")
        ok(skill["publisher"]["url"] == skill["skillSource"],
           "a promoted askills row would install from a different URL")
        docx = row_by_id(rows, "skill:anthropics:docx")
        ok(docx is not None and docx["installability"] == UNPROMOTED,
           "a manifest path with no SKILL.md must stay discovery_only")
        canvas = row_by_id(rows, "skill:anthropics:canvas-design")
        ok(canvas is not None and canvas["installability"] == PROMOTED,
           "a skill read at its manifest path was not promoted")
        ok(row_by_id(rows, "plugin:anthropic-skills:example-skills")
           ["installability"] == PROMOTED,
           "vendor plugin rows lost their manifest class")
        for row in rows:
            ok(row.get("version") is None
               or row["kind"] == "plugin",
               f"row {row['id']} carries an invented version")
        ok(sum(1 for r in rows if r["kind"] == "skill") == 3,
           "the fixture's three skills did not all emit rows")
        ok(sum(1 for r in rows if r["kind"] == "plugin") == 2,
           "the fixture's two plugins did not all emit rows")

    # ======================================================================
    # (v)/(vi) skills.sh enrichment of a row another feed already published
    # ======================================================================
    with tempfile.TemporaryDirectory() as root:
        out, requested, unexpected, out_dir, _ = run_producer(
            root, SKILLSH_CANNED, keep=[VOLT, SKILLSH])
        ok(not unexpected,
           f"block fetched URLs outside the fixture: {unexpected}")
        ok("1 summaries refreshed from public pages" in out,
           f"existing row was not enriched: {out.splitlines()[-14:]}")
        ok("+1 rows" in out, "the new skills.sh id did not add a row")
        ok("BUG" not in out, "a consumed snapshot had no recording")

        rows = read_catalog(out_dir)
        existing = row_by_id(rows, "skill:acme:existing")
        fresh = row_by_id(rows, "skill:brand:thing")
        ok(sum(1 for row in rows if row["id"] == "skill:brand:thing") == 1,
           "duplicate skills.sh sitemap locations emitted duplicate rows")
        ok(requested.count(SKILLSH_NEW_URL) == 1,
           "duplicate skills.sh sitemap locations fetched the same record twice")
        ok(existing is not None, "the existing row disappeared")
        ok(existing["summary"] == ENRICH_DESC,
            f"summary was not refreshed from public page: {existing['summary']!r}")
        ok(existing["source"] == VOLT,
           f"enrichment rewrote the row's source: {existing['source']!r}")
        ok(existing["skillSource"]
           == "https://github.com/acme/skills/tree/main/skills/existing",
           "enrichment rewrote the row's skillSource")
        # (v) content proves a description, not a clonable git path.
        ok(existing["installability"] == UNPROMOTED,
           "skills.sh content promoted a row it cannot prove is clonable")
        ok(existing.get("version") is None and "command" not in existing,
           "an enriched row carries a fabricated claim")
        ok(fresh is not None and fresh["source"] == SKILLSH
           and fresh["summary"] == "A brand new skill.",
            "the new skills.sh row was not built from its public page")
        ok(all("/api/" not in url for url in requested),
           "producer requested a robots-disallowed skills.sh API path")

        # Ledger: the enrichment record created no row, the new one created one.
        ok(snapshot_status(root, SKILLSH_ENRICH_URL)["itemCount"] == 0,
           "the enrichment record was attributed rows it did not create")
        ok(snapshot_status(root, SKILLSH_NEW_URL)["itemCount"] == 1,
           "the new row was not attributed to its own record")
        ok(snapshot_status(root, SKILLSH_ENRICH_URL)["status"] == "healthy",
           "a parsed enrichment record was not finalized")

        # Replaying the durable snapshots after interruption must rebuild the
        # same unique dataset without re-fetching successful skills.sh records.
        out2, requested2, unexpected2, out_dir2, _ = run_producer(
            root, SKILLSH_CANNED, keep=[VOLT, SKILLSH])
        rows2 = read_catalog(out_dir2)
        ok(not unexpected2,
           f"replay fetched URLs outside the fixture: {unexpected2}")
        ok(rows2 == rows,
           "snapshot replay changed rows or introduced duplicates")
        ok(sum(1 for row in rows2 if row["id"] == "skill:brand:thing") == 1,
           "snapshot replay emitted duplicate skills.sh rows")
        ok(SKILLSH_NEW_URL not in requested2,
           "snapshot replay re-fetched the already recorded skill")

    print(f"SKILL PROMOTION TEST OK ({checks} checks)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
