// Package discover turns an installed MCP server into capability rows, and
// invokes a discovered capability.
//
// # Why this exists
//
// Installing a server only writes a config entry. The catalog then knows nothing
// about what that server can DO, so an agent has no way to discover its tools and
// no way to call them. The pieces for closing that gap were all present and
// unwired: `mcpclient` could speak to a server but had no production importer,
// `ProviderRecord`/`CapabilityRecord` and their state writers existed with no
// caller, and `provider.invoke`, `capabilities.search` and
// `capabilities.describe` answered -32601 with the reason "no capability rows are
// persisted to resolve a capabilityId against".
//
// # What it deliberately does not do
//
// It does not persist invocations. An invocation whose process died with the
// daemon is worse than a forgotten one, so the asynchronous invocation registry
// stays where ARCH/34 puts it (DESIGNED) and `invocation.get` /
// `invocation.cancel` keep failing closed with that reason. `provider.invoke`
// itself is synchronous, which is what ARCH/06 §4 and the Bridge tool contract
// (`invoke_capability` -> `{output, isError}`) already specify.
package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/mcpclient"
	"github.com/sarv-projects/litespm/internal/state"
)

// ProviderSpec is one installed MCP server to probe or call.
type ProviderSpec struct {
	// InstallID ties the provider and its capabilities to an install record, so
	// a capability id can always be traced back to what the user installed.
	InstallID string
	// ComponentName is the name the server is registered under.
	ComponentName string
	Command       string
	Args          []string
	Env           map[string]string
	Transport     string
}

// DiscoveredProvider is what a probe found.
type DiscoveredProvider struct {
	ProviderID   string
	InstallID    string
	Capabilities []domain.CapabilityRecord
}

// ProviderIDFor derives a stable provider id from the install and component, so
// re-probing updates rows instead of accumulating duplicates.
func ProviderIDFor(installID, componentName string) string {
	return fmt.Sprintf("prov_%s_%s", sanitizeIDPart(installID), sanitizeIDPart(componentName))
}

