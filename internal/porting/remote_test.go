package porting

// remote_test.go — B1 acceptance for the copy/porting half of remote (URL)
// MCP entries (research PART 4 Phase 2 Step 2.6):
//
//   - the IR carries endpoint + transport through both directions
//     (SourceMCP → NormalizeMCP, and HostServerEntry → IRFromHostEntry);
//   - Fingerprint includes them, so a remote entry and a same-name stdio
//     entry never compare "identical";
//   - a remote row against a target with no compatible remote spec is
//     reported "not copyable" with its reason — never dropped, never
//     rewritten as stdio.

import (
	"strings"
	"testing"

	"github.com/sarv-projects/litespm/internal/host"
)

const remoteURL = "https://mcp.example.com/mcp"

func capableRemoteSpec() *host.RemoteEntrySpec {
	return &host.RemoteEntrySpec{
		URLKey: "url", TypeKey: "type", TypeValue: "http", SSEValue: "sse",
		Transports: []string{host.TransportStreamableHTTP, host.TransportSSE},
	}
}

func streamableOnlyRemoteSpec() *host.RemoteEntrySpec {
	return &host.RemoteEntrySpec{
		URLKey: "url", Transports: []string{host.TransportStreamableHTTP},
	}
}

// remoteSource is a source machine holding one remote entry, expressed the way
// readCopySource records it (host discriminator included).
func remoteSource(transport string) *Source {
	return &Source{
		From:       "claude-code",
		Display:    "Claude Code",
		EntryShape: string(host.ShapeObject),
		MCP: []SourceMCP{{
			Name:      "demo",
			Endpoint:  remoteURL,
			Transport: transport,
		}},
	}
}

func remoteTarget(to string, remote *host.RemoteEntrySpec) *TargetCaps {
	return &TargetCaps{
		To:             to,
		Display:        to,
		HasEntrySpec:   true,
		EntryShape:     string(host.ShapeObject),
		Remote:         remote,
		ExistingMCP:    map[string]host.HostServerEntry{},
		ExistingSkills: map[string]TargetSkill{},
	}
}

// TestRemoteIRRoundTrip preserves endpoint and transport through both
// directions of the IR, including the host-vocabulary spellings a config read
// returns ("http", "streamableHttp", "remote").
func TestRemoteIRRoundTrip(t *testing.T) {
	for _, hostTransport := range []string{"", "http", "streamableHttp", "remote", "streamable-http"} {
		entry, _ := NormalizeMCP(SourceMCP{Name: "demo", Endpoint: remoteURL, Transport: hostTransport})
		if entry.Endpoint != remoteURL {
			t.Errorf("transport %q: endpoint lost = %q", hostTransport, entry.Endpoint)
		}
		if entry.Command != "" {
			t.Errorf("transport %q: remote entry grew a command %q", hostTransport, entry.Command)
		}
		if entry.Transport != host.TransportStreamableHTTP {
			t.Errorf("transport %q: NormalizeMCP gave %q, want %q", hostTransport, entry.Transport, host.TransportStreamableHTTP)
		}
		// The L1 read-back of that same entry fingerprints identically, so a
		// write verifies against the plan it came from.
		readBack := IRFromHostEntry(host.HostServerEntry{
			Name: "demo", Endpoint: remoteURL, Transport: hostTransport,
		})
		if Fingerprint(entry) != Fingerprint(readBack) {
			t.Errorf("transport %q: plan fingerprint %s != read-back %s",
				hostTransport, Fingerprint(entry), Fingerprint(readBack))
		}
	}

	// An sse discriminator stays sse through both directions.
	src, _ := NormalizeMCP(SourceMCP{Name: "demo", Endpoint: remoteURL, Transport: "sse"})
	if src.Transport != host.TransportSSE {
		t.Errorf("sse transport normalised to %q", src.Transport)
	}
	if Fingerprint(src) != Fingerprint(IRFromHostEntry(host.HostServerEntry{
		Name: "demo", Endpoint: remoteURL, Transport: "sse",
	})) {
		t.Error("sse read-back fingerprint differs from the plan")
	}

	// A stdio entry never gains a transport.
	std, _ := NormalizeMCP(SourceMCP{Name: "demo", Command: "npx", Args: []string{"-y", "demo"}})
	if std.Endpoint != "" || std.Transport != "" {
		t.Errorf("stdio entry gained remote fields: %+v", std)
	}
}

