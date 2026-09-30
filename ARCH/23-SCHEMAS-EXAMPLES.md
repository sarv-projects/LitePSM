# Schemas & Validation Examples

## 1. Canonical Schema Registry

All public contracts and local plans are validated against strict JSON Schema (Draft 2020-12) specifications located in `schemas/`:

1.  `schemas/listing.schema.json`: Catalog discovery listings.
2.  `schemas/version.schema.json`: Pinned version and artifact references.
3.  `schemas/source.schema.json`: Upstream registry and marketplace configurations.
4.  `schemas/install-plan.schema.json`: Cryptographic plan contracts.
5.  `schemas/catalog-release.schema.json`: Release pointers and file manifests.
6.  `schemas/errors.schema.json`: Machine-readable error envelopes.

---

## 2. Specification: `install-plan.schema.json`

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://litepsm.dev/schemas/v1/install-plan.schema.json",
  "title": "InstallPlan",
  "type": "object",
  "required": [
    "schemaVersion", "planId", "planHash", "createdAt", "expiresAt",
    "catalogReleaseId", "request", "resolved", "effects", "preconditions", "approval"
  ],
  "properties": {
    "schemaVersion": { "type": "integer", "const": 2 },
    "planId": { "type": "string", "pattern": "^plan_[0-9A-Za-z]{26}$" },
    "planHash": { "type": "string", "pattern": "^sha256:[a-f0-9]{64}$" },
    "createdAt": { "type": "string", "format": "date-time" },
    "expiresAt": { "type": "string", "format": "date-time" },
    "catalogReleaseId": { "type": "string" },
    "sourceSnapshots": { "type": "array", "items": { "type": "string" } },
    "request": {
      "type": "object",
      "required": ["listingId", "targetScope"],
      "properties": {
        "listingId": { "type": "string" },
        "requestedVersion": { "type": "string" },
        "selectedComponents": { "type": "array", "items": { "type": "string" } },
        "targetScope": { "type": "string", "enum": ["user", "project"] },
        "targetHost": { "type": "string" }
      }
    },
    "resolved": {
      "type": "object",
      "required": ["version", "artifacts"],
      "properties": {
        "version": { "type": "string" },
        "immutableRefs": { "type": "array", "items": { "type": "string" } },
        "artifacts": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["artifactId", "type", "locator"],
            "properties": {
              "artifactId": { "type": "string" },
              "type": { "type": "string" },
              "locator": { "type": "string" },
              "digest": { "type": "string", "pattern": "^sha256:[a-f0-9]{64}$" },
              "size": { "type": "integer" }
            }
          }
        }
      }
    },
    "effects": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["type"],
        "properties": {
          "type": { "type": "string" },
          "target": { "type": "string" }
        }
      }
    },
    "preconditions": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["type"],
        "properties": {
          "type": { "type": "string" },
          "target": { "type": "string" },
          "expected": { "type": "string" }
        }
      }
    },
    "approval": {
      "type": "object",
      "required": ["required"],
      "properties": {
        "required": { "type": "bool" },
        "reasonCodes": { "type": "array", "items": { "type": "string" } },
        "minimumChannel": { "type": "string" }
      }
    }
  },
  "additionalProperties": false
}
```

---

## 3. Concrete Example Fixtures

### 3.1 Valid Listing Fixture
```json
{
  "schemaVersion": 1,
  "id": "mcp:builtin:mcp-registry:postgres",
  "kind": "mcp",
  "name": "PostgreSQL MCP Server",
  "summary": "Read-only and read-write SQL access to PostgreSQL databases via MCP.",
  "categories": ["database", "developer-tools"],
  "keywords": ["postgres", "sql", "mcp"],
  "publisherClaim": {
    "name": "Model Context Protocol",
    "url": "https://github.com/modelcontextprotocol"
  },
  "source": {
    "sourceId": "builtin:mcp-registry",
    "upstreamId": "postgres",
    "sourceType": "mcp-registry",
    "manifestFormat": "server.json"
  },
  "versions": [
    {
      "version": "1.4.0",
      "immutableRef": "git:7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c",
      "publishedAt": "2026-09-20T10:00:00Z"
    }
  ],
  "componentsSummary": [
    { "kind": "mcp-provider", "name": "server", "supportedByLitePSM": "yes" }
  ],
  "requirementsSummary": ["executable:node >= 18.0.0"],
  "compatibilitySummary": [
    { "host": "claude-code", "supported": "yes" },
    { "host": "codex", "supported": "yes" }
  ],
  "verificationSummary": {
    "sourceVerified": true,
    "formatCompatible": true,
    "tested": true,
    "lastTestedAt": "2026-09-30T10:00:00Z"
  },
  "provenance": {
    "firstSeenAt": "2026-09-01T00:00:00Z",
    "lastSeenAt": "2026-09-30T12:00:00Z",
    "fetchedAt": "2026-09-30T12:00:00Z"
  },
  "status": "active"
}
```
