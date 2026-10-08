"""Fetch snapshot layer for the catalog dataset producer.

Every upstream fetch the catalog build performs is recorded here as a durable,
immutable *source snapshot* before any of its bytes are parsed, per ARCH/03 §4
("Every ingestion cycle of an upstream source is intended to produce a durable,
immutable SourceSnapshot") and the `snapshot` object shape in
`schemas/source.schema.json`. `scripts/build_full_catalog.py` normalizes from
the recorded bytes, never from a live response.

On-disk layout (one directory per fetched URL, under the snapshot root):

    source-snapshots/
      <slug>-<sha256(url)[:12]>/   # the CURRENT (newest) fetch for that URL
        snapshot.json     # {"format": ..., "snapshot": {...}, "fetch": {...}}
        body              # the raw upstream bytes, exactly as received
      history/
        <slug>-<sha256(url)[:12]>/
          <entry>/        # one directory per SUPERSEDED fetch, oldest first
            snapshot.json # the record exactly as it stood when superseded
            body          # that fetch's bytes (hard-linked, never rewritten)
      failures.jsonl      # append-only log of fetch attempts that failed

`snapshot.json.snapshot` carries exactly the fields the ARCH/03 §4 record and
`schemas/source.schema.json#/properties/snapshot` allow; `snapshot.json.fetch`
is the fetch-layer extension (URL, ETag when the server sent one, HTTP status,
fetch timestamp, body file name, byte size).

Per-fetch history (supersession is never destruction): `write_snapshot` never
discards the fetch it replaces. Before the current directory is swapped, the
record being superseded -- url, sourceId, status (including a completed
`healthy` + `itemCount` if it earned one), ETag, timestamps, byte size and
content digest -- is archived verbatim under `history/<key>/<entry>/`. The
current record is always the newest fetch, so a URL's full per-fetch sequence
is the `history` entries (oldest first, via `list_history`) followed by the
current record. A history entry is written once and never rewritten:
`mark_ingested` finalizes only the current record. Entry names are
`<UTC of the archive moment, nanosecond precision>-<snapshotId>`; because
entries are archived in fetch order, names sort chronologically -- the name is
an ordering key, not a fetch-time claim (the record's `fetch.fetchedAt` is the
fetch time). History lives beside -- not inside -- the current directory
because the current directory is swapped wholesale on every write; a snapshot
key can never equal `history` (keys always contain "-" plus 12 hex chars).

Retention (bounded disk growth -- enforced, never unbounded):

  * At most `HISTORY_MAX_ENTRIES` (8) superseded entries per URL, and their
    combined size plus the entry about to be added fits `HISTORY_MAX_BYTES`
    (32 MiB). Pruning drops the oldest entries first.
  * Pruning runs BEFORE an entry is added, so a prune failure (OSError)
    aborts the write while the current record is untouched: history cannot
    grow past the caps while writes still succeed. The current record is
    never pruned (replay depends on it).
  * A single entry that alone exceeds the byte budget is kept until the next
    archive, which prunes it first. The producer bounds one response to
    16 MiB (`build_full_catalog.MAX_RESPONSE_BYTES`), below the byte budget,
    so producer writes cannot produce an over-budget entry.
  * Stale `.entry-tmp-*` staging directories are cleaned opportunistically.

Backward compatibility: stores recorded before history existed (only
`<key>/{snapshot.json,body}`) load unchanged. `history/` is created lazily on
the first supersede, which archives the legacy record at that point -- no
migration step exists because none is needed.

Honesty rules enforced by this module (they are the contract, not the caller's):

  * A snapshot is only written from bytes actually received (HTTP 200). A
    failed fetch writes no snapshot -- never fabricate an entry you did not
    fetch; the failure goes to `failures.jsonl` instead.
  * `load_snapshot` recomputes the content digest and refuses to return bytes
    whose digest or byte size does not match the recorded values, or whose
    record is malformed (bad format, bad snapshot id, bad source id, unknown
    status). A refused snapshot is an error, never a fallback.
    `load_history_entry` applies the same refusal rules to a superseded fetch.
  * An ETag-less endpoint snapshots fine: the ETag is recorded as `null` and
    refresh falls back to digest-only comparison.
  * `mark_ingested` only updates a snapshot that already exists; it never
    creates one.

Storage location: the spec does not pin a filesystem location (the
`source_snapshots` table in ARCH/12 is the daemon's runtime store and no
production ingestion runs). The default root is therefore the in-repo, gitignored
`source-snapshots/` directory, matching this repository's convention that build
inputs/outputs live in-tree and are gitignored (`pages-dist/`, `dist/`,
`web/public/v1/releases/`); `LITESPM_SOURCE_SNAPSHOT_DIR` overrides it.

Run `python3 scripts/snapshot_store.py` to execute the built-in self-test.
"""

