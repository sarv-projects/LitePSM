package main

// help_test.go — the ARCH/38 §3.1 binding rule and the help render paths.
//
// The important one is TestHelpCoversEveryDispatchCase: it parses the dispatch
// switch of main.go (the same pattern the capabilities usage test established)
// and fails when a dispatched command has no help entry — and, in the other
// direction, when help advertises a command that does not dispatch.

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// dispatchLiterals returns every string literal the top-level dispatch switch
// in main.go matches. Only the cases indented with exactly one tab are read,
// so the nested `catalog`/`skills` sub-switches are not mistaken for commands.
func dispatchLiterals(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	// Normalize CRLF before token work: Git for Windows checks out text with
	// core.autocrlf=true, and an unquoted case token ending in \r is a syntax
	// error. The dispatch parser must behave identically on every platform.
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	start := strings.Index(text, "func main()")
	end := strings.Index(text, "func printUsage()")
	if start < 0 || end < 0 || end < start {
		t.Fatal("could not locate the dispatch switch between func main() and func printUsage() in main.go")
	}

	var literals []string
	for _, line := range strings.Split(text[start:end], "\n") {
		if !strings.HasPrefix(line, "\tcase \"") {
			continue
		}
		spec := strings.TrimSuffix(strings.TrimPrefix(line, "\tcase "), ":")
		for _, part := range strings.Split(spec, ", ") {
			lit, err := strconv.Unquote(part)
			if err != nil {
				t.Fatalf("dispatch case %q: not a string literal: %v", part, err)
			}
			literals = append(literals, lit)
		}
	}
	if len(literals) == 0 {
		t.Fatal("parsed no dispatch cases out of main.go — the switch moved or the indentation changed")
	}
	return literals
}

// TestHelpCoversEveryDispatchCase is the binding completeness rule: every case
// the dispatcher matches has a help entry, and every help entry is a case the
// dispatcher matches (no aspirational verbs).
func TestHelpCoversEveryDispatchCase(t *testing.T) {
	literals := dispatchLiterals(t)

	seen := map[string]bool{}
	for _, lit := range literals {
		seen[lit] = true
		if _, ok := helpCatalog[lit]; !ok {
			t.Errorf("dispatch case %q has no help entry: a shipped command cannot ship without help", lit)
		}
	}
	for key := range helpCatalog {
		if !seen[key] {
			t.Errorf("help advertises %q, which the dispatcher does not match", key)
		}
	}
	if len(helpCatalog) != len(seen) {
		t.Errorf("helpCatalog has %d keys, the dispatcher has %d cases", len(helpCatalog), len(seen))
	}
}

// TestHelpGroupingCoversEveryCanonicalCommand keeps the default page honest in
// both directions: every group entry exists, and every canonical page is
// listed in exactly one group so no shipped command is hidden.
func TestHelpGroupingCoversEveryCanonicalCommand(t *testing.T) {
	inGroup := map[string]int{}
	for _, g := range helpGroups {
		for _, name := range g.Entries {
			page, ok := helpCatalog[name]
			if !ok {
				t.Errorf("group %s lists %q, which has no help entry", g.Label, name)
				continue
			}
			if page.Canonical != name {
				t.Errorf("group %s lists %q, which is an alias of %q", g.Label, name, page.Canonical)
			}
			inGroup[name]++
		}
	}
	for key, page := range helpCatalog {
		if page.Canonical != key {
			continue // aliases share their canonical page
		}
		if inGroup[key] != 1 {
			t.Errorf("canonical command %q appears in %d groups, want exactly 1", key, inGroup[key])
		}
	}
}

