package source

// snapshot.go — the fetch snapshot layer (ARCH/03 §4 `SourceSnapshot`).
//
// A snapshot is one recorded upstream fetch: the raw bytes exactly as
// received, their SHA-256 content digest, the server's ETag when it sent one,
// and the fetch timestamp, stored as
//
//	<root>/<slug>-<sha256(url)[:12]>/{snapshot.json, body}
//
// The layout, field names and validation rules mirror
// `scripts/snapshot_store.py` byte-for-byte, so a snapshot recorded by the
// Python catalog producer loads here and vice versa (pinned by
// TestSnapshotDirMatchesPythonStore and the testdata snapshot, which was
// written by the Python store).
//
// Honesty rules enforced by the loader — they are the contract, not the
// caller's:
//
//   - LoadSnapshot recomputes the digest and refuses bytes that do not match
//     the recorded `contentDigest` / `byteSize`. A refused snapshot is an
//     error, never a fallback.
//   - A URL with no recorded snapshot yields ErrSnapshotMissing. Nothing is
//     fabricated: WriteSnapshot is only ever called with bytes that arrived
//     from a 200 response, and MarkIngested only updates an existing record.
//   - An ETag-less endpoint snapshots fine (`etag: null`, digest-only
//     refresh comparison).
//
// There is no HTTP fetcher in this package on purpose: ARCH/03 §5 requires
// the build-time ingestion client to enforce HTTPS-only plus DNS/IP-range
// (SSRF) filtering, and no such production client exists yet. The Go seam is
// record + load + verify; the network fetch is scripts/build_full_catalog.py.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/domain"
)

const (
	// SnapshotFormat identifies this on-disk record layout.
	SnapshotFormat = "litespm-source-snapshot/v1"
	// SnapshotAdapterVersion names the writer code that produced a record.
	SnapshotAdapterVersion = "go-source-1"
	// SnapshotStatusPartial / SnapshotStatusHealthy are the ARCH/03 §4
	// ingestion outcomes this store can produce ("failed" is never written
	// because a failed fetch records nothing).
	SnapshotStatusPartial = "partial"
	SnapshotStatusHealthy = "healthy"

	snapshotBodyFile = "body"
	snapshotMetaFile = "snapshot.json"
)

// Errors returned by the snapshot store. Callers map
// ErrSnapshotMissing to "fetch it" and both other errors to "refuse the
// recording" (never fall back to it).
var (
	ErrSnapshotMissing        = errors.New("source: no snapshot recorded")
	ErrSnapshotInvalid        = errors.New("source: snapshot record is invalid")
	ErrSnapshotDigestMismatch = errors.New("source: snapshot content digest mismatch")
)

var (
	snapshotIDPattern     = regexp.MustCompile(`^snap_[0-9A-Za-z]{26}$`)
	snapshotDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	gitETagPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	crockfordAlphabet     = []byte("0123456789ABCDEFGHJKMNPQRSTVWXYZ")
)

// SnapshotSection is the `snapshot` object of the record: exactly the fields
// `schemas/source.schema.json#/properties/snapshot` allows (ARCH/03 §4).
// Nil pointers correspond to JSON `null` (upstreamRevision, etag,
// errorSummary) or an absent optional key (completedAt, itemCount, cursor).
type SnapshotSection struct {
	SnapshotID       string  `json:"snapshotId"`
	SourceID         string  `json:"sourceId"`
	AdapterVersion   string  `json:"adapterVersion"`
	UpstreamRevision *string `json:"upstreamRevision"`
	StartedAt        string  `json:"startedAt"`
	CompletedAt      string  `json:"completedAt,omitempty"`
	Status           string  `json:"status"`
	ItemCount        *int    `json:"itemCount,omitempty"`
	Cursor           string  `json:"cursor,omitempty"`
	ContentDigest    string  `json:"contentDigest"`
	ErrorSummary     *string `json:"errorSummary"`
}

// FetchSection is the fetch-layer extension: where the bytes came from and
// how they were captured.
type FetchSection struct {
	URL        string  `json:"url"`
	ETag       *string `json:"etag"`
	HTTPStatus int     `json:"httpStatus"`
	FetchedAt  string  `json:"fetchedAt"`
	BodyFile   string  `json:"bodyFile"`
	ByteSize   int     `json:"byteSize"`
}

