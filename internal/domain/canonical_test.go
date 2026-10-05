package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestCanonicalizeJSONNumbers pins RFC 8785 §3.2.2 (ECMAScript
// Number::toString) for every range boundary. The regression this guards:
// 'g' float formatting rendered file sizes of 10^6 and above in scientific
// notation ("4.900491e06"), which encoding/json refuses to read back into a
// release manifest's int64 size — the builder's own output was unreadable
// once a real-sized catalog release existed.
func TestCanonicalizeJSONNumbers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Fixed notation: integers a release manifest actually produces.
		{"4900491", "4900491"},            // the size that broke manifest decoding
		{"2713712", "2713712"},            // the second file in that manifest
		{"1000000", "1000000"},            // 10^6: first 'g' exponent case
		{"100", "100"},                    // trailing-zero fixed form
		{"1e20", "100000000000000000000"}, // n = 21: still fixed
		{"0.1", "0.1"},
		{"1.5", "1.5"},
		{"10.5", "10.5"},
		{"123.456", "123.456"},
		{"3.141592653589793", "3.141592653589793"},
		{"0.000001", "0.000001"}, // n = -5: 0.(-n zeros)digits
		{"-1.5", "-1.5"},
		{"-0", "0"},
		{"0", "0"},

		// Exponential: outside the fixed range, sign always explicit.
		{"1e21", "1e+21"},     // n = 22: first exponential case
		{"0.0000001", "1e-7"}, // n = -6: not > -6, so exponential
		{"-1e21", "-1e+21"},
		{"1.7976931348623157e308", "1.7976931348623157e+308"},
	}
	for _, tc := range cases {
		got, err := CanonicalizeJSON([]byte(`{"n":` + tc.in + `}`))
		if err != nil {
			t.Errorf("CanonicalizeJSON(%s): %v", tc.in, err)
			continue
		}
		if want := `{"n":` + tc.want + `}`; string(got) != want {
			t.Errorf("CanonicalizeJSON(%s) = %s, want %s", tc.in, got, want)
		}
	}
}

// TestCanonicalizeJSONNumbersRoundTrip ensures canonical output is still
// decodable into the integer types the release manifest uses — the exact
// failure the scientific-notation regression caused.
func TestCanonicalizeJSONNumbersRoundTrip(t *testing.T) {
	type fileEntry struct {
		Size int64 `json:"size"`
	}
	input := `{"size":4900491}`
	canonical, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatalf("CanonicalizeJSON: %v", err)
	}
	var out fileEntry
	if err := json.Unmarshal(canonical, &out); err != nil {
		t.Fatalf("canonical output %s does not decode into an int64 field: %v", canonical, err)
	}
	if out.Size != 4900491 {
		t.Fatalf("decoded size %d, want 4900491", out.Size)
	}
}

// TestCanonicalizeJSONRejectsNonFinite guards the parse boundary: a number
// outside the double range must fail closed rather than serialize as a
// non-JSON literal.
func TestCanonicalizeJSONRejectsNonFinite(t *testing.T) {
	if _, err := CanonicalizeJSON([]byte(`{"n":1e999}`)); err == nil {
		t.Fatal("expected an out-of-range number to be rejected")
	}
}

// TestCanonicalizeJSONSortsKeysAndKeepsStrings covers the core JCS contract
// the rest of the tree relies on: UTF-16 key order and unchanged strings.
func TestCanonicalizeJSONSortsKeysAndKeepsStrings(t *testing.T) {
	canonical, err := CanonicalizeJSON([]byte(`  { "b": "x", "a": "y z", "digest": "sha256:abc" } `))
	if err != nil {
		t.Fatalf("CanonicalizeJSON: %v", err)
	}
	if got, want := string(canonical), `{"a":"y z","b":"x","digest":"sha256:abc"}`; got != want {
		t.Fatalf("canonical form %s, want %s", got, want)
	}
	// Re-canonicalizing is the identity: digests over canonical bytes stay
	// stable across builds.
	again, err := CanonicalizeJSON(canonical)
	if err != nil {
		t.Fatalf("re-canonicalize: %v", err)
	}
	if !bytes.Equal(again, canonical) {
		t.Fatalf("canonicalization is not idempotent: %s vs %s", again, canonical)
	}
}

func TestComputeBytesDigestFormat(t *testing.T) {
	digest := ComputeBytesDigest([]byte("payload"))
	if !RegexDigest.MatchString(digest) {
		t.Fatalf("ComputeBytesDigest returned %q, which does not match %s", digest, RegexDigest)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("digest %q lacks sha256: prefix", digest)
	}
}