// TestFingerprintSeparatesRemoteFromStdio is the identity rule: without the
// endpoint a remote entry and a stdio entry registered under the same name
// would compare identical while describing opposite transports.
func TestFingerprintSeparatesRemoteFromStdio(t *testing.T) {
	stdio, _ := NormalizeMCP(SourceMCP{Name: "demo", Command: "npx", Args: []string{"-y", "demo"}})
	remote, _ := NormalizeMCP(SourceMCP{Name: "demo", Endpoint: remoteURL, Transport: host.TransportStreamableHTTP})
	other, _ := NormalizeMCP(SourceMCP{Name: "demo", Endpoint: "https://other.example/mcp", Transport: host.TransportStreamableHTTP})
	remoteSSE, _ := NormalizeMCP(SourceMCP{Name: "demo", Endpoint: remoteURL, Transport: host.TransportSSE})
	sameAgain, _ := NormalizeMCP(SourceMCP{Name: "demo", Endpoint: remoteURL, Transport: "http"})

	if Fingerprint(stdio) == Fingerprint(remote) {
		t.Error("a remote entry and a stdio entry with the same name share a fingerprint")
	}
	if Fingerprint(remote) == Fingerprint(other) {
		t.Error("two different endpoints share a fingerprint")
	}
	if Fingerprint(remote) == Fingerprint(remoteSSE) {
		t.Error("streamable-http and sse of the same URL share a fingerprint")
	}
	if Fingerprint(remote) != Fingerprint(sameAgain) {
		t.Error("the same fact in host vocabulary ('http') fingerprints differently")
	}
	// Stdio fingerprints must be unchanged by the new fields (omitempty).
	payload := Fingerprint(stdio)
	for _, leaked := range []string{"endpoint", remoteURL} {
		if strings.Contains(payload, leaked) {
			t.Errorf("stdio fingerprint leaked %q: %s", leaked, payload)
		}
	}
}