// SnapshotRecord is one snapshot.json document.
type SnapshotRecord struct {
	Format   string          `json:"format"`
	Snapshot SnapshotSection `json:"snapshot"`
	Fetch    FetchSection    `json:"fetch"`
}

// SnapshotContentDigest returns `sha256:<hex>` for raw bytes.
func SnapshotContentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// snapshotSlugFromPath mirrors scripts/snapshot_store.py: snapshot_slug().
func snapshotSlugFromPath(path string) string {
	stem := path
	if i := strings.LastIndex(stem, "/"); i >= 0 {
		stem = stem[i+1:]
	}
	if stem == "" {
		stem = "feed"
	}
	if i := strings.LastIndex(stem, "."); i >= 0 {
		stem = stem[:i]
	}
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(stem) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 48 {
		slug = slug[:48]
	}
	if slug == "" {
		slug = "feed"
	}
	return slug
}

// urlPath mirrors urllib.parse.urlsplit(url).path: everything after the
// authority, cut at the first '?' or '#', still percent-encoded.
func urlPath(raw string) string {
	rest := raw[len("https://"):]
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[i:]
	}
	return ""
}

// SnapshotKey is the per-URL directory key: <slug>-<sha256(url)[:12]>. Only
// https URLs may be snapshotted (ARCH/03 §5 protocol restriction).
func SnapshotKey(sourceURL string) (string, error) {
	if !strings.HasPrefix(sourceURL, "https://") {
		return "", fmt.Errorf("%w: URL must be https://, got %q", ErrSnapshotInvalid, sourceURL)
	}
	sum := sha256.Sum256([]byte(sourceURL))
	return snapshotSlugFromPath(urlPath(sourceURL)) + "-" + hex.EncodeToString(sum[:])[:12], nil
}

// SnapshotDir returns the snapshot directory for a URL under root.
func SnapshotDir(root, sourceURL string) (string, error) {
	key, err := SnapshotKey(sourceURL)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, key), nil
}

// newSnapshotID mints `snap_` + 26-char ULID (48-bit ms timestamp + 80 random
// bits), as the schema's `^snap_[0-9A-Za-z]{26}$` requires.
func newSnapshotID(at time.Time) (string, error) {
	var rnd [10]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("snapshot id entropy: %w", err)
	}
	buf := make([]byte, 16)
	ms := uint64(at.UnixMilli())
	for i := 0; i < 6; i++ {
		buf[i] = byte(ms >> (40 - 8*i))
	}
	copy(buf[6:], rnd[:])

	value := new(big.Int).SetBytes(buf)
	mask := big.NewInt(31)
	out := make([]byte, 26)
	shift := new(big.Int)
	chunk := new(big.Int)
	for i := 0; i < 26; i++ {
		shift.SetInt64(int64(25-i) * 5)
		chunk.Set(value)
		chunk.Rsh(chunk, uint(shift.Int64()))
		chunk.And(chunk, mask)
		out[i] = crockfordAlphabet[int64(chunk.Int64())]
	}
	return "snap_" + string(out), nil
}

// upstreamRevision maps a server ETag to `upstreamRevision` (ARCH/03 §4
// example: `git:<sha>`). Only a 40-hex ETag that really is a git object id
// earns that claim; anything else records no revision rather than a guess.
func upstreamRevision(etag *string) *string {
	if etag == nil {
		return nil
	}
	raw := strings.TrimSpace(*etag)
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, `"`)
	if gitETagPattern.MatchString(raw) {
		rev := "git:" + raw
		return &rev
	}
	return nil
}

func strPtr(s string) *string { return &s }

