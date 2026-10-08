#!/usr/bin/env python3
"""Rewrite the committed catalog's MCP `transport` field — a one-time migration, now historical.

Why this existed
----------------
`transport` was derived from an emoji in the upstream registry's README table
(a cloud emoji without a house meant "sse"). That is a publisher's formatting
choice, not a protocol fact, and it produced 1,665 of 4,079 MCP rows labelled
`sse` while carrying a local `command` + `args` and no URL of any kind. When
this migration ran (commit `dbb1e60`, 2026-10-06) every MCP row in the dataset
did have a local launch line and no remote endpoint, so the honest published
fact was `stdio`: what the installer registered and what the host would start.
The upstream hint was kept under `upstreamTransportHint`, explicitly marked as
somebody else's claim, so the information was not lost and the two could never
be confused again.

What is true now — do not re-run this blindly
---------------------------------------------
* The dataset has a `url` field since the remote (URL) MCP work
  (`internal/catalogbuild/dataset.go:45`): the producer emits an endpoint
  selected from the registry's own `remotes[]` for a remote row, and that
  row's transport is `streamable-http` or `sse`, never `stdio`
  (`scripts/build_full_catalog.py:683-723`). This script forces
  `transport = "stdio"` on EVERY MCP row, so on such a dataset it would turn a
  remote row into a lie.
* The dataset as last built carries no launch line on any MCP row (all MCP rows
  are `discovery_only`; `command` and `transport` are null), so there is no row
  in it that this rewrite could make more honest either.
Run it only against a pre-`url` dataset that still carries the emoji-derived
`transport` claim — the situation it was written for.

This is a deterministic transform of the committed dataset, not a re-scrape, so
every other field stays byte-identical and the diff is reviewable. It was a
one-time migration: `scripts/build_full_catalog.py` was changed alongside it to
stop emitting the emoji-derived claim (it now writes `transport: null` plus the
`upstreamTransportHint` marker for awesome-list rows, and a real transport only
for the endpoint rows it publishes), and this script existed only to bring the
already-committed dataset in line without re-fetching upstream (which would
churn unrelated rows).

Usage: scripts/migrate_dataset_transport.py [dataset.json]
"""

import json
import pathlib
import sys


def main() -> int:
    path = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else "web/data/catalog.json")
    rows = json.loads(path.read_text())

    changed = 0
    labelled_sse = 0
    for row in rows:
        if row.get("kind") != "mcp":
            continue
        current = row.get("transport")
        if current == "sse":
            labelled_sse += 1
        if current != "stdio":
            row["transport"] = "stdio"
            changed += 1
        if current and current != "stdio":
            # Keep the publisher's claim, but under a name that cannot be read
            # as our own assertion.
            row["upstreamTransportHint"] = current
        else:
            row.pop("upstreamTransportHint", None)

    path.write_text(json.dumps(rows, indent=2, ensure_ascii=False) + "\n")
    print(f"mcp rows: {sum(1 for r in rows if r.get('kind') == 'mcp')}")
    print(f"  labelled sse upstream : {labelled_sse} (kept as upstreamTransportHint)")
    print(f"  transport rewritten   : {changed}")
    print(f"  transport now         : stdio for every row this run rewrote "
          f"(a pre-`url` dataset; see the module docstring before re-running)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