import hashlib
import json
import os
import re
import secrets
import shutil
import time
import urllib.parse

SNAPSHOT_FORMAT = "litespm-source-snapshot/v1"
# Version of THIS parser/fetch layer (recorded per snapshot so a record names
# the code that produced it). Not a package version and not a fabricated
# upstream claim.
ADAPTER_VERSION = "py-catalog-1"
BODY_FILE = "body"
META_FILE = "snapshot.json"
FAILURES_FILE = "failures.jsonl"
# Superseded fetches live under root/history/<key>/<entry>/ (see the module
# docstring): one directory per replaced record, its record + body byte-shape
# exactly as they stood when superseded. A snapshot key can never equal
# "history" (keys always contain "-" plus 12 hex chars), so this directory is
# never mistaken for a snapshot directory.
HISTORY_DIR = "history"
# Retention (enforced before each archive, oldest pruned first; the current
# record is never pruned): fewer than HISTORY_MAX_ENTRIES superseded entries
# per URL, combined with the incoming entry within HISTORY_MAX_BYTES.
HISTORY_MAX_ENTRIES = 8
HISTORY_MAX_BYTES = 32 * 1024 * 1024

SNAPSHOT_ID_RE = re.compile(r"^snap_[0-9A-Za-z]{26}$")
DIGEST_RE = re.compile(r"^sha256:[a-f0-9]{64}$")
SOURCE_ID_RE = re.compile(r"^[a-z0-9_-]{3,32}:[a-z0-9_-]{3,64}$")
# The ARCH/03 §4 record shape allows exactly these ingestion outcomes here;
# "failed" is never written because a failed fetch produces no snapshot bytes.
LOADABLE_STATUS = ("partial", "healthy")

_CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"


class SnapshotError(Exception):
    """Base class for snapshot-store failures."""


class SnapshotMissing(SnapshotError):
    """No snapshot was ever recorded for this URL (nothing to fabricate)."""


class SnapshotIntegrityError(SnapshotError):
    """A recorded snapshot failed verification and is refused, not used."""


def default_root():
    """Snapshot root: $LITESPM_SOURCE_SNAPSHOT_DIR, else <repo>/source-snapshots."""
    env = os.environ.get("LITESPM_SOURCE_SNAPSHOT_DIR")
    if env:
        return os.path.abspath(env)
    here = os.path.dirname(os.path.abspath(__file__))
    return os.path.join(os.path.dirname(here), "source-snapshots")


def snapshot_slug(url):
    """Readable per-URL slug: the URL's last path segment, sanitized."""
    path = urllib.parse.urlsplit(url).path
    stem = path.rsplit("/", 1)[-1] or "feed"
    if "." in stem:
        stem = stem.rsplit(".", 1)[0]
    slug = re.sub(r"[^a-z0-9]+", "-", stem.lower()).strip("-")
    return slug[:48] or "feed"


def snapshot_key(url):
    """Directory key for a URL: <slug>-<sha256(url)[:12]> (deterministic).

    The URL digest guarantees distinctness (two sources ending in
    `marketplace.json` differ); the slug keeps the directory readable.
    Only https URLs may be snapshotted (ARCH/03 §5 protocol restriction).
    """
    if not url.startswith("https://"):
        raise ValueError(f"snapshot URL must be https://, got {url!r}")
    url_digest = hashlib.sha256(url.encode("utf-8")).hexdigest()[:12]
    return f"{snapshot_slug(url)}-{url_digest}"


def snapshot_dir(root, url):
    return os.path.join(root, snapshot_key(url))


def history_dir(root, url):
    """Directory holding this URL's superseded fetches (may not exist yet)."""
    return os.path.join(root, HISTORY_DIR, snapshot_key(url))


def digest_bytes(body):
    return "sha256:" + hashlib.sha256(body).hexdigest()


def iso_utc(at=None):
    t = time.time() if at is None else at
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(t))


def new_snapshot_id(at=None):
    """`snap_` + 26-char ULID (48-bit ms timestamp + 80 random bits), as the
    schema's `^snap_[0-9A-Za-z]{26}$` requires."""
    ms = int((time.time() if at is None else at) * 1000)
    value = (ms << 80) | secrets.randbits(80)
    chars = [_CROCKFORD[(value >> ((25 - i) * 5)) & 31] for i in range(26)]
    return "snap_" + "".join(chars)