// WriteSnapshot records a freshly fetched 200 response as a snapshot in
// status `partial` (the record becomes `healthy` only through MarkIngested,
// after a successful parse).
//
// The directory swap is staged through a hidden temp directory so a crash can
// leave either the old snapshot or none (which makes the next run re-fetch),
// never a half-written mixture of new body and old record.
func WriteSnapshot(root string, sourceID domain.SourceID, sourceURL, etag string, httpStatus int, body []byte, fetchedAt time.Time) (SnapshotRecord, error) {
	if httpStatus != 200 {
		return SnapshotRecord{}, fmt.Errorf("%w: only a 200 response can be snapshotted, got %d", ErrSnapshotInvalid, httpStatus)
	}
	if _, err := domain.ParseSourceID(string(sourceID)); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: bad sourceId: %v", ErrSnapshotInvalid, err)
	}
	key, err := SnapshotKey(sourceURL)
	if err != nil {
		return SnapshotRecord{}, err
	}
	id, err := newSnapshotID(fetchedAt)
	if err != nil {
		return SnapshotRecord{}, err
	}

	var etagPtr *string
	if etag != "" {
		etagPtr = strPtr(etag)
	}
	stamp := fetchedAt.UTC().Format("2006-01-02T15:04:05Z")
	rec := SnapshotRecord{
		Format: SnapshotFormat,
		Snapshot: SnapshotSection{
			SnapshotID:       id,
			SourceID:         string(sourceID),
			AdapterVersion:   SnapshotAdapterVersion,
			UpstreamRevision: upstreamRevision(etagPtr),
			StartedAt:        stamp,
			Status:           SnapshotStatusPartial,
			ContentDigest:    SnapshotContentDigest(body),
			ErrorSummary:     nil,
		},
		Fetch: FetchSection{
			URL:        sourceURL,
			ETag:       etagPtr,
			HTTPStatus: httpStatus,
			FetchedAt:  stamp,
			BodyFile:   snapshotBodyFile,
			ByteSize:   len(body),
		},
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return SnapshotRecord{}, fmt.Errorf("snapshot root: %w", err)
	}
	final := filepath.Join(root, key)
	tmp := filepath.Join(root, "."+key+".new."+strconv.Itoa(os.Getpid()))
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return SnapshotRecord{}, fmt.Errorf("stage snapshot: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, snapshotBodyFile), body, 0o644); err != nil {
		return SnapshotRecord{}, fmt.Errorf("write snapshot body: %w", err)
	}
	meta, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("encode snapshot record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, snapshotMetaFile), meta, 0o644); err != nil {
		return SnapshotRecord{}, fmt.Errorf("write snapshot record: %w", err)
	}

	old := filepath.Join(root, "."+key+".old."+strconv.Itoa(os.Getpid()))
	_ = os.RemoveAll(old)
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, old); err != nil {
			return SnapshotRecord{}, fmt.Errorf("replace snapshot: %w", err)
		}
		if err := os.Rename(tmp, final); err != nil {
			_ = os.Rename(old, final) // best effort: keep the previous snapshot
			return SnapshotRecord{}, fmt.Errorf("replace snapshot: %w", err)
		}
		_ = os.RemoveAll(old)
	} else if err := os.Rename(tmp, final); err != nil {
		return SnapshotRecord{}, fmt.Errorf("commit snapshot: %w", err)
	}
	return rec, nil
}

// verifySnapshotRecord applies the checks load_snapshot() applies in the
// Python store. url == "" skips the URL binding check (only Write's callers
// could pass that, and none do).
func verifySnapshotRecord(rec *SnapshotRecord, metaPath, url string) error {
	if rec.Format != SnapshotFormat {
		return fmt.Errorf("%w: %s: unknown format %q", ErrSnapshotInvalid, metaPath, rec.Format)
	}
	if !snapshotIDPattern.MatchString(rec.Snapshot.SnapshotID) {
		return fmt.Errorf("%w: %s: bad snapshotId %q", ErrSnapshotInvalid, metaPath, rec.Snapshot.SnapshotID)
	}
	if _, err := domain.ParseSourceID(rec.Snapshot.SourceID); err != nil {
		return fmt.Errorf("%w: %s: bad sourceId %q", ErrSnapshotInvalid, metaPath, rec.Snapshot.SourceID)
	}
	if rec.Snapshot.Status != SnapshotStatusPartial && rec.Snapshot.Status != SnapshotStatusHealthy {
		return fmt.Errorf("%w: %s: status %q is not ingestible", ErrSnapshotInvalid, metaPath, rec.Snapshot.Status)
	}
	if !snapshotDigestPattern.MatchString(rec.Snapshot.ContentDigest) {
		return fmt.Errorf("%w: %s: bad contentDigest %q", ErrSnapshotInvalid, metaPath, rec.Snapshot.ContentDigest)
	}
	if rec.Fetch.HTTPStatus != 200 {
		return fmt.Errorf("%w: %s: httpStatus %d", ErrSnapshotInvalid, metaPath, rec.Fetch.HTTPStatus)
	}
	if url != "" && rec.Fetch.URL != url {
		return fmt.Errorf("%w: %s: records url %q, not %q", ErrSnapshotInvalid, metaPath, rec.Fetch.URL, url)
	}
	return nil
}

