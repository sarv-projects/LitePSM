package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSkillMD = `---
name: pr-review-assistant
description: Automated GitHub PR code review and quality checks.
license: MIT
version: 1.2.0
author: LitePSM Team
triggers:
  - "/pr review"
  - "/pr audit"
tools_required:
  - github.get_pull_request
  - github.create_review_comment
---
# PR Review Assistant Instructions

When invoked with a PR URL or number:
1. Retrieve PR diff using github tool.
2. Inspect changed files for security issues and test coverage.
3. Post constructive inline review comments.
`

func TestParseSkillMD_Valid(t *testing.T) {
	pkg, err := ParseSkillMD([]byte(sampleSkillMD))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if pkg.Name != "pr-review-assistant" {
		t.Errorf("expected name 'pr-review-assistant', got %q", pkg.Name)
	}
	if pkg.Description != "Automated GitHub PR code review and quality checks." {
		t.Errorf("expected matching description, got %q", pkg.Description)
	}
	if pkg.License != "MIT" {
		t.Errorf("expected license MIT, got %q", pkg.License)
	}
	if pkg.Version != "1.2.0" {
		t.Errorf("expected version 1.2.0, got %q", pkg.Version)
	}

	if len(pkg.Triggers) != 2 || pkg.Triggers[0] != "/pr review" || pkg.Triggers[1] != "/pr audit" {
		t.Errorf("unexpected triggers: %v", pkg.Triggers)
	}

	if len(pkg.ToolsRequired) != 2 || pkg.ToolsRequired[0] != "github.get_pull_request" {
		t.Errorf("unexpected tools: %v", pkg.ToolsRequired)
	}

	if !strings.Contains(pkg.Instructions, "PR Review Assistant Instructions") {
		t.Errorf("instructions missing expected content: %s", pkg.Instructions)
	}
}

func TestLoadSkillFromDirectory(t *testing.T) {
	tempDir := t.TempDir()
	skillPath := filepath.Join(tempDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(sampleSkillMD), 0644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadSkillFromDirectory(tempDir)
	if err != nil {
		t.Fatalf("failed to load skill from directory: %v", err)
	}

	if pkg.Name != "pr-review-assistant" {
		t.Errorf("unexpected loaded skill name: %s", pkg.Name)
	}
	if pkg.TreePath != tempDir {
		t.Errorf("unexpected tree path: %s", pkg.TreePath)
	}
}

func TestRenderProgressiveIndexAndPrompt(t *testing.T) {
	pkg, err := ParseSkillMD([]byte(sampleSkillMD))
	if err != nil {
		t.Fatal(err)
	}

	indexTable := RenderProgressiveIndex([]*SkillPackage{pkg})
	if !strings.Contains(indexTable, "pr-review-assistant") || !strings.Contains(indexTable, "/pr review") {
		t.Errorf("index table missing skill details: %s", indexTable)
	}

	prompt := RenderFullPrompt(pkg)
	if !strings.Contains(prompt, "## Skill: pr-review-assistant") || !strings.Contains(prompt, "github.get_pull_request") {
		t.Errorf("rendered prompt missing expected headers: %s", prompt)
	}
}