// TestHelpPageTemplateIsComplete runs the §3.1 template over every page: each
// section exists, each field is filled, and nothing is faked with a
// placeholder.
func TestHelpPageTemplateIsComplete(t *testing.T) {
	seenPages := map[string]bool{}
	for _, lit := range dispatchLiterals(t) {
		page, ok := helpCatalog[lit]
		if !ok {
			continue // reported by the completeness test
		}
		if seenPages[page.Canonical] {
			continue // aliases share one page; check it once
		}
		seenPages[page.Canonical] = true
		t.Run(page.Canonical, func(t *testing.T) {
			var buf bytes.Buffer
			renderHelp(&buf, page)
			text := buf.String()

			for _, section := range []string{
				"LITESPM " + strings.ToUpper(page.Canonical),
				"USAGE", "WHAT IT DOES", "WHAT IT DOES NOT",
				"EXAMPLES", "EXIT CODES", "SEE ALSO",
			} {
				if !strings.Contains(text, section) {
					t.Errorf("page is missing the %s section:\n%s", section, text)
				}
			}
			if strings.TrimSpace(page.Purpose) == "" {
				t.Error("page has no one-line purpose")
			}
			if len(page.Synopsis) == 0 {
				t.Error("page has no usage synopsis")
			}
			if len(page.Does) < 2 || len(page.Does) > 4 {
				t.Errorf("WHAT IT DOES has %d entries, want 2-4", len(page.Does))
			}
			if len(page.DoesNot) == 0 {
				t.Error("WHAT IT DOES NOT is empty: every command has honest limits")
			}
			if len(page.Examples) < 2 || len(page.Examples) > 4 {
				t.Errorf("EXAMPLES has %d entries, want 2-4", len(page.Examples))
			}
			for _, ex := range page.Examples {
				if !strings.HasPrefix(ex, "litespm ") {
					t.Errorf("example %q is not a copy-pasteable litespm invocation", ex)
				}
				verb := strings.Fields(ex)[1]
				if _, ok := helpCatalog[verb]; !ok {
					t.Errorf("example %q invokes %q, which does not dispatch", ex, verb)
				}
			}
			for _, related := range page.SeeAlso {
				if _, ok := helpCatalog[related]; !ok {
					t.Errorf("SEE ALSO points at %q, which has no help entry", related)
				}
			}
			// No unfilled template slots and no aspirational verbs anywhere in
			// a page.
			for _, placeholder := range []string{"TODO", "TBD", "<fill", "lorem"} {
				if strings.Contains(text, placeholder) {
					t.Errorf("page contains the placeholder %q", placeholder)
				}
			}
			if !strings.Contains(text, "0  success") || !strings.Contains(text, "2  usage error") {
				t.Errorf("EXIT CODES does not state the ARCH/20 contract:\n%s", text)
			}
		})
	}
	if len(seenPages) == 0 {
		t.Fatal("no help pages were checked")
	}
}

// TestHelpConceptsDefinesVocabulary pins the §3.3 glossary: the words `list`
// and `inventory` use, defined in user language first.
func TestHelpConceptsDefinesVocabulary(t *testing.T) {
	var buf bytes.Buffer
	renderConcepts(&buf)
	text := buf.String()

	for _, term := range []string{
		"PACKAGE", "CAPABILITY", "AGENT", "MCP SERVER", "SKILL", "PLUGIN",
		"BRIDGE", "SCOPE", "MANAGED", "OBSERVED", "ADOPTED",
		"CAPABILITY vs DEPLOYMENT", "REFERENCE vs VALUE",
	} {
		if !strings.Contains(text, term) {
			t.Errorf("concepts does not define %q", term)
		}
	}
	for _, phrase := range []string{
		"unique capabilities", "deployments", "drifted", "not knowable",
	} {
		if !strings.Contains(text, phrase) {
			t.Errorf("concepts does not explain %q", phrase)
		}
	}
	// The counting rule must be stated as the §4 phrasing.
	if !regexp.MustCompile(`\d+ unique capabilities · \d+ deployments`).MatchString(text) {
		t.Errorf("concepts does not show the counting rule phrasing:\n%s", text)
	}
}