// LoadSnapshot returns the recorded raw bytes for a URL plus their record,
// after verifying the record shape and the content digest. Missing recording
// -> ErrSnapshotMissing; anything that fails verification -> refused with
// ErrSnapshotDigestMismatch or ErrSnapshotInvalid (never returned for use).
func LoadSnapshot(root, sourceURL string) ([]byte, SnapshotRecord, error) {
	dir, err := SnapshotDir(root, sourceURL)
	if err != nil {
		return nil, SnapshotRecord{}, err
	}
	metaPath := filepath.Join(dir, snapshotMetaFile)
	metaRaw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, SnapshotRecord{}, fmt.Errorf("%w: %s", ErrSnapshotMissing, sourceURL)
		}
		return nil, SnapshotRecord{}, fmt.Errorf("%w: %s: %v", ErrSnapshotInvalid, metaPath, err)
	}
	var rec SnapshotRecord
	if err := json.Unmarshal(metaRaw, &rec); err != nil {
		return nil, SnapshotRecord{}, fmt.Errorf("%w: %s: %v", ErrSnapshotInvalid, metaPath, err)
	}
	if err := verifySnapshotRecord(&rec, metaPath, sourceURL); err != nil {
		return nil, SnapshotRecord{}, err
	}

	bodyFile := rec.Fetch.BodyFile
	if bodyFile == "" {
		bodyFile = snapshotBodyFile
	}
	if bodyFile != filepath.Base(bodyFile) || bodyFile == "." || bodyFile == ".." || strings.ContainsAny(bodyFile, `/\`) {
		return nil, SnapshotRecord{}, fmt.Errorf("%w: %s: unsafe body file %q", ErrSnapshotInvalid, metaPath, bodyFile)
	}
	body, err := os.ReadFile(filepath.Join(dir, bodyFile))
	if err != nil {
		return nil, SnapshotRecord{}, fmt.Errorf("%w: %s: body unreadable: %v", ErrSnapshotInvalid, metaPath, err)
	}
	if rec.Fetch.ByteSize != len(body) {
		return nil, SnapshotRecord{}, fmt.Errorf("%w: %s: recorded byteSize %d but body holds %d bytes -- refused",
			ErrSnapshotInvalid, metaPath, rec.Fetch.ByteSize, len(body))
	}
	if actual := SnapshotContentDigest(body); actual != rec.Snapshot.ContentDigest {
		return nil, SnapshotRecord{}, fmt.Errorf("%w: %s: recorded %s, body hashes to %s -- refused",
			ErrSnapshotDigestMismatch, metaPath, rec.Snapshot.ContentDigest, actual)
	}
	return body, rec, nil
}

// MarkIngested finalizes a snapshot after a successful parse: status healthy,
// itemCount, completedAt. It verifies the snapshot first (health is never
// claimed for bytes that fail verification) and returns changed=false when
// the record already says the same, so re-runs leave it byte-for-byte intact.
// A missing recording returns (false, nil): nothing is ever fabricated.
func MarkIngested(root, sourceURL string, itemCount int, at time.Time) (bool, error) {
	_, rec, err := LoadSnapshot(root, sourceURL)
	if err != nil {
		if errors.Is(err, ErrSnapshotMissing) {
			return false, nil
		}
		return false, err
	}
	if rec.Snapshot.Status == SnapshotStatusHealthy && rec.Snapshot.ItemCount != nil && *rec.Snapshot.ItemCount == itemCount {
		return false, nil
	}
	rec.Snapshot.Status = SnapshotStatusHealthy
	rec.Snapshot.CompletedAt = at.UTC().Format("2006-01-02T15:04:05Z")
	rec.Snapshot.ItemCount = &itemCount

	dir, err := SnapshotDir(root, sourceURL)
	if err != nil {
		return false, err
	}
	meta, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode snapshot record: %w", err)
	}
	metaPath := filepath.Join(dir, snapshotMetaFile)
	tmp := metaPath + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, meta, 0o644); err != nil {
		return false, fmt.Errorf("write snapshot record: %w", err)
	}
	if err := os.Rename(tmp, metaPath); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("commit snapshot record: %w", err)
	}
	return true, nil
}
