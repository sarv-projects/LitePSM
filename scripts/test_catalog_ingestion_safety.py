"""Focused offline regression checks for producer fetch and registry behavior."""

import contextlib
import io
import socket
import os
import sys
import tempfile
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import build_full_catalog as producer  # noqa: E402
import snapshot_store  # noqa: E402


def _public_dns(host, port, type=None):
    return [(None, None, None, None, ("93.184.216.34", port))]


def main():
    checks = 0

    def ok(cond, message):
        nonlocal checks
        if not cond:
            raise AssertionError(message)
        checks += 1

    # Destination policy: configured exact hosts are accepted, sitemap-fed
    # arbitrary hosts, credentials, private literals and non-HTTPS URLs are not.
    with mock.patch.object(producer.socket, "getaddrinfo", side_effect=_public_dns):
        producer._validate_request_url(
            "https://skills.sh/sitemap.xml", "feed:skills-sh")
    for url in (
        "https://127.0.0.1/sitemap-skills-1.xml",
        "https://skills.sh.evil.example/sitemap.xml",
        "https://user:pass@skills.sh/sitemap.xml",
        "http://skills.sh/sitemap.xml",
    ):
        try:
            producer._validate_request_url(url, "feed:skills-sh", check_dns=False)
        except ValueError:
            ok(True, f"unsafe request URL accepted: {url}")
        else:
            ok(False, f"unsafe request URL accepted: {url}")

    with mock.patch.object(producer.socket, "getaddrinfo", return_value=[
            (None, None, None, None, ("169.254.169.254", 443))]):
        try:
            producer._validate_request_url(
                "https://skills.sh/sitemap.xml", "feed:skills-sh")
        except ValueError:
            ok(True, "allowlisted hostname resolving to link-local IP rejected")
        else:
            ok(False, "allowlisted hostname resolving to link-local IP accepted")

    # Mixed DNS answers fail closed rather than selecting just the public
    # answer. This is also checked again at the point the socket is opened.
    with mock.patch.object(producer.socket, "getaddrinfo", return_value=[
            (socket.AF_INET, socket.SOCK_STREAM, socket.IPPROTO_TCP, "",
             ("93.184.216.34", 443)),
            (socket.AF_INET, socket.SOCK_STREAM, socket.IPPROTO_TCP, "",
             ("10.0.0.4", 443)),
    ]):
        try:
            producer._resolve_public_addresses("skills.sh", 443)
        except ValueError:
            ok(True, "mixed public/private DNS answers rejected")
        else:
            ok(False, "mixed public/private DNS answers accepted")

    # The actual HTTPS connection uses the exact checked sockaddr and retains
    # the DNS hostname for certificate validation/SNI (no second DNS dial).
    class FakeSocket:
        connected = None
        timeout = None
        closed = False

        def settimeout(self, value):
            self.timeout = value

        def connect(self, address):
            self.connected = address

        def close(self):
            self.closed = True

    class FakeTLSContext:
        server_hostname = None

        def wrap_socket(self, sock, server_hostname=None):
            self.server_hostname = server_hostname
            return sock

    pinned = FakeSocket()
    tls_context = FakeTLSContext()
    allowed_sockaddr = ("93.184.216.34", 443)
    with mock.patch.object(producer.socket, "getaddrinfo", return_value=[
            (socket.AF_INET, socket.SOCK_STREAM, socket.IPPROTO_TCP, "", allowed_sockaddr)]), \
            mock.patch.object(producer.socket, "socket", return_value=pinned):
        conn_type = producer._source_https_connection("feed:skills-sh")
        conn = conn_type("skills.sh", timeout=2)
        conn._context = tls_context
        conn.connect()
    ok(pinned.connected == allowed_sockaddr,
       "HTTPS connection did not dial the exact validated sockaddr")
    ok(tls_context.server_hostname == "skills.sh",
       "HTTPS TLS hostname verification did not retain the origin hostname")

    # Replay cannot bypass the allowlist using an already-recorded arbitrary
    # sitemap URL; validation occurs before snapshot lookup.
    try:
        producer.obtain("https://127.0.0.1/sitemap-skills-1.xml",
                        "feed:skills-sh", 1, False, "test")
    except ValueError:
        ok(True, "unsafe replay URL rejected before snapshot lookup")
    else:
        ok(False, "unsafe replay URL bypassed destination policy")

    # Redirect validation applies the source allowlist and public-IP check.
    handler = producer._SourceRedirectHandler("feed:skills-sh")
    request = producer.urllib.request.Request("https://skills.sh/sitemap.xml")
    with mock.patch.object(producer.socket, "getaddrinfo", side_effect=_public_dns):
        redirected = handler.redirect_request(
            request, None, 302, "Found", {}, "https://www.skills.sh/sitemap-1.xml")
        ok(redirected is not None, "approved same-source redirect was not followed")
    try:
        handler.redirect_request(
            request, None, 302, "Found", {}, "https://169.254.169.254/latest/meta-data")
    except ValueError:
        ok(True, "private redirect destination rejected")
    else:
        ok(False, "private redirect destination accepted")

    ok(producer._is_source_sitemap_url(
        "https://www.skills.sh/sitemap-skills-1.xml", "feed:skills-sh",
        r"/sitemap-skills-[A-Za-z0-9._-]+\.xml"),
       "valid skills sub-sitemap URL rejected")
    for loc, source_id, pattern in (
        ("https://skills.sh.evil.example/sitemap-skills-1.xml", "feed:skills-sh",
         r"/sitemap-skills-[A-Za-z0-9._-]+\.xml"),
        ("https://skills.sh/nested/sitemap-skills-1.xml", "feed:skills-sh",
         r"/sitemap-skills-[A-Za-z0-9._-]+\.xml"),
        ("https://mcpservers.org.evil.example/sitemaps/servers/1.xml", "feed:mcpservers-org",
         r"/sitemaps/servers/[A-Za-z0-9._-]+\.xml"),
        ("https://mcpservers.org/sitemaps/servers/1.xml?next=bad", "feed:mcpservers-org",
         r"/sitemaps/servers/[A-Za-z0-9._-]+\.xml"),
    ):
        ok(not producer._is_source_sitemap_url(loc, source_id, pattern),
           f"invalid sitemap URL accepted: {loc}")

    # The streaming cap refuses an over-limit body even without Content-Length
    # and closes the response on the error path.
    class Response:
        headers = {}
        closed = False

        def getcode(self):
            return 200

        def read(self, size=-1):
            return b"12345"[:size]

        def close(self):
            self.closed = True

    response = Response()

    class Opener:
        def open(self, req, timeout=None):
            return response

    with mock.patch.object(producer, "MAX_RESPONSE_BYTES", 4), \
            mock.patch.object(producer.socket, "getaddrinfo", side_effect=_public_dns), \
            mock.patch.object(producer.urllib.request, "build_opener", return_value=Opener()):
        try:
            producer._http_get("https://skills.sh/sitemap.xml", 1,
                               source_id="feed:skills-sh")
        except ValueError as exc:
            ok("byte limit" in str(exc), "oversized response gave the wrong error")
        else:
            ok(False, "oversized response was accepted")
    ok(response.closed, "oversized HTTP response was not closed")

    # Registry metadata must be structurally complete and only an explicit
    # active status is publishable.
    elements, cursor = producer.registry_page_parts(
        b'{"servers": [], "metadata": {"nextCursor": "next"}}')
    ok(elements == [] and cursor == "next", "valid registry page envelope not decoded")
    for payload in (
        b'{"servers": []}',
        b'{"servers": [], "metadata": {"nextCursor": 12}}',
        ('{"servers": [], "metadata": {"nextCursor": "' + "x" * 4097 + '"}}').encode(),
    ):
        try:
            producer.registry_page_parts(payload)
        except (ValueError, TypeError):
            ok(True, "malformed registry pagination metadata rejected")
        else:
            ok(False, "malformed registry pagination metadata treated as complete")
    ok(producer.registry_status_is_active("active"), "explicit active status rejected")
    ok(not producer.registry_status_is_active(None), "missing status treated as active")
    ok(not producer.registry_status_is_active(""), "empty status treated as active")
    ok(not producer.registry_status_is_active("deprecated"), "deprecated status treated as active")
    ok(producer.registry_latest_flag(True), "true latest flag was lost")
    ok(not producer.registry_latest_flag(False), "false latest flag became true")
    try:
        producer.registry_latest_flag("false")
    except TypeError:
        ok(True, "non-boolean isLatest metadata rejected")
    else:
        ok(False, "truthy string isLatest metadata was accepted")

    # Refresh fallback is represented in the machine-readable run summary even
    # though bulk progress output is intentionally quiet.
    with tempfile.TemporaryDirectory() as root:
        url = "https://skills.sh/sitemap.xml"
        snapshot_store.write_snapshot(
            root, url, "feed:skills-sh", None, 200, b"<sitemap/>")
        producer._reset_run_ledger()
        with mock.patch.object(snapshot_store, "default_root", return_value=root), \
                mock.patch.object(producer, "_http_get", side_effect=OSError("offline")):
            result = producer.obtain(url, "feed:skills-sh", 1, True, "sitemap")
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            producer._print_ingestion_status(True, frozenset(), None)
        ok(result == b"<sitemap/>", "refresh failure did not use the recorded bytes")
        ok('"staleSources": ["feed:skills-sh"]' in output.getvalue(),
           "stale fallback was omitted from structured ingestion status")

    print(f"CATALOG INGESTION SAFETY TEST OK ({checks} checks)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
