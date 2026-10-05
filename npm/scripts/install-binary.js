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

/** Fetch a small text resource over HTTPS, following up to 5 redirects. */
function fetchText(url, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 5) {
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
          resolve(fetchText(res.headers.location, redirects + 1));
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`HTTP ${res.statusCode}`));
          return;
        }
        let data = "";
        res.setEncoding("utf8");
        res.on("data", (chunk) => {
          data += chunk;
        });
        res.on("end", () => resolve(data));
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

function request(url) {
  return https.get(url, { agent, timeout: REQUEST_TIMEOUT_MS });
}

function downloadFile(url, targetPath) {
  return new Promise((resolve, reject) => {
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

    const handleResponse = (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        // Follow GitHub redirect. The response MUST be drained, otherwise its
        // socket is never released and the process cannot exit.
        res.resume();
        request(res.headers.location).on("error", done).on("timeout", function () {
          this.destroy(new Error("download timeout"));
        }).on("response", handleResponse);
        return;
      }

      if (res.statusCode !== 200) {
        res.resume();
        done(new Error(`Download failed with status HTTP ${res.statusCode}`));
        return;
      }

      // `close` (not `finish`) is the reliable signal that the fd is closed.
      const file = fs.createWriteStream(targetPath);
      file.on("close", () => done());
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
      .on("response", handleResponse);
  });
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

  // 2. Check if dist/ contains the pre-compiled binary (e.g. local build or git checkout)
  const localDist = path.join(__dirname, "..", "..", "dist", binName);
  if (fs.existsSync(localDist)) {
    fs.copyFileSync(localDist, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    fs.writeFileSync(stampPath, "local\n");
    console.log(`[litespm] Installed ${binName} to ${targetPath}`);
    return;
  }

  // 3. Fallback: Download pre-compiled release binary from GitHub Releases
  const releaseBase = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  const releaseUrl = `${releaseBase}/${binName}`;
  console.log(`[litespm] Downloading native binary from GitHub Releases: ${releaseUrl}...`);
  try {
    const tempTarget = `${targetPath}.tmp.${Date.now()}`;
    await downloadFile(releaseUrl, tempTarget);

    // Verify the downloaded binary against the published SHA-256 checksums.
    // A mismatch aborts the install; a missing manifest is surfaced as a warning.
    try {
      const sums = await fetchText(`${releaseBase}/SHA256SUMS.txt`);
      const expected = checksumFor(sums, binName);
      if (expected) {
        const actual = computeFileSHA256(tempTarget);
        if (actual !== expected) {
          fs.unlinkSync(tempTarget);
          console.warn(`[litespm] Checksum mismatch for ${binName} (expected ${expected}, got ${actual}); aborting install.`);
          return;
        }
        console.log(`[litespm] Verified SHA-256 checksum for ${binName}`);
      } else {
        console.warn(`[litespm] Warning: no checksum entry for ${binName}; proceeding unverified.`);
      }
    } catch (verifyErr) {
      console.warn(`[litespm] Warning: could not verify checksum (${verifyErr.message}).`);
    }

    fs.renameSync(tempTarget, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    fs.writeFileSync(stampPath, `${VERSION}\n`);
    console.log(`[litespm] Successfully downloaded and installed ${binName} to ${targetPath}`);
  } catch (err) {
    console.warn(`[litespm] Notice: could not download the native binary (${err.message}).`);
    console.warn(`[litespm] Expected release asset: ${releaseUrl}`);
    console.warn(
      `[litespm] If that release does not exist yet, push the v${VERSION} tag and let its GitHub release publish before installing from npm.`
    );
    console.warn(`[litespm] LiteSPM will resolve or re-attempt on first invocation.`);
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
      console.warn(`[litespm] Postinstall notice: ${err.message}`);
      agent.destroy();
      process.exit(0);
    });
}

module.exports = { installBinary, computeFileSHA256, checksumFor, agent };