def etag_revision(etag):
    """Map a server ETag to `upstreamRevision` (ARCH/03 §4 example:
    `git:<sha>`). Only a 40-hex ETag that really is a git object id earns
    that claim; anything else records no revision rather than a guess."""
    if not etag:
        return None
    raw = etag.strip()
    if raw.startswith("W/"):
        raw = raw[2:]
    raw = raw.strip('"')
    if re.fullmatch(r"[0-9a-f]{40}", raw):
        return "git:" + raw
    return None


def _atomic_write(path, data):
    tmp = f"{path}.tmp-{os.getpid()}"
    with open(tmp, "wb") as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, path)


def _record_json(snapshot, fetch):
    return json.dumps(
        {"format": SNAPSHOT_FORMAT, "snapshot": snapshot, "fetch": fetch},
        indent=2,
    ).encode("utf-8")


def _history_entries(hist):
    """Committed entry names under `hist`, oldest first (staging excluded)."""
    try:
        names = os.listdir(hist)
    except FileNotFoundError:
        return []
    return sorted(
        n for n in names
        if not n.startswith(".") and os.path.isdir(os.path.join(hist, n))
    )


def _dir_size(path):
    """Total bytes of the files directly inside an entry directory."""
    total = 0
    try:
        names = os.listdir(path)
    except OSError:
        return 0
    for name in names:
        try:
            total += os.path.getsize(os.path.join(path, name))
        except OSError:
            pass
    return total


def _prune_history(hist, incoming_bytes):
    """Enforce retention BEFORE a new entry is added (oldest first).

    On return, adding one entry of `incoming_bytes` keeps the directory at
    most HISTORY_MAX_ENTRIES entries and at most HISTORY_MAX_BYTES total --
    unless that single entry alone exceeds the byte budget (it is then kept
    until the next archive, which prunes it first). A removal failure (OSError)
    propagates with nothing added, so growth is fail-closed, never unbounded.
    """
    try:
        names = os.listdir(hist)
    except FileNotFoundError:
        return
    for name in names:  # stale staging residue from an interrupted archive
        if name.startswith(".entry-tmp-"):
            shutil.rmtree(os.path.join(hist, name), ignore_errors=True)
    entries = _history_entries(hist)
    sizes = {n: _dir_size(os.path.join(hist, n)) for n in entries}
    total = sum(sizes.values())
    while entries and (len(entries) + 1 > HISTORY_MAX_ENTRIES
                       or total + incoming_bytes > HISTORY_MAX_BYTES):
        victim = entries.pop(0)
        total -= sizes[victim]
        shutil.rmtree(os.path.join(hist, victim))


def _link_or_copy(src, dst):
    """Preserve `src` at `dst`, hard-linking first so supersession costs no
    extra disk until the superseded directory is removed (copy as fallback)."""
    try:
        os.link(src, dst)
    except OSError:
        shutil.copy2(src, dst)


def _new_entry_name(sid):
    """`<UTC of this archive moment, nanosecond precision>-<snapshotId>`.

    Archives happen in fetch order, so names sort chronologically; the name
    is an ordering key, not a fetch-time claim (see the module docstring).
    """
    ns = time.time_ns()
    sec, frac = divmod(ns, 1_000_000_000)
    stamp = time.strftime("%Y%m%dT%H%M%S", time.gmtime(sec)) + f".{frac:09d}Z"
    return f"{stamp}-{sid}"


