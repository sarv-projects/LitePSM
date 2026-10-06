#!/usr/bin/env python3
"""Rewrite the committed catalog's MCP `transport` field to what is actually true.

Why this exists
---------------
`transport` was derived from an emoji in the upstream registry's README table
(a cloud emoji without a house meant "sse"). That is a publisher's formatting
choice, not a protocol fact, and it produced 1,665 of 4,079 MCP rows labelled
`sse` while carrying a local `command` + `args` and no URL of any kind. The
dataset has no URL field, so an `sse` row could not be installed as a remote
server even in principle; the label only ever misled.

What is true
------------
Every MCP row in this dataset has a local launch line (`command`, `args`) and no
remote endpoint. So the honest published fact is `stdio`: that is what the
installer registers and what the host will start. The upstream hint is kept
under `upstreamTransportHint`, explicitly marked as somebody else's claim, so
the information is not lost and the two can never be confused again.

This is a deterministic transform of the committed dataset, not a re-scrape, so
every other field stays byte-identical and the diff is reviewable. It is a
one-time migration: `scripts/build_full_catalog.py` was changed to emit the same
truthy values, and this script exists only to bring the already-committed dataset
in line without re-fetching upstream (which would churn unrelated rows).

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
    print(f"  transport now         : stdio for every row (each has a local command and no URL)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
