#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const os = require("os");
const crypto = require("crypto");
const https = require("https");
const { getBinaryName } = require("../bin/litespm.js");

const VERSION = require("../package.json").version;

// Repository hosting the release assets. Overridable so a mirror, or an
// install racing the repository rename, can be redirected without a code
// change: LITESPM_RELEASE_REPO=owner/name node scripts/install-binary.js
const REPO = process.env.LITESPM_RELEASE_REPO || "sarv-projects/LiteSPM";

function ensureDir(dir) {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

function computeFileSHA256(filePath) {
  const hash = crypto.createHash("sha256");
  const data = fs.readFileSync(filePath);
  hash.update(data);
  return hash.digest("hex");
}

/** Fetch a small text resource over HTTPS, following up to MAX_REDIRECTS. */
function fetchText(url, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > MAX_REDIRECTS) {
      reject(new Error("too many redirects"));
      return;
    }
    request(url)
      .on("error", reject)
      .on("timeout", function () {
        this.destroy(new Error("timeout"));
      })
      .on("response", (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          // A downgrade here would be silently accepted, so it throws instead.
          resolve(fetchText(resolveRedirect(url, res.headers.location), redirects + 1));
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`HTTP ${res.statusCode}`));
          return;
        }
        let bytes = 0;
        let data = "";
        let tooLarge = false;
        res.setEncoding("utf8");
        res.on("data", (chunk) => {
          bytes += Buffer.byteLength(chunk);
          if (bytes > MAX_TEXT_BYTES) {
            tooLarge = true;
            res.destroy(new Error(`response exceeds the ${MAX_TEXT_BYTES} byte limit`));
            return;
          }
          data += chunk;
        });
        res.on("end", () => {
          if (tooLarge || bytes > MAX_TEXT_BYTES) {
            reject(new Error(`response exceeds the ${MAX_TEXT_BYTES} byte limit`));
            return;
          }
          resolve(data);
        });
        res.on("error", reject);
      });
  });
}

/** Extract the SHA-256 hex digest for `binName` from a `sha256sum` style listing. */
function checksumFor(sumsText, binName) {
  for (const line of String(sumsText).split(/\r?\n/)) {
    const match = line.trim().match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/);
    if (match && match[2].trim() === binName) {
      return match[1].toLowerCase();
    }
  }
  return null;
}

// Keep-alive is disabled deliberately: Node's default global agent keeps
// sockets alive for ~5s, which leaves the postinstall process alive after the
// binary is written and makes `npm install` look hung (it ends in SIGINT).
const agent = new https.Agent({ keepAlive: false });

// Guard against a stalled transfer: an npm lifecycle script must never hang.
const REQUEST_TIMEOUT_MS = 120000;

// Bounds for remote reads. A hostile or misconfigured origin must not be able
// to exhaust memory or disk: the checksum manifest is a small text file (the
// Go updater caps it at 1 MiB), and a native binary must fit well under the
// 256 MiB archive bound used by internal/artifact. Redirects are capped at 5,
// matching the Go artifact fetcher.
const MAX_TEXT_BYTES = 1 << 20;
const MAX_BINARY_BYTES = 256 << 20;
const MAX_REDIRECTS = 5;

/**
 * Refuse anything that is not plain HTTPS.
 *
 * This installer downloads a native binary and then executes it, so it is the
 * one place in the tree where a transport downgrade turns into code execution:
 * a redirect to http:// would deliver both the binary and its checksum manifest
 * in cleartext, and a fail-open verification would then accept them. Redirect
 * hosts are deliberately not pinned — GitHub serves release assets from
 * objects.githubusercontent.com, and LITESPM_RELEASE_REPO exists so a mirror can
 * be substituted without a code change — but the scheme is never negotiable.
 */
function assertHTTPS(url) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch (e) {
    throw new Error(`malformed URL: ${url}`);
  }
  if (parsed.protocol !== "https:") {
    throw new Error(`refusing non-HTTPS URL (${parsed.protocol}//): ${url}`);
  }
  return url;
}

function request(url) {
  return https.get(assertHTTPS(url), { agent, timeout: REQUEST_TIMEOUT_MS });
}

/** Resolve a redirect Location against the current URL, keeping HTTPS mandatory. */
function resolveRedirect(currentUrl, location) {
  return assertHTTPS(new URL(location, currentUrl).toString());
}