func sanitizeIDPart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// Discover probes a running server and persists its provider and capability rows.
//
// A probe failure is returned, not swallowed: the caller decides whether an
// unprobeable server should fail the surrounding operation. The rows themselves
// are written only after a complete, successful listing, so a server that dies
// mid-probe cannot leave a half-discovered capability set behind.
func Discover(ctx context.Context, db *state.DB, spec ProviderSpec) (*DiscoveredProvider, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("provider %q has no command to run", spec.ComponentName)
	}

	session, cleanup, err := dial(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	tools, err := mcpclient.ProbeProvider(ctx, session)
	if err != nil {
		return nil, err
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("provider %q exposed no tools", spec.ComponentName)
	}

	providerID := ProviderIDFor(spec.InstallID, spec.ComponentName)
	now := time.Now().UTC()

	argsJSON, _ := json.Marshal(spec.Args)
	envJSON := ""
	if len(spec.Env) > 0 {
		if b, err := json.Marshal(spec.Env); err == nil {
			envJSON = string(b)
		}
	}
	if err := db.SaveProvider(ctx, &domain.ProviderRecord{
		ProviderID:    providerID,
		InstallID:     spec.InstallID,
		ComponentName: spec.ComponentName,
		Transport:     transportOrDefault(spec.Transport),
		Command:       spec.Command,
		ArgsJSON:      string(argsJSON),
		EnvJSON:       envJSON,
		Status:        "active",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return nil, fmt.Errorf("record provider: %w", err)
	}

	out := &DiscoveredProvider{ProviderID: providerID, InstallID: spec.InstallID}
	for _, tool := range tools {
		record := domain.CapabilityRecord{
			CapabilityID:      string(domain.NewCapabilityID(domain.InstallID(spec.InstallID), spec.ComponentName, tool.NativeName)),
			ProviderID:        providerID,
			Name:              tool.NativeName,
			Description:       tool.Description,
			InputSchemaJSON:   string(tool.InputSchema),
			SchemaFingerprint: tool.SchemaFingerprint,
			DiscoveredAt:      now,
			UpdatedAt:         now,
		}
		if err := db.SaveCapability(ctx, &record); err != nil {
			return nil, fmt.Errorf("record capability %q: %w", tool.NativeName, err)
		}
		out.Capabilities = append(out.Capabilities, record)
	}
	return out, nil
}

// InvokeResult is a tool call's outcome, shaped like the Bridge contract.
type InvokeResult struct {
	Output  string `json:"output"`
	IsError bool   `json:"isError"`
}

// Invoke calls one discovered capability.
//
// It resolves the capability id, re-reads the provider row for the command, and
// calls the tool. The schema fingerprint is re-checked first: if the server's
// tool schema changed since discovery, the call is refused rather than made
// against arguments nobody validated. That is the drift check
// `mcpclient.DetectDrift` exists for, applied as a gate instead of a report.
func Invoke(ctx context.Context, db *state.DB, capabilityID string, arguments json.RawMessage) (*InvokeResult, error) {
	_, installID, componentName, toolName, err := domain.ParseCapabilityID(capabilityID)
	if err != nil {
		return nil, err
	}
	providerID := ProviderIDFor(string(installID), componentName)

	provider, err := db.GetProvider(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("no provider is registered for capability %q (provider %s): %w", capabilityID, providerID, err)
	}
	capability, err := db.GetCapability(ctx, capabilityID)
	if err != nil {
		return nil, fmt.Errorf("no discovered capability %q: %w", capabilityID, err)
	}

	// The capability id is the authority on which component this is: the
	// providers table stores a component_id, not the display name.
	component := componentName
	if component == "" {
		component = provider.ComponentName
	}
	spec := ProviderSpec{
		InstallID:     provider.InstallID,
		ComponentName: component,
		Command:       provider.Command,
		Transport:     provider.Transport,
	}
	if provider.ArgsJSON != "" {
		if err := json.Unmarshal([]byte(provider.ArgsJSON), &spec.Args); err != nil {
			return nil, fmt.Errorf("provider %s has an unreadable argument list: %w", providerID, err)
		}
	}
	if provider.EnvJSON != "" {
		if err := json.Unmarshal([]byte(provider.EnvJSON), &spec.Env); err != nil {
			return nil, fmt.Errorf("provider %s has an unreadable environment: %w", providerID, err)
		}
	}
	if spec.Command == "" {
		return nil, fmt.Errorf("provider %s has no command recorded; re-run discovery", providerID)
	}

	session, cleanup, err := dial(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Re-probe and compare the schema before calling: an argument shape that
	// changed under us is a hard stop, not a warning.
	current, err := mcpclient.ProbeProvider(ctx, session)
	if err != nil {
		return nil, err
	}
	for _, tool := range current {
		if tool.NativeName != toolName {
			continue
		}
		if tool.SchemaFingerprint != capability.SchemaFingerprint {
			return nil, fmt.Errorf("tool %q changed its input schema since discovery (recorded %s, now %s); "+
				"re-run discovery before invoking it", toolName, capability.SchemaFingerprint, tool.SchemaFingerprint)
		}
		if len(arguments) == 0 {
			arguments = json.RawMessage("{}")
		}
		result, err := session.CallTool(ctx, toolName, arguments)
		if err != nil {
			return nil, fmt.Errorf("tool %q failed: %w", toolName, err)
		}
		return &InvokeResult{Output: flattenContent(result.Content), IsError: result.IsError}, nil
	}
	return nil, fmt.Errorf("provider %s no longer exposes a tool named %q", providerID, toolName)
}

// dial spawns the server and completes an MCP session over its stdio pipes.
// flattenContent renders a tool result's content parts as the single string the
// Bridge contract expects. Non-text parts are named rather than dropped, so a
// caller is never told a call produced nothing when it produced an image.
func flattenContent(parts []mcpclient.ToolContent) string {
	var b strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "", "text":
			b.WriteString(part.Text)
		case "image":
			fmt.Fprintf(&b, "[image: %d base64 chars]", len(part.Data))
		case "resource":
			fmt.Fprintf(&b, "[resource: %s]", part.Text)
		default:
			fmt.Fprintf(&b, "[%s content omitted]", part.Type)
		}
	}
	return b.String()
}

func dial(ctx context.Context, spec ProviderSpec) (mcpclient.ClientSession, func(), error) {
	if !isStdioTransport(spec.Transport) {
		// The catalog publishes no remote endpoint for any row, so this is
		// unreachable today; it fails loudly rather than pretending to dial.
		return nil, nil, fmt.Errorf("provider %q uses transport %q, which is not supported yet",
			spec.ComponentName, spec.Transport)
	}
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	if len(spec.Env) > 0 {
		env := make([]string, 0, len(spec.Env))
		for k, v := range spec.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = append(cmd.Environ(), env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", spec.Command, err)
	}
	kill := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	session, err := mcpclient.ConnectStdio(ctx, stdout, stdin)
	if err != nil {
		kill()
		return nil, nil, fmt.Errorf("connect to %s: %w", spec.Command, err)
	}
	cleanup := func() {
		_ = session.CloseSession()
		kill()
	}
	return session, cleanup, nil
}

func transportOrDefault(t string) string {
	if strings.TrimSpace(t) == "" {
		return "stdio"
	}
	return t
}

// isStdioTransport accepts both vocabularies in play: the transport names the
// catalog and domain model speak ("stdio"), and the CHECK-constrained enum the
// providers table stores ("local-stdio"). Treating them as different transports
// would make every discovered provider look remote.
func isStdioTransport(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "stdio", "local-stdio":
		return true
	default:
		return false
	}
}
