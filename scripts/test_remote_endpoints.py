"""Offline regression checks for the remote (URL) MCP endpoint data path (B1).

The Official MCP Registry's server.json declares endpoints in `remotes[]`
([{type, url}, ...]); multiple endpoints per server are the norm upstream (a
live sample carried 41 entries across 38 servers). These checks pin the
producer half of the contract, with no network and no recorded snapshots:

  * the selection rule (first `streamable-http`, else the first entry) and the
    count of rows where extra entries were dropped;
  * the publish policy (an endpoint the egress guard would later refuse is
    never published: non-https and non-loopback, credentials, fragments,
    non-default ports, non-public IP literals);
  * the honesty rules: metadata_verified only when an endpoint was actually
    published, command/args/runtime always null, version only from the
    document, and no invented launch line of any kind.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import build_full_catalog as producer  # noqa: E402

ITEM_ID = "mcp:builtin:mcp-registry:io.github.acme-remote-mcp"


def fixture_server(remotes=None, version="2.1.0"):
    """A minimal active server.json document as the registry publishes it."""
    srv = {
        "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
        "name": "io.github.acme/remote-mcp",
        "description": "Acme's hosted MCP endpoint.",
        "version": version,
    }
    if remotes is not None:
        srv["remotes"] = remotes
    return srv


def build_row(srv):
    """Run the real per-server row builder the registry block uses."""
    return producer.registry_row(
        ITEM_ID,
        srv.get("title") or srv["name"],
        "remote-mcp",
        "Developer Tools",
        srv["description"],
        "acme",
        "https://github.com/acme",
        srv,
    )


def main():
    checks = 0

    def ok(cond, message):
        nonlocal checks
        if not cond:
            raise AssertionError(message)
        checks += 1

    # --- Selection rule -------------------------------------------------
    one = fixture_server([{"type": "streamable-http", "url": "https://mcp.acme.example/mcp"}])
    entry, extras = producer.select_remote_endpoint(one["remotes"])
    ok(entry["url"] == "https://mcp.acme.example/mcp",
       "single remote endpoint not selected")
    ok(extras == 0, "a single-endpoint document must drop nothing")

    three = fixture_server([
        {"type": "sse", "url": "https://sse.acme.example/sse"},
        {"type": "streamable-http", "url": "https://mcp.acme.example/mcp"},
        {"type": "sse", "url": "https://other.acme.example/sse"},
    ])
    entry, extras = producer.select_remote_endpoint(three["remotes"])
    ok(entry["url"] == "https://mcp.acme.example/mcp",
       "selection did not prefer the streamable-http entry")
    ok(extras == 2, f"3 declared endpoints must drop 2, got {extras}")

    all_sse = fixture_server([
        {"type": "sse", "url": "https://sse1.acme.example/sse"},
        {"type": "sse", "url": "https://sse2.acme.example/sse"},
    ])
    entry, extras = producer.select_remote_endpoint(all_sse["remotes"])
    ok(entry["url"] == "https://sse1.acme.example/sse",
       "without a streamable-http entry the first one must be selected")
    ok(extras == 1, "the unchosen sse endpoint must be counted as dropped")

    ok(producer.select_remote_endpoint(None) == (None, 0),
       "a document with no remotes must declare no endpoint")
    ok(producer.select_remote_endpoint("not-a-list") == (None, 0),
       "a malformed remotes field must not be treated as an endpoint")
    ok(producer.select_remote_endpoint(["not-a-dict"]) == (None, 0),
       "a malformed remotes entry must not be treated as an endpoint")

    # --- Row shape for a publishable endpoint ---------------------------
    row, extras, refused = build_row(one)
    ok(row["url"] == "https://mcp.acme.example/mcp", "published URL lost")
    ok(row["transport"] == "streamable-http", "published transport lost")
    ok(row["installability"] == producer.METADATA_VERIFIED,
       "a published endpoint must classify metadata_verified (Decision 7)")
    ok(row["version"] == "2.1.0", "the top-level server.json version was not kept")
    ok(extras == 0 and refused is False,
       "a single publishable endpoint must report no drops and no refusal")

    row, extras, refused = build_row(three)
    ok(row["url"] == "https://mcp.acme.example/mcp" and row["transport"] == "streamable-http",
       "multi-endpoint document did not publish the selected endpoint")
    ok(extras == 2, f"multi-endpoint row must report 2 dropped entries, got {extras}")
    ok(row["installability"] == producer.METADATA_VERIFIED,
       "a multi-endpoint row with a publishable selection must be verified")

    # --- Rows that must stay discovery_only -----------------------------
    no_remotes = fixture_server(remotes=None)
    row, extras, refused = build_row(no_remotes)
    ok(row["url"] is None and row["transport"] is None,
       "a document with no remotes must publish no endpoint")
    ok(row["installability"] == producer.DISCOVERY_ONLY,
       "an endpoint-less registry row must stay discovery_only")

    for label, bad in (
        ("plain http (non-loopback)", "http://mcp.acme.example/mcp"),
        ("credentials in the URL", "https://user:pass@mcp.acme.example/mcp"),
        ("URL fragment", "https://mcp.acme.example/mcp#tok"),
        ("non-default port", "https://mcp.acme.example:8443/mcp"),
        ("link-local literal", "https://169.254.169.254/latest/meta-data"),
        ("private literal", "https://10.0.0.8/mcp"),
        ("wrong scheme", "ws://mcp.acme.example/mcp"),
        ("not a string", None),
    ):
        srv = fixture_server([{"type": "streamable-http", "url": bad}])
        row, extras, refused = build_row(srv)
        ok(row["url"] is None and row["transport"] is None,
           f"refusable endpoint was published ({label}): {bad!r}")
        ok(row["installability"] == producer.DISCOVERY_ONLY,
           f"a refused endpoint must keep discovery_only ({label})")
        ok(refused is True, f"a refused endpoint must be logged as refused ({label})")

    # Loopback over plain http is the guard's one carve-out
    # (egress.Policy.AllowLoopbackHTTP): a local dev endpoint is publishable.
    loopback = fixture_server([{"type": "streamable-http", "url": "http://127.0.0.1:8080/mcp"}])
    row, extras, refused = build_row(loopback)
    ok(row["url"] == "http://127.0.0.1:8080/mcp",
       "the loopback http carve-out must be publishable")
    ok(refused is False, "a loopback endpoint must not be counted as refused")

    # A declared endpoint with no transport type cannot be published either:
    # catalogbuild.ParseDataset refuses a verified row without transport.
    no_type = fixture_server([{"url": "https://mcp.acme.example/mcp"}])
    row, extras, refused = build_row(no_type)
    ok(row["url"] is None and row["transport"] is None,
       "an endpoint with no transport type must not be published")
    ok(row["installability"] == producer.DISCOVERY_ONLY,
       "an untyped endpoint must not earn metadata_verified")
    ok(refused is True, "an untyped endpoint must be counted as refused")

    # --- No invented launch line, ever ----------------------------------
    for srv in (one, three, no_remotes, loopback,
                fixture_server([{"type": "streamable-http", "url": "http://bad.example/mcp"}])):
        row, _, _ = build_row(srv)
        ok(row["command"] is None, "registry rows must never publish a command")
        ok(row["args"] is None, "registry rows must never publish args")
        ok(row["runtime"] is None, "registry rows must never publish a runtime hint")
        ok(row["stars"] is None, "registry rows must never publish a star figure")
        ok(row["source"] == producer.REGISTRY_SOURCE,
           "registry row lost its source id")

    # --- Dropped-count logging -----------------------------------------
    fixtures = [one, three, all_sse, no_remotes, loopback, no_type,
                fixture_server([{"type": "streamable-http", "url": "http://bad.example/mcp"}])]
    extra_rows = refused_rows = 0
    for srv in fixtures:
        _, extras, refused = build_row(srv)
        if extras:
            extra_rows += 1
        if refused:
            refused_rows += 1
    ok(extra_rows == 2, f"expected 2 rows with dropped extra endpoints, got {extra_rows}")
    ok(refused_rows == 2, f"expected 2 rows with a refused endpoint, got {refused_rows}")

    line = producer.registry_summary(7, 7, 1, 0, 0, 0, 0, 0, extra_rows, refused_rows)
    ok(f"{extra_rows} row(s) dropped extra remote endpoints" in line,
       f"dropped-endpoint count missing from the log line: {line}")
    ok(f"{refused_rows} row(s) declared an endpoint that was not publishable" in line,
       f"refused-endpoint count missing from the log line: {line}")

    print(f"REMOTE ENDPOINT TEST OK ({checks} checks)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