def _archive_current(final, root, key):
    """Preserve the fetch `write_snapshot` is about to supersede.

    Called BEFORE the directory swap. Every regular file of the current
    record (the record, its body, and any stray a corrupt record may sit
    beside) is hard-linked into `root/history/<key>/<entry>/` and committed
    with one atomic rename, so a crash leaves either both the current record
    and its archive (retry skips it: identical record already archived), or
    a committed history entry plus no current record (a re-fetch -- the entry
    keeps the bytes). Supersession therefore never destroys a recorded byte.
    Retention runs before the entry is added; any OSError propagates with the
    current record untouched.
    """
    if not os.path.isdir(final):
        return
    meta_path = os.path.join(final, META_FILE)
    if not os.path.isfile(meta_path):
        return  # no record in this directory: nothing was fetched to preserve
    with open(meta_path, "rb") as f:
        meta_bytes = f.read()

    sid = "unknown"
    try:
        rec = json.loads(meta_bytes.decode("utf-8"))
        candidate = (rec.get("snapshot") or {}).get("snapshotId")
        if SNAPSHOT_ID_RE.match(candidate or ""):
            sid = candidate
    except (ValueError, UnicodeDecodeError, AttributeError, TypeError):
        pass  # a malformed record is archived as it stands, under a derived name

    pending = []
    incoming = 0
    for name in sorted(os.listdir(final)):
        if name.startswith("."):
            continue  # staging residue of an interrupted swap, not record data
        path = os.path.join(final, name)
        if os.path.isfile(path):
            pending.append((name, path))
            try:
                incoming += os.path.getsize(path)
            except OSError:
                pass  # accounting only; linking below reports real failures
    if not any(name == META_FILE for name, _ in pending):
        return  # vanished between checks: nothing verifiable to archive

    hist = os.path.join(root, HISTORY_DIR, key)
    if sid != "unknown":  # crash retry: identical record already committed
        for existing in _history_entries(hist):
            if existing.endswith("-" + sid):
                try:
                    with open(os.path.join(hist, existing, META_FILE), "rb") as f:
                        if f.read() == meta_bytes:
                            return
                except OSError:
                    pass
                break

    os.makedirs(hist, exist_ok=True)
    _prune_history(hist, incoming)
    name = _new_entry_name(sid)
    dest = os.path.join(hist, name)
    n = 1
    while os.path.exists(dest):
        n += 1
        dest = os.path.join(hist, f"{name}-{n}")
    stage = os.path.join(hist, f".entry-tmp-{os.getpid()}")
    shutil.rmtree(stage, ignore_errors=True)
    os.mkdir(stage)
    try:
        for entry_name, path in pending:
            _link_or_copy(path, os.path.join(stage, entry_name))
        os.rename(stage, dest)
    except OSError:
        shutil.rmtree(stage, ignore_errors=True)
        raise


def write_snapshot(root, url, source_id, etag, http_status, body,
                   started_at=None, fetched_at=None):
    """Record a freshly fetched response as a snapshot (status `partial`).

    The fetch this replaces is first archived verbatim under
    `root/history/<key>/<entry>/` (immutable per-fetch history, retention-
    bounded; see the module docstring), then the directory swap is staged
    through a hidden temp directory so a crash can leave the old snapshot,
    an archived copy of it plus none (which makes the next run re-fetch), or
    none -- never a half-written mixture of new body and old record.
    """
    if http_status != 200:
        raise ValueError(f"only a 200 response can be snapshotted, got {http_status}")
    if not SOURCE_ID_RE.match(source_id or ""):
        raise ValueError(f"invalid sourceId for snapshot: {source_id!r}")
    if not isinstance(body, (bytes, bytearray)):
        raise TypeError("snapshot body must be raw bytes")
    body = bytes(body)

    key = snapshot_key(url)
    os.makedirs(root, exist_ok=True)
    final = os.path.join(root, key)
    tmp = os.path.join(root, f".{key}.new.{os.getpid()}")
    shutil.rmtree(tmp, ignore_errors=True)
    os.makedirs(tmp)

    snapshot = {
        "snapshotId": new_snapshot_id(fetched_at),
        "sourceId": source_id,
        "adapterVersion": ADAPTER_VERSION,
        "upstreamRevision": etag_revision(etag),
        "startedAt": iso_utc(started_at if started_at is not None else fetched_at),
        "status": "partial",
        "contentDigest": digest_bytes(body),
        "errorSummary": None,
    }
    fetch = {
        "url": url,
        "etag": etag,
        "httpStatus": http_status,
        "fetchedAt": iso_utc(fetched_at),
        "bodyFile": BODY_FILE,
        "byteSize": len(body),
    }
    with open(os.path.join(tmp, BODY_FILE), "wb") as f:
        f.write(body)
    with open(os.path.join(tmp, META_FILE), "wb") as f:
        f.write(_record_json(snapshot, fetch))

    # Preserve the fetch being superseded before touching the current record.
    # A failure here aborts the write with the current record untouched.
    try:
        _archive_current(final, root, key)
    except OSError:
        shutil.rmtree(tmp, ignore_errors=True)
        raise

    old = os.path.join(root, f".{key}.old.{os.getpid()}")
    shutil.rmtree(old, ignore_errors=True)
    had_previous = os.path.isdir(final)
    try:
        if had_previous:
            os.rename(final, old)
        os.rename(tmp, final)
    except OSError:
        try:
            if had_previous and not os.path.isdir(final) and os.path.isdir(old):
                os.rename(old, final)  # best effort: keep the previous snapshot
        finally:
            shutil.rmtree(tmp, ignore_errors=True)
        raise
    shutil.rmtree(old, ignore_errors=True)
    return json.loads(_record_json(snapshot, fetch).decode("utf-8"))