function downloadFile(url, targetPath, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > MAX_REDIRECTS) {
      reject(new Error("too many redirects"));
      return;
    }
    let settled = false;
    const done = (err) => {
      if (settled) return;
      settled = true;
      if (err) {
        try {
          fs.unlinkSync(targetPath);
        } catch (e) {
          /* best effort */
        }
        reject(err);
      } else {
        resolve();
      }
    };

    const handleResponse = (res, currentUrl, redirectCount) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        if (redirectCount >= MAX_REDIRECTS) {
          res.resume();
          done(new Error("too many redirects"));
          return;
        }
        // Follow the GitHub redirect. The response MUST be drained, otherwise
        // its socket is never released and the process cannot exit. The target
        // must still be HTTPS (see assertHTTPS).
        res.resume();
        let nextUrl;
        try {
          nextUrl = resolveRedirect(currentUrl, res.headers.location);
        } catch (err) {
          done(err);
          return;
        }
        request(nextUrl).on("error", done).on("timeout", function () {
          this.destroy(new Error("download timeout"));
        }).on("response", (r2) => handleResponse(r2, nextUrl, redirectCount + 1));
        return;
      }

      if (res.statusCode !== 200) {
        res.resume();
        done(new Error(`Download failed with status HTTP ${res.statusCode}`));
        return;
      }

      // Bound the download: a binary larger than MAX_BINARY_BYTES is either a
      // misconfigured origin or an attack, never a legitimate release asset.
      let bytes = 0;
      res.on("data", (chunk) => {
        bytes += chunk.length;
        if (bytes > MAX_BINARY_BYTES) {
          done(new Error(`download exceeds the ${MAX_BINARY_BYTES} byte limit`));
          try {
            res.destroy();
          } catch (e) {
            /* best effort */
          }
        }
      });

      // `close` (not `finish`) is the reliable signal that the fd is closed.
      const file = fs.createWriteStream(targetPath);
      file.on("close", () => {
        if (bytes > MAX_BINARY_BYTES) {
          done(new Error(`download exceeds the ${MAX_BINARY_BYTES} byte limit`));
          return;
        }
        done();
      });
      file.on("error", done);
      res.on("error", done);
      res.on("aborted", () => done(new Error("download aborted")));
      res.pipe(file);
    };

    request(url)
      .on("error", done)
      .on("timeout", function () {
        this.destroy(new Error("download timeout"));
      })
      .on("response", (res) => handleResponse(res, url, redirects));
  });
}

/**
 * Verify a downloaded binary against the release's published SHA-256 manifest,
 * deleting it and throwing unless the digest is proven. Fail-closed by design.
 */
async function verifyDownloadedBinary(binaryPath, binName, releaseBase) {
  const fail = (reason) => {
    try {
      fs.unlinkSync(binaryPath);
    } catch (e) {
      /* best effort */
    }
    const err = new Error(`refusing to install ${binName}: ${reason}`);
    // Distinguishes "we could not prove these bytes" from "there were no
    // bytes", because the two demand different operator responses.
    err.unverified = true;
    throw err;
  };

  let sums;
  try {
    sums = await fetchText(`${releaseBase}/SHA256SUMS.txt`);
  } catch (err) {
    fail(`the checksum manifest could not be fetched (${err.message})`);
  }

  const expected = checksumFor(sums, binName);
  if (!expected) {
    fail(`no checksum entry for ${binName} in SHA256SUMS.txt`);
  }

  return assertVerifiedDigest(binaryPath, binName, expected);
}

/**
 * Compare a downloaded file against an expected digest, deleting it and
 * throwing when they disagree. Split out from verifyDownloadedBinary so the
 * accept and reject paths are testable without standing up TLS.
 */
function assertVerifiedDigest(binaryPath, binName, expected) {
  const actual = computeFileSHA256(binaryPath);
  if (actual !== expected) {
    try {
      fs.unlinkSync(binaryPath);
    } catch (e) {
      /* best effort */
    }
    const err = new Error(
      `refusing to install ${binName}: checksum mismatch (expected ${expected}, got ${actual})`
    );
    err.unverified = true;
    throw err;
  }
  console.log(`[litespm] Verified SHA-256 checksum for ${binName}`);
  return actual;
}

