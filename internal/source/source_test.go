package source

import (
	"context"
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/domain"
)

func TestMCPRegistryAdapterIngest(t *testing.T) {
	mockFeed := []byte(`[
		{
			"name": "@modelcontextprotocol/server-postgres",
			"title": "PostgreSQL MCP Server",
			"description": "Enables querying and inspecting PostgreSQL databases",
			"repository": "https://github.com/modelcontextprotocol/servers",
			"categories": ["database", "sql"],
			"publisher": {
				"name": "Anthropic",
				"url": "https://anthropic.com"
			},
			"packages": [
				{
					"registryType": "npm",
					"name": "@modelcontextprotocol/server-postgres",
					"version": "1.4.0",
					"digest": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					"runtime": "node",
					"transport": "stdio",
					"command": "node",
					"args": ["dist/index.js"]
				}
			]
		},
		{
			"name": "github-remote",
			"title": "GitHub Hosted MCP Server",
			"description": "Access GitHub repos, PRs, and issues over Streamable HTTP",
			"categories": ["developer-tools"],
			"remotes": [
				{
					"url": "https://mcp.github.com/v1",
					"transport": "http",
					"authType": "oauth2"
				}
			]
		}
	]`)

	adapter, err := NewMCPRegistryAdapter("builtin:mcp-registry", mockFeed)
	if err != nil {
		t.Fatalf("failed to create MCP registry adapter: %v", err)
	}

	res, err := adapter.Ingest(context.Background(), "snap_001")
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	if res.ItemCount != 2 {
		t.Fatalf("expected 2 listings, got %d", res.ItemCount)
	}
	if len(res.Listings) != 2 || len(res.Versions) != 2 {
		t.Fatalf("mismatch in listings/versions count: %d listings, %d versions", len(res.Listings), len(res.Versions))
	}

	// Verify listing 1
	l1 := res.Listings[0]
	if !strings.HasPrefix(l1.ID, "mcp:builtin:mcp-registry:") {
		t.Fatalf("expected canonical listing ID prefix, got %s", l1.ID)
	}
	if l1.Kind != domain.KindMCP {
		t.Fatalf("expected kind mcp, got %s", l1.Kind)
	}
	if l1.PublisherClaim.Name != "Anthropic" {
		t.Fatalf("expected publisher Anthropic, got %s", l1.PublisherClaim.Name)
	}

	// Verify version 1
	v1 := res.Versions[0]
	if v1.Version != "1.4.0" {
		t.Fatalf("expected version 1.4.0, got %s", v1.Version)
	}
	if len(v1.Artifacts) != 1 || v1.Artifacts[0].Type != domain.ArtifactNPM {
		t.Fatalf("expected 1 npm artifact, got %+v", v1.Artifacts)
	}
	if len(v1.Components) != 1 || v1.Components[0].Runtime.Type != "stdio" {
		t.Fatalf("expected stdio runtime, got %+v", v1.Components[0].Runtime)
	}

	// Verify remote server
	l2 := res.Listings[1]
	v2 := res.Versions[1]
	if v2.Components[0].Runtime.Type != "http" || v2.Components[0].Runtime.Endpoint != "https://mcp.github.com/v1" {
		t.Fatalf("expected http remote endpoint, got %+v", v2.Components[0].Runtime)
	}
	if l2.Title != "GitHub Hosted MCP Server" {
		t.Fatalf("expected title match, got %s", l2.Title)
	}
}

func TestAgentSkillsAdapterIngest(t *testing.T) {
	mockSkill := []byte(`---
name: git-commit-workflow
title: Semantic Git Commit Workflow
description: Automates semantic git commit creation, diff inspection, and branch hygiene
author: agentskills.io
version: 1.2.0
categories:
  - version-control
  - git
triggers:
  - /commit
  - git commit
tools:
  - git:status
  - git:diff
---

# Semantic Git Workflow

Follow these step-by-step instructions to create clean semantic commits:
1. Inspect git status
2. Review staged diffs
3. Formulate atomic commit message
`)

	docs := map[string][]byte{
		"git-commit-workflow.md": mockSkill,
	}

	adapter, err := NewAgentSkillsAdapter("builtin:agent-skills", docs)
	if err != nil {
		t.Fatalf("failed to create agent skills adapter: %v", err)
	}

	res, err := adapter.Ingest(context.Background(), "snap_002")
	if err != nil {
		t.Fatalf("skills Ingest failed: %v", err)
	}

	if res.ItemCount != 1 {
		t.Fatalf("expected 1 skill listing, got %d", res.ItemCount)
	}

	l := res.Listings[0]
	if l.Kind != domain.KindSkill {
		t.Fatalf("expected kind skill, got %s", l.Kind)
	}
	if l.ID != "skill:builtin:agent-skills:git-commit-workflow" {
		t.Fatalf("unexpected skill ID: %s", l.ID)
	}
	if l.Title != "Semantic Git Commit Workflow" {
		t.Fatalf("unexpected title: %s", l.Title)
	}

	v := res.Versions[0]
	if v.Version != "1.2.0" {
		t.Fatalf("expected version 1.2.0, got %s", v.Version)
	}
	if len(v.Components) != 1 || v.Components[0].Kind != domain.ComponentSkill {
		t.Fatalf("expected component skill, got %+v", v.Components)
	}
}
