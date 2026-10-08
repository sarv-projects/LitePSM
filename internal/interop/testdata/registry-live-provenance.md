# registry-live-* fixture provenance

Captured 2026-10-08T11:58:33Z (read-only HTTPS GETs) by the REGISTRY-SCHEMA lane.

| file | source |
|---|---|
| `registry-live-list-100.json` | verbatim response body of `https://registry.modelcontextprotocol.io/v0/servers?limit=100` |
| `registry-live-servers.json` | trimmed-but-verbatim subset: whole `server` documents byte-sliced out of that response (plus three from cursor pages of the same API, see below) |
| `registry-live-single.json` | one of those documents as the single-object wire shape |

Schema: `https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json`.

Documents and where they came from:
- `ac.inference.sh/mcp` `2.0.1` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100` (340 bytes)
- `ai.1325/mcp` `0.1.3` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100` (868 bytes)
- `ai.adramp/google-ads` `1.0.0` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100` (358 bytes)
- `ai.adtest/adtest-mcp` `1.1.0` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100` (980 bytes)
- `ai.agent-bev/bev-door` `0.2.5` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100` (730 bytes)
- `ai.agentdm/agentdm` `2.0.0` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100` (884 bytes)
- `ai.agenticaffiliate/affiliate-networks-mcp` `0.19.0` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100&cursor=ai.agentdm%2Fagentdm%3A2.0.0` (661 bytes)
- `ai.apithreshold/apithreshold` `0.1.0` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100&cursor=ai.analyticslegends%2Fsap-analytics%3A1.0.5` (1120 bytes)
- `ai.aquex/stage1` `0.5.1` — `https://registry.modelcontextprotocol.io/v0/servers?limit=100&cursor=ai.analyticslegends%2Fsap-analytics%3A1.0.5` (686 bytes)

Why a subset rather than the whole response: the list response is a
paginated collection of *versioned* documents — the captured response
holds 100 entries for 50 distinct server names (several versions per
name). Importing it as one server.json document would collide on the
listing id, so `ParseServerJSON` refuses the `servers` envelope with an
explicit message (see `TestServerJSONListResponseRefusedAccurately`)
and this fixture pins the document shape instead.

The registry changes constantly; these bytes are a dated snapshot, not
a stable contract. Refresh by re-running the capture, never by
hand-editing: the value of the fixture is that the bytes are upstream's.

No environment or header VALUE appears in any captured document (the
capture refuses to write one); only names and descriptions are upstream.