async function installBinary() {
  const binName = getBinaryName();
  const targetDir = path.join(os.homedir(), ".litespm", "bin");
  ensureDir(targetDir);
  const targetPath = path.join(targetDir, binName);
  const stampPath = `${targetPath}.version`;

  // 1. Skip only when the installed binary is the version we asked for.
  //    Comparing versions (not mere existence) is what makes an npm upgrade
  //    actually deliver the new binary.
  if (fs.existsSync(targetPath)) {
    let installed = "";
    try {
      installed = fs.readFileSync(stampPath, "utf8").trim();
    } catch (e) {
      installed = "";
    }
    if (installed === VERSION) {
      return;
    }
    console.log(
      `[litespm] Updating ${binName} ${installed || "(unknown)"} -> ${VERSION}`
    );
  }

  // 2. A pre-built binary in dist/ is a developer convenience, NOT a substitute
  //    for the pinned release. It used to take precedence and was stamped
  //    "local", so a checkout with a stale dist/ kept installing that binary
  //    forever and never re-downloaded the version this package pins. It is now
  //    opt-in: set LITESPM_ALLOW_LOCAL_DIST=1 to use it deliberately, e.g.
  //    while testing a build before publishing a release.
  const localDist = path.join(__dirname, "..", "..", "dist", binName);
  if (process.env.LITESPM_ALLOW_LOCAL_DIST === "1" && fs.existsSync(localDist)) {
    fs.copyFileSync(localDist, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    fs.writeFileSync(stampPath, `${VERSION}-local\n`);
    console.log(
      `[litespm] Using the LOCAL build at ${localDist} (LITESPM_ALLOW_LOCAL_DIST=1). ` +
        `It is not the published v${VERSION} binary and its checksum is not verified.`
    );
    return;
  }
  if (fs.existsSync(localDist)) {
    console.log(
      `[litespm] Ignoring the local build at ${localDist}; installing the published ` +
        `v${VERSION} binary. Set LITESPM_ALLOW_LOCAL_DIST=1 to use it deliberately.`
    );
  }

  // 3. Fallback: Download pre-compiled release binary from GitHub Releases
  const releaseBase = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  const releaseUrl = `${releaseBase}/${binName}`;
  console.log(`[litespm] Downloading native binary from GitHub Releases: ${releaseUrl}...`);
  try {
    const tempTarget = `${targetPath}.tmp.${Date.now()}`;
    await downloadFile(releaseUrl, tempTarget);

    // Verification is FAIL-CLOSED. Bytes are on disk and this script's entire
    // job is to make them trustworthy, so an unverifiable download is discarded
    // rather than installed: a missing manifest, a missing entry, an unreadable
    // manifest or a digest mismatch all leave no binary behind. Proceeding on a
    // warning would mean the one artifact we execute is the one artifact we
    // could not check.
    await verifyDownloadedBinary(tempTarget, binName, releaseBase);

    fs.renameSync(tempTarget, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    fs.writeFileSync(stampPath, `${VERSION}\n`);
    console.log(`[litespm] Successfully downloaded and installed ${binName} to ${targetPath}`);
  } catch (err) {
    if (err && err.unverified) {
      // No binary was installed and nothing unverified will run. This is a
      // release-integrity failure, not a network one, so it must not read like
      // a transient notice.
      console.warn(`[litespm] INSTALL REFUSED — ${err.message}`);
      console.warn(
        `[litespm] The download was discarded rather than installed unverified. Verify SHA256SUMS.txt for v${VERSION} on the release, then reinstall.`
      );
    } else {
      console.warn(`[litespm] Notice: could not download the native binary (${err.message}).`);
      console.warn(`[litespm] Expected release asset: ${releaseUrl}`);
      console.warn(
        `[litespm] If that release does not exist yet, push the v${VERSION} tag and let its GitHub release publish before installing from npm.`
      );
    }
    // Fail closed: a postinstall that cannot prove its binary must fail the
    // install (non-zero exit) rather than succeed silently. The wrapper
    // re-attempts the download on first invocation, so a transient network
    // failure is recoverable by retrying the install.
    throw err;
  }
}

if (require.main === module) {
  installBinary()
    .then(() => {
      // Release the pooled sockets and exit explicitly: an npm lifecycle
      // script must not linger waiting for keep-alive timeouts.
      agent.destroy();
      process.exit(0);
    })
    .catch((err) => {
      console.error(`[litespm] Postinstall failed: ${err.message}`);
      agent.destroy();
      process.exit(1);
    });
}

module.exports = {
  installBinary,
  computeFileSHA256,
  checksumFor,
  verifyDownloadedBinary,
  assertVerifiedDigest,
  assertHTTPS,
  resolveRedirect,
  downloadFile,
  fetchText,
  agent,
  MAX_TEXT_BYTES,
  MAX_BINARY_BYTES,
  MAX_REDIRECTS,
};