def _load_record_dir(directory, url, missing_message):
    """Read + fully verify the record stored in `directory`.

    Shared by `load_snapshot` (the current record) and `load_history_entry`
    (a superseded fetch): identical refusal rules either way.
    """
    meta_path = os.path.join(directory, META_FILE)
    try:
        with open(meta_path, "rb") as f:
            raw = f.read()
    except FileNotFoundError:
        raise SnapshotMissing(missing_message) from None
    except OSError as exc:
        raise SnapshotIntegrityError(f"unreadable snapshot record {meta_path}: {exc}") from exc

    try:
        meta = json.loads(raw.decode("utf-8"))
    except (ValueError, UnicodeDecodeError) as exc:
        raise SnapshotIntegrityError(f"snapshot record {meta_path} is not valid JSON: {exc}") from exc
    _verify_record(meta, meta_path, url)

    snapshot = meta["snapshot"]
    fetch = meta["fetch"]
    body_file = fetch.get("bodyFile") or BODY_FILE
    if body_file != os.path.basename(body_file) or body_file in (".", ".."):
        raise SnapshotIntegrityError(f"snapshot {meta_path} names an unsafe body file {body_file!r}")
    body_path = os.path.join(directory, body_file)
    try:
        with open(body_path, "rb") as f:
            body = f.read()
    except OSError as exc:
        raise SnapshotIntegrityError(f"snapshot body {body_path} unreadable: {exc}") from exc

    if fetch.get("byteSize") != len(body):
        raise SnapshotIntegrityError(
            f"snapshot {meta_path}: recorded byteSize {fetch.get('byteSize')!r} "
            f"but body holds {len(body)} bytes -- refused"
        )
    actual = digest_bytes(body)
    if actual != snapshot["contentDigest"]:
        raise SnapshotIntegrityError(
            f"snapshot {meta_path}: contentDigest {snapshot['contentDigest']} "
            f"does not match body digest {actual} -- refused"
        )
    return body, meta


def load_snapshot(root, url):
    """Return `(raw_bytes, record)` for a recorded snapshot, verified.

    Refuses (raises `SnapshotIntegrityError`) when the record is malformed or
    the bytes do not hash to the recorded `contentDigest` / `byteSize`.
    Raises `SnapshotMissing` when nothing was ever recorded -- a missing entry
    is never invented.
    """
    key = snapshot_key(url)
    return _load_record_dir(
        os.path.join(root, key), url, f"no recorded snapshot for {url}")


def list_history(root, url):
    """Superseded fetches for `url`, oldest first, as `(entry, record)` pairs.

    Verifies each entry's record shape and URL binding but does not hash
    bodies; use `load_history_entry` for a fully verified replay. An entry
    whose record fails verification raises `SnapshotIntegrityError` --
    corruption is refused loudly, never skipped silently. A URL with no
    history (including one never recorded) returns `[]`.
    """
    hist = history_dir(root, url)
    out = []
    for name in _history_entries(hist):
        path = os.path.join(hist, name, META_FILE)
        try:
            with open(path, "rb") as f:
                raw = f.read()
        except OSError as exc:
            raise SnapshotIntegrityError(f"unreadable history record {path}: {exc}") from exc
        try:
            meta = json.loads(raw.decode("utf-8"))
        except (ValueError, UnicodeDecodeError) as exc:
            raise SnapshotIntegrityError(f"history record {path} is not valid JSON: {exc}") from exc
        _verify_record(meta, path, url)
        out.append((name, meta))
    return out


def load_history_entry(root, url, entry):
    """Return `(raw_bytes, record)` for one superseded fetch, verified.

    Same refusal rules as `load_snapshot`. `entry` must be a plain entry
    name (as returned by `list_history`) -- never a path.
    """
    if (not entry or entry in (".", "..") or "/" in entry or "\\" in entry
            or entry != os.path.basename(entry)):
        raise SnapshotMissing(f"no history entry {entry!r} for {url}")
    return _load_record_dir(
        os.path.join(history_dir(root, url), entry), url,
        f"no history entry {entry!r} for {url}")


