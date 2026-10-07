// Package compat stores evidence-backed compatibility records (ARCH/26
// §7.3, ARCH/31 roadmap): host × OS × version × date × test outcome.
//
// Binding rule: publisher-declared support is stored with
// Evidence="declared" and never ranks as verified. A record counts as test
// evidence only when it names the test that produced it.
package compat

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Evidence levels.
const (
	EvidenceTested   = "tested"
	EvidenceDeclared = "declared"
	EvidenceUnknown  = "unknown"
)

// Outcome values.
const (
	OutcomePass = "pass"
	OutcomeFail = "fail"
)

// Record is one compatibility observation.
type Record struct {
	HostID         string    `json:"hostId"`
	HostVersion    string    `json:"hostVersion,omitempty"`
	OS             string    `json:"os,omitempty"`
	PackageID      string    `json:"packageId"`
	PackageVersion string    `json:"packageVersion,omitempty"`
	Outcome        string    `json:"outcome"`
	Evidence       string    `json:"evidence"`
	TestRef        string    `json:"testRef,omitempty"`
	ObservedAt     time.Time `json:"observedAt"`
}

// Store holds compatibility records.
type Store struct {
	records []Record
}

// New returns an empty store.
func New() *Store { return &Store{} }

// Add stores a record. Evidence is mandatory; a "tested" record must also
// name the test that produced it.
func (s *Store) Add(r Record) error {
	if strings.TrimSpace(r.HostID) == "" || strings.TrimSpace(r.PackageID) == "" {
		return fmt.Errorf("LPSM-COMPAT-ID: hostId and packageId are required")
	}
	if r.Outcome != OutcomePass && r.Outcome != OutcomeFail {
		return fmt.Errorf("LPSM-COMPAT-OUTCOME: %q must be pass|fail", r.Outcome)
	}
	if strings.TrimSpace(r.Evidence) == "" {
		return fmt.Errorf("LPSM-COMPAT-NO-EVIDENCE: record for %s on %s has no evidence", r.PackageID, r.HostID)
	}
	if r.Evidence == EvidenceTested && strings.TrimSpace(r.TestRef) == "" {
		return fmt.Errorf("LPSM-COMPAT-NO-TEST: tested evidence requires a test reference")
	}
	if r.ObservedAt.IsZero() {
		r.ObservedAt = time.Now().UTC()
	}
	s.records = append(s.records, r)
	return nil
}

// Declare records publisher-declared support. It is stored honestly as
// declared evidence and never verifies.
func (s *Store) Declare(hostID, packageID, note string) error {
	return s.Add(Record{
		HostID:    hostID,
		PackageID: packageID,
		Outcome:   OutcomePass,
		Evidence:  EvidenceDeclared,
		TestRef:   "",
	})
}

// Query returns records for a package, newest first.
func (s *Store) Query(packageID string) []Record {
	var out []Record
	for _, r := range s.records {
		if r.PackageID == packageID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObservedAt.After(out[j].ObservedAt) })
	return out
}

// VerifiedHosts returns hosts with at least one passing TESTED record for
// the package. Declared-only hosts are excluded by design.
func (s *Store) VerifiedHosts(packageID string) []string {
	seen := map[string]bool{}
	for _, r := range s.records {
		if r.PackageID == packageID && r.Evidence == EvidenceTested && r.Outcome == OutcomePass {
			seen[r.HostID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Len returns the record count.
func (s *Store) Len() int { return len(s.records) }
