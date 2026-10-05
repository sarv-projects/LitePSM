package skills

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
)

// SkillPackage represents a parsed SKILL.md instruction workflow.
type SkillPackage struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	License       string   `json:"license,omitempty"`
	Version       string   `json:"version,omitempty"`
	Author        string   `json:"author,omitempty"`
	Triggers      []string `json:"triggers,omitempty"`
	ToolsRequired []string `json:"toolsRequired,omitempty"`
	Instructions  string   `json:"instructions"`
	TreePath      string   `json:"treePath,omitempty"`
	RawContent    string   `json:"rawContent,omitempty"`
}

// ParseSkillMD parses the contents of a SKILL.md document.
// It parses the YAML-like frontmatter enclosed in '---' delimiters and extracts the body instructions.
func ParseSkillMD(content []byte) (*SkillPackage, error) {
	raw := string(content)
	scanner := bufio.NewScanner(bytes.NewReader(content))

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read skill content: %w", err)
	}

	if len(lines) == 0 {
		return nil, domain.ErrInvalidIdentifier("SKILL.md", "non-empty SKILL.md file")
	}

	pkg := &SkillPackage{
		RawContent: raw,
	}

	inFrontmatter := false
	frontmatterDone := false
	var bodyLines []string
	var currentListKey string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if i == 0 && trimmed == "---" {
			inFrontmatter = true
			continue
		}

		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
				frontmatterDone = true
				continue
			}

			// Check for list items
			if strings.HasPrefix(trimmed, "- ") && currentListKey != "" {
				itemVal := strings.TrimSpace(strings.Trim(strings.TrimPrefix(trimmed, "- "), "\"'`"))
				switch currentListKey {
				case "triggers":
					pkg.Triggers = append(pkg.Triggers, itemVal)
				case "tools_required", "tools":
					pkg.ToolsRequired = append(pkg.ToolsRequired, itemVal)
				}
				continue
			}

			colonIdx := strings.Index(trimmed, ":")
			if colonIdx != -1 {
				key := strings.ToLower(strings.TrimSpace(trimmed[:colonIdx]))
				val := strings.TrimSpace(trimmed[colonIdx+1:])
				val = strings.Trim(val, "\"'`")

				switch key {
				case "name":
					pkg.Name = val
					currentListKey = ""
				case "description":
					pkg.Description = val
					currentListKey = ""
				case "license":
					pkg.License = val
					currentListKey = ""
				case "version":
					pkg.Version = val
					currentListKey = ""
				case "author":
					pkg.Author = val
					currentListKey = ""
				case "triggers":
					currentListKey = "triggers"
					if val != "" && !strings.HasPrefix(val, "[") {
						pkg.Triggers = append(pkg.Triggers, val)
					}
				case "tools_required", "tools":
					currentListKey = "tools_required"
					if val != "" && !strings.HasPrefix(val, "[") {
						pkg.ToolsRequired = append(pkg.ToolsRequired, val)
					}
				default:
					currentListKey = ""
				}
			}
			continue
		}

		if frontmatterDone || i > 0 {
			bodyLines = append(bodyLines, line)
		}
	}

	pkg.Instructions = strings.TrimSpace(strings.Join(bodyLines, "\n"))

	if pkg.Name == "" {
		// Fallback: derive name from first header or default
		for _, bl := range bodyLines {
			tb := strings.TrimSpace(bl)
			if strings.HasPrefix(tb, "# ") {
				pkg.Name = strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(tb, "# "), " ", "-"))
				break
			}
		}
		if pkg.Name == "" {
			pkg.Name = "unnamed-skill"
		}
	}

	return pkg, nil
}

// LoadSkillFromDirectory loads and parses SKILL.md from a target directory.
func LoadSkillFromDirectory(dirPath string) (*SkillPackage, error) {
	skillFile := filepath.Join(dirPath, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		// Check lowercase skill.md fallback
		skillFile = filepath.Join(dirPath, "skill.md")
		data, err = os.ReadFile(skillFile)
		if err != nil {
			return nil, fmt.Errorf("SKILL.md not found in directory %s: %w", dirPath, err)
		}
	}

	pkg, err := ParseSkillMD(data)
	if err != nil {
		return nil, err
	}
	pkg.TreePath = dirPath
	return pkg, nil
}

// RenderProgressiveIndex formats an ultra-compact discovery table for AI system prompts (Level 1).
func RenderProgressiveIndex(skills []*SkillPackage) string {
	if len(skills) == 0 {
		return "No active agent skills installed."
	}

	var sb strings.Builder
	sb.WriteString("| Skill Name | Description | Triggers | Required Tools |\n")
	sb.WriteString("|---|---|---|---|\n")

	for _, s := range skills {
		triggersStr := "None"
		if len(s.Triggers) > 0 {
			triggersStr = fmt.Sprintf("`%s`", strings.Join(s.Triggers, "`, `"))
		}

		toolsStr := "None"
		if len(s.ToolsRequired) > 0 {
			toolsStr = fmt.Sprintf("`%s`", strings.Join(s.ToolsRequired, "`, `"))
		}

		desc := s.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}

		sb.WriteString(fmt.Sprintf("| **%s** | %s | %s | %s |\n", s.Name, desc, triggersStr, toolsStr))
	}

	return sb.String()
}

// RenderFullPrompt formats the complete instructional body when a skill is activated (Level 2).
func RenderFullPrompt(skill *SkillPackage) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Skill: %s\n", skill.Name))
	if skill.Description != "" {
		sb.WriteString(fmt.Sprintf("> %s\n\n", skill.Description))
	}
	if len(skill.ToolsRequired) > 0 {
		sb.WriteString(fmt.Sprintf("**Required MCP Tools:** `%s`\n\n", strings.Join(skill.ToolsRequired, "`, `")))
	}
	sb.WriteString("### Instructions\n")
	sb.WriteString(skill.Instructions)
	sb.WriteString("\n")
	return sb.String()
}