def _verify_record(meta, meta_path, url=None):
    if not isinstance(meta, dict) or meta.get("format") != SNAPSHOT_FORMAT:
        raise SnapshotIntegrityError(f"snapshot {meta_path}: unknown format {meta.get('format')!r}")
    snapshot = meta.get("snapshot")
    fetch = meta.get("fetch")
    if not isinstance(snapshot, dict) or not isinstance(fetch, dict):
        raise SnapshotIntegrityError(f"snapshot {meta_path}: missing snapshot/fetch sections")
    if not SNAPSHOT_ID_RE.match(snapshot.get("snapshotId") or ""):
        raise SnapshotIntegrityError(f"snapshot {meta_path}: bad snapshotId {snapshot.get('snapshotId')!r}")
    if not SOURCE_ID_RE.match(snapshot.get("sourceId") or ""):
        raise SnapshotIntegrityError(f"snapshot {meta_path}: bad sourceId {snapshot.get('sourceId')!r}")
    if snapshot.get("status") not in LOADABLE_STATUS:
        raise SnapshotIntegrityError(
            f"snapshot {meta_path}: status {snapshot.get('status')!r} is not ingestible"
        )
    if not DIGEST_RE.match(snapshot.get("contentDigest") or ""):
        raise SnapshotIntegrityError(f"snapshot {meta_path}: bad contentDigest")
    if fetch.get("httpStatus") != 200:
        raise SnapshotIntegrityError(f"snapshot {meta_path}: httpStatus {fetch.get('httpStatus')!r}")
    if url is not None and fetch.get("url") != url:
        raise SnapshotIntegrityError(
            f"snapshot {meta_path}: records url {fetch.get('url')!r}, not {url!r}"
        )


def mark_ingested(root, url, item_count, at=None):
    """Finalize a snapshot after a successful parse: status `healthy`,
    `itemCount`, `completedAt`.

    Returns `(record, changed)`. Only an existing snapshot is updated: a
    missing entry returns `(None, False)` and is never created here, and a
    snapshot already recorded healthy with the same count is left byte-for-byte
    untouched (so re-runs are reproducible).
    """
    try:
        _, meta = load_snapshot(root, url)  # verifies integrity before claiming health
    except SnapshotMissing:
        return None, False  # nothing recorded: never create an entry here
    snapshot = meta["snapshot"]
    if snapshot.get("status") == "healthy" and snapshot.get("itemCount") == item_count:
        return meta, False

    completed = {"completedAt": iso_utc(at), "status": "healthy", "itemCount": item_count}
    snapshot.update(completed)
    meta_path = os.path.join(snapshot_dir(root, url), META_FILE)
    _atomic_write(meta_path, _record_json(snapshot, meta["fetch"]))
    return meta, True


def log_failure(root, url, stage, error):
    """Append one attempt failure to failures.jsonl.

    This is a log line, not a snapshot: it records that a fetch happened and
    failed, and carries no fabricated contentDigest or bytes.
    """
    os.makedirs(root, exist_ok=True)
    line = json.dumps(
        {"at": iso_utc(), "stage": stage, "url": url, "error": str(error)},
        ensure_ascii=False,
    ) + "\n"
    fd = os.open(os.path.join(root, FAILURES_FILE), os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o644)
    try:
        os.write(fd, line.encode("utf-8"))
    finally:
        os.close(fd)