// TestHelpRenderPaths covers the three targets and their exit codes: a page
// and the glossary are 0, an unknown target prints the shipped list and is 2,
// and a help invocation with more than one target is 2.
func TestHelpRenderPaths(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		want  int
		inOut []string
		inErr []string
	}{
		{name: "no target is the default page", args: nil, want: 0,
			inOut: []string{"QUICK START", "litespm help concepts", "VIEW"}},
		{name: "a shipped command", args: []string{"copy"}, want: 0,
			inOut: []string{"LITESPM COPY", "WHAT IT DOES NOT", "EXIT CODES"}},
		{name: "an alias resolves to its canonical page", args: []string{"-h"}, want: 0,
			inOut: []string{"LITESPM HELP"}},
		{name: "concepts", args: []string{"concepts"}, want: 0,
			inOut: []string{"OBSERVED", "MANAGED"}},
		{name: "unknown target", args: []string{"nosuchcmd"}, want: 2,
			inErr: []string{"Unknown help target", "DISCOVER", "list · inventory"}},
		{name: "designed verb is unknown", args: []string{"status"}, want: 2,
			inErr: []string{"Unknown help target"}},
		{name: "two targets", args: []string{"copy", "install"}, want: 2,
			inErr: []string{"at most one target"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := helpTo(&out, &errOut, tc.args); got != tc.want {
				t.Fatalf("helpTo(%v) = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					tc.args, got, tc.want, out.String(), errOut.String())
			}
			for _, want := range tc.inOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("stdout is missing %q:\n%s", want, out.String())
				}
			}
			for _, want := range tc.inErr {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("stderr is missing %q:\n%s", want, errOut.String())
				}
			}
		})
	}
}

// TestDefaultHelpAdvertisesOnlyShippedCommands reads the rendered front page
// back and refuses any invocation it names that the dispatcher does not match
// (ARCH/38 invariant 5: an unbuilt verb cannot be advertised).
func TestDefaultHelpAdvertisesOnlyShippedCommands(t *testing.T) {
	var buf bytes.Buffer
	renderDefaultHelp(&buf)
	text := buf.String()

	re := regexp.MustCompile(`litespm ([a-z][a-z-]*)`)
	mentions := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		mentions[m[1]] = true
	}
	if len(mentions) == 0 {
		t.Fatal("the default page names no commands at all")
	}
	for verb := range mentions {
		if _, ok := helpCatalog[verb]; !ok {
			t.Errorf("the default page advertises `litespm %s`, which does not dispatch", verb)
		}
	}
	// Explicitly: none of the designed-but-unshipped verbs may appear.
	for _, verb := range []string{
		"status", "diff", "why", "outdated", "profile", "verify", "adopt",
		"unmanage", "trust", "completion", "clone", "migrate", "mirror",
		"matrix", "sync", "info",
	} {
		if mentions[verb] {
			t.Errorf("the default page advertises the unshipped verb %q", verb)
		}
	}
	// The page must point at both deeper surfaces and the exit-code contract.
	for _, want := range []string{"help <command>", "help concepts", "0 success · 1 refused or failed · 2 usage"} {
		if !strings.Contains(text, want) {
			t.Errorf("the default page does not mention %q", want)
		}
	}
}

// captureStdout collects what fn writes to the real stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	data, _ := io.ReadAll(r)
	_ = r.Close()
	return string(data)
}

// TestPrintUsageIsTheDefaultHelp pins the §3.2 alias: printUsage renders
// exactly what `litespm help` renders, so a bare invocation and `litespm help`
// can never disagree.
func TestPrintUsageIsTheDefaultHelp(t *testing.T) {
	var want bytes.Buffer
	renderDefaultHelp(&want)

	if got := captureStdout(t, printUsage); got != want.String() {
		t.Errorf("printUsage() and renderDefaultHelp disagree:\n--- printUsage ---\n%s\n--- renderDefaultHelp ---\n%s",
			got, want.String())
	}

	var help bytes.Buffer
	if code := helpTo(&help, &help, nil); code != 0 {
		t.Fatalf("helpTo(nil) = %d, want 0", code)
	}
	if help.String() != want.String() {
		t.Errorf("`litespm help` and the default page disagree:\n--- help ---\n%s\n--- default ---\n%s",
			help.String(), want.String())
	}
}