// TestPlanRemoteToIncapableTargetIsNotCopyable: nil Remote is the fail-closed
// answer — the row is reported with the reason, and Writable() never includes
// it, so apply writes nothing.
func TestPlanRemoteToIncapableTargetIsNotCopyable(t *testing.T) {
	plan, err := BuildPlan(remoteSource("http"), remoteTarget("roo", nil), CopyOptions{From: "claude-code", To: "roo"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Found[KindMCP] != 1 {
		t.Fatalf("Found[mcp] = %d, want 1", plan.Found[KindMCP])
	}
	item := plan.Items[0]
	if item.Action != ActionUnsupported {
		t.Fatalf("action = %q, want unsupported (%+v)", item.Action, item)
	}
	if !strings.Contains(item.Reason, "not copyable") || !strings.Contains(item.Reason, "roo") {
		t.Errorf("refusal must say the entry is not copyable and name the target: %q", item.Reason)
	}
	if !strings.Contains(item.Reason, "LPSM-COPY-002") {
		t.Errorf("refusal must carry the typed code: %q", item.Reason)
	}
	if len(plan.Writable()) != 0 {
		t.Errorf("an unsupported remote row must not be writable: %+v", plan.Writable())
	}
}

// TestPlanRemoteTransportNotSupportedByTarget: the target has a remote spec
// but does not document this transport — still an explicit refusal, not a
// bare-url guess.
func TestPlanRemoteTransportNotSupportedByTarget(t *testing.T) {
	plan, err := BuildPlan(remoteSource("sse"), remoteTarget("zed", streamableOnlyRemoteSpec()),
		CopyOptions{From: "claude-code", To: "zed"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	item := plan.Items[0]
	if item.Action != ActionUnsupported {
		t.Fatalf("action = %q, want unsupported (%+v)", item.Action, item)
	}
	if !strings.Contains(item.Reason, host.TransportSSE) || !strings.Contains(item.Reason, "not copyable") {
		t.Errorf("refusal must name the unsupported transport: %q", item.Reason)
	}
	if len(plan.Writable()) != 0 {
		t.Errorf("nothing must be writable: %+v", plan.Writable())
	}
}

// TestPlanRemoteBetweenCapableHosts: the normal success path — a direct row
// (a remote entry has no argv shape to translate), L1 verification only, and
// the endpoint preserved in the item IR.
func TestPlanRemoteBetweenCapableHosts(t *testing.T) {
	src := remoteSource("streamable-http")
	src.EntryShape = string(host.ShapeLocalArray) // a source shape the target does not share
	plan, err := BuildPlan(src, remoteTarget("cursor", capableRemoteSpec()), CopyOptions{From: "opencode", To: "cursor"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	item := plan.Items[0]
	if item.Action != ActionDirect {
		t.Errorf("action = %q, want direct (remote entries have no shape to translate)", item.Action)
	}
	if item.TargetDiff != "" {
		t.Errorf("remote row reported a shape diff: %q", item.TargetDiff)
	}
	if item.IR.Endpoint != remoteURL {
		t.Errorf("endpoint lost in the plan: %+v", item.IR)
	}
	if len(item.Verify) != 1 || item.Verify[0] != VerifyL1Config {
		t.Errorf("verify steps = %v, want only %s", item.Verify, VerifyL1Config)
	}
	if len(plan.Writable()) != 1 {
		t.Errorf("writable = %d, want 1", len(plan.Writable()))
	}
}

// TestPlanRemoteConflictClassification: an identical remote entry on the
// target is "already identical"; a different endpoint is a conflict — the
// fingerprint carries the endpoint, so both classifications are reachable.
func TestPlanRemoteConflictClassification(t *testing.T) {
	tgt := remoteTarget("cursor", capableRemoteSpec())
	tgt.ExistingMCP["demo"] = host.HostServerEntry{Name: "demo", Endpoint: remoteURL, Transport: "http"}
	plan, err := BuildPlan(remoteSource("http"), tgt, CopyOptions{From: "claude-code", To: "cursor"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Items[0].Action != ActionSkipped {
		t.Errorf("identical remote entry action = %q, want skipped (%+v)", plan.Items[0].Action, plan.Items[0])
	}

	tgt = remoteTarget("cursor", capableRemoteSpec())
	tgt.ExistingMCP["demo"] = host.HostServerEntry{Name: "demo", Endpoint: "https://other.example/mcp", Transport: "http"}
	plan, err = BuildPlan(remoteSource("http"), tgt, CopyOptions{From: "claude-code", To: "cursor"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Items[0].Action != ActionUnsupported && len(plan.Conflicts) == 0 {
		t.Errorf("different endpoints must classify as a conflict, got %+v", plan.Items[0])
	}
}

// TestPlanRemoteWithForwardedEnvIsRefused: no host documents a name→header
// mapping, so a remote row carrying --env names cannot be ported; the names
// stay visible under Needs instead of being silently dropped.
func TestPlanRemoteWithForwardedEnvIsRefused(t *testing.T) {
	src := remoteSource("http")
	src.MCP[0].EnvNames = []string{"API_TOKEN"}
	plan, err := BuildPlan(src, remoteTarget("cursor", capableRemoteSpec()), CopyOptions{From: "claude-code", To: "cursor"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	item := plan.Items[0]
	if item.Action != ActionUnsupported {
		t.Fatalf("action = %q, want unsupported (%+v)", item.Action, item)
	}
	if !strings.Contains(item.Reason, "API_TOKEN") {
		t.Errorf("refusal must name the variables: %q", item.Reason)
	}
	if len(plan.AllNeeds()) == 0 {
		t.Error("the env names must remain visible under Needs")
	}
}