def _selftest():
    import tempfile

    checks = 0

    def ok(cond, msg):
        nonlocal checks
        if not cond:
            raise AssertionError(msg)
        checks += 1

    url = "https://example.com/vendor/.claude-plugin/marketplace.json"
    body = b'{"plugins": [{"name": "demo"}]}\n'

    with tempfile.TemporaryDirectory() as root:
        # 1. A fetch is recorded, then replayed byte-for-byte.
        rec = write_snapshot(root, url, "git:claude-plugins-official",
                             '"abc123etag"', 200, body)
        ok(SNAPSHOT_ID_RE.match(rec["snapshot"]["snapshotId"]), "snapshotId grammar")
        ok(rec["snapshot"]["status"] == "partial", "fresh snapshot starts partial")
        ok("completedAt" not in rec["snapshot"], "partial snapshot has no completedAt")
        got, meta = load_snapshot(root, url)
        ok(got == body, "replay returns recorded bytes")
        ok(meta["fetch"]["etag"] == '"abc123etag"', "etag recorded verbatim")
        ok(meta["fetch"]["url"] == url, "url recorded")

        # 2. Ingestion finalization; second call is a no-op (byte-stable).
        meta1, changed1 = mark_ingested(root, url, 1)
        ok(changed1, "first mark_ingested changes the record")
        ok(meta1["snapshot"]["status"] == "healthy", "marked healthy")
        ok(meta1["snapshot"]["itemCount"] == 1, "itemCount recorded")
        meta_path = os.path.join(snapshot_dir(root, url), META_FILE)
        before = open(meta_path, "rb").read()
        meta2, changed2 = mark_ingested(root, url, 1)
        ok(not changed2, "repeated mark is a no-op")
        ok(open(meta_path, "rb").read() == before, "healthy record is byte-stable across re-runs")

        # 3. A body that no longer matches its digest is refused, not used.
        with open(os.path.join(snapshot_dir(root, url), BODY_FILE), "ab") as f:
            f.write(b"tampered")
        try:
            load_snapshot(root, url)
        except SnapshotIntegrityError:
            checks += 1
        else:
            raise AssertionError("tampered body must be refused")

        # 4. An ETag-less endpoint still snapshots (digest-only).
        etagless = "https://example.com/feed/readme.md"
        write_snapshot(root, etagless, "feed:example-feed", None, 200, b"# hi\n")
        got, meta = load_snapshot(root, etagless)
        ok(got == b"# hi\n" and meta["fetch"]["etag"] is None, "etag-less snapshot loads digest-only")
        ok(etag_revision(None) is None, "no revision claimed without an etag")
        ok(etag_revision('"4b102a6326ec6fdf7bcbe8813eb2298f8036030186a4698b080ab111b8c16324"') is None,
           "no git revision claimed for a content-shaped etag")
        sha40 = '"' + "0123456789abcdef0123456789abcdef01234567" + '"'  # 40 hex chars
        ok(etag_revision(sha40) == "git:" + sha40.strip('"'), "40-hex etag maps to git revision")
        ok(etag_revision("W/" + sha40) == "git:" + sha40.strip('"'),
           "weak 40-hex etag also maps to git revision")

        # 5. A URL never fetched has no entry (never fabricated).
        try:
            load_snapshot(root, "https://example.com/never/fetched.json")
        except SnapshotMissing:
            checks += 1
        else:
            raise AssertionError("missing snapshot must raise, not invent bytes")

        # 6. mark_ingested on a missing snapshot creates nothing.
        ok(mark_ingested(root, "https://example.com/never/fetched.json", 5) == (None, False),
           "mark_ingested never fabricates an entry")
        ok(not os.path.exists(snapshot_dir(root, "https://example.com/never/fetched.json")),
           "no directory is created for an unfetched URL")

        # 7. Failed fetches are logged, not snapshotted.
        log_failure(root, "https://example.com/feed/readme.md", "refresh", "timeout")
        lines = open(os.path.join(root, FAILURES_FILE), encoding="utf-8").read().splitlines()
        ok(len(lines) == 1 and json.loads(lines[0])["stage"] == "refresh", "failure logged")

        # 8. Key derivation is deterministic and https-only.
        ok(snapshot_key(url) == snapshot_key(url), "key deterministic")
        ok(snapshot_key(url) != snapshot_key(etagless), "distinct urls, distinct keys")
        try:
            snapshot_key("http://example.com/x.json")
        except ValueError:
            checks += 1
        else:
            raise AssertionError("http URL must be refused")

    # 9. Two snapshots written in sequence get distinct ULIDs.
    a, b = new_snapshot_id(), new_snapshot_id()
    ok(a != b, "snapshot ids are unique")
    ok(DIGEST_RE.match(digest_bytes(body)), "digest grammar")

    # 10. Per-fetch history: superseding a URL archives the fetch it replaces.
    with tempfile.TemporaryDirectory() as root:
        h_url = "https://example.com/feed/changelog.md"
        rec1 = write_snapshot(root, h_url, "feed:example-feed", None, 200, b"version one\n")
        ok(list_history(root, h_url) == [],
           "no history exists before the first fetch is superseded")
        got, meta = load_snapshot(root, h_url)
        ok(got == b"version one\n", "first fetch replays as the current record")
        meta1, changed1 = mark_ingested(root, h_url, 3)
        ok(changed1 and meta1["snapshot"]["status"] == "healthy",
           "first fetch finalized before it can be superseded")

        rec2 = write_snapshot(root, h_url, "feed:example-feed", '"etag-two"', 200,
                              b"version two\n")
        entries = list_history(root, h_url)
        ok(len(entries) == 1, f"superseded fetch archived exactly once, got {len(entries)}")
        entry1, archived = entries[0]
        ok(archived["snapshot"]["snapshotId"] == rec1["snapshot"]["snapshotId"],
           "history holds the exact record that was current")
        ok(archived["snapshot"]["status"] == "healthy"
           and archived["snapshot"].get("itemCount") == 3,
           "history preserves the finalized status and itemCount as they stood")
        ok(archived["fetch"]["etag"] is None
           and archived["fetch"]["byteSize"] == len(b"version one\n")
           and archived["fetch"]["url"] == h_url,
           "history records what was actually fetched (url, etag, byte size)")
        body1, rec1b = load_history_entry(root, h_url, entry1)
        ok(body1 == b"version one\n", "history entry replays the superseded bytes")
        ok(rec1b["snapshot"]["contentDigest"] == digest_bytes(b"version one\n"),
           "history digest is the digest of the bytes actually fetched")
        cur_body, cur = load_snapshot(root, h_url)
        ok(cur_body == b"version two\n" and cur["snapshot"]["status"] == "partial"
           and cur["snapshot"]["snapshotId"] == rec2["snapshot"]["snapshotId"],
           "current record is the newest fetch, fresh and partial")

        # Immutability: finalizing the new current never rewrites history.
        mark_ingested(root, h_url, 7)
        _, archived_after = load_history_entry(root, h_url, entry1)
        ok(archived_after["snapshot"].get("itemCount") == 3,
           "history entry is immutable while the current record changes")

        # A tampered history entry is refused, never served.
        with open(os.path.join(history_dir(root, h_url), entry1, BODY_FILE), "ab") as f:
            f.write(b"tampered")
        try:
            load_history_entry(root, h_url, entry1)
        except SnapshotIntegrityError:
            checks += 1
        else:
            raise AssertionError("tampered history entry must be refused")

        # Entry names are names, never paths.
        try:
            load_history_entry(root, h_url, "../" + snapshot_key(h_url))
        except SnapshotError:
            checks += 1
        else:
            raise AssertionError("history entry path traversal must be refused")

        # 11. Retention: the entry cap prunes oldest; the current record and
        # the byte budget are enforced by the same pre-archive prune.
        for i in range(HISTORY_MAX_ENTRIES + 5):
            write_snapshot(root, h_url, "feed:example-feed", None, 200,
                           f"filler {i}".encode("utf-8"))
        entries = list_history(root, h_url)
        ok(len(entries) == HISTORY_MAX_ENTRIES,
           f"history capped at {HISTORY_MAX_ENTRIES} entries, got {len(entries)}")
        ok(load_history_entry(root, h_url, entries[0][0])[0] == b"filler 4",
           "oldest superseded fetches pruned first")
        ok(load_history_entry(root, h_url, entries[-1][0])[0] == b"filler 11",
           "newest superseded fetch kept within the cap")
        ok(load_snapshot(root, h_url)[0] == b"filler 12",
           "the current record is never pruned")

        saved_budget = globals()["HISTORY_MAX_BYTES"]
        globals()["HISTORY_MAX_BYTES"] = 64  # one record already exceeds this
        try:
            for i in range(4):
                write_snapshot(root, h_url, "feed:example-feed", None, 200,
                               f"tiny {i}".encode("utf-8"))
            entries = list_history(root, h_url)
            ok(len(entries) == 1,
               f"byte budget prunes history to a single entry, got {len(entries)}")
            ok(load_history_entry(root, h_url, entries[0][0])[0] == b"tiny 2",
               "the byte-budget survivor is the most recent superseded fetch")
        finally:
            globals()["HISTORY_MAX_BYTES"] = saved_budget

    # 12. A store recorded before history existed loads unchanged; history
    # appears lazily when that legacy record is first superseded (no migration).
    with tempfile.TemporaryDirectory() as legacy:
        l_url = "https://example.com/legacy/feed.json"
        write_snapshot(legacy, l_url, "feed:example-feed", None, 200, b'{"legacy": true}')
        ok(not os.path.isdir(os.path.join(legacy, HISTORY_DIR)),
           "first fetch creates no history directory (legacy on-disk layout)")
        got, meta = load_snapshot(legacy, l_url)
        ok(got == b'{"legacy": true}', "legacy-shaped store replays unchanged")
        ok(list_history(legacy, "https://example.com/never/historied.json") == [],
           "a URL with no history lists empty, never fabricated")
        write_snapshot(legacy, l_url, "feed:example-feed", None, 200, b'{"legacy": false}')
        entries = list_history(legacy, l_url)
        ok(len(entries) == 1,
           "legacy record archived on first supersede (lazy, no migration)")
        ok(load_history_entry(legacy, l_url, entries[0][0])[0] == b'{"legacy": true}',
           "legacy bytes preserved verbatim in history")

    print(f"SNAPSHOT STORE SELFTEST OK ({checks} checks)")


if __name__ == "__main__":
    _selftest()
