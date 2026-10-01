#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const os = require("os");
const crypto = require("crypto");
const https = require("https");
const { getBinaryName } = require("../bin/litepsm.js");

const VERSION = require("../package.json").version;
const REPO = "sarv-projects/LitePSM";

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
    https
      .get(url, (res) => {
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
      })
      .on("error", reject);
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

function downloadFile(url, targetPath) {
  return new Promise((resolve, reject) => {
    const handleResponse = (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        // Follow GitHub redirect
        https.get(res.headers.location, handleResponse).on("error", reject);
        return;
      }

      if (res.statusCode !== 200) {
        reject(new Error(`Download failed with status HTTP ${res.statusCode}`));
        return;
      }

      const file = fs.createWriteStream(targetPath);
      res.pipe(file);
      file.on("finish", () => {
        file.close(resolve);
      });
      file.on("error", (err) => {
        fs.unlink(targetPath, () => {});
        reject(err);
      });
    };

    https.get(url, handleResponse).on("error", reject);
  });
}

async function installBinary() {
  const binName = getBinaryName();
  const targetDir = path.join(os.homedir(), ".litepsm", "bin");
  ensureDir(targetDir);
  const targetPath = path.join(targetDir, binName);

  // 1. If already installed, skip
  if (fs.existsSync(targetPath)) {
    return;
  }

  // 2. Check if dist/ contains the pre-compiled binary (e.g. local build or git checkout)
  const localDist = path.join(__dirname, "..", "..", "dist", binName);
  if (fs.existsSync(localDist)) {
    fs.copyFileSync(localDist, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    console.log(`[litepsm] Installed ${binName} to ${targetPath}`);
    return;
  }

  // 3. Fallback: Download pre-compiled release binary from GitHub Releases
  const releaseBase = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  const releaseUrl = `${releaseBase}/${binName}`;
  console.log(`[litepsm] Downloading native binary from GitHub Releases: ${releaseUrl}...`);
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
          console.warn(`[litepsm] Checksum mismatch for ${binName} (expected ${expected}, got ${actual}); aborting install.`);
          return;
        }
        console.log(`[litepsm] Verified SHA-256 checksum for ${binName}`);
      } else {
        console.warn(`[litepsm] Warning: no checksum entry for ${binName}; proceeding unverified.`);
      }
    } catch (verifyErr) {
      console.warn(`[litepsm] Warning: could not verify checksum (${verifyErr.message}).`);
    }

    fs.renameSync(tempTarget, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    console.log(`[litepsm] Successfully downloaded and installed ${binName} to ${targetPath}`);
  } catch (err) {
    console.warn(`[litepsm] Notice: Could not download native binary (${err.message}).`);
    console.warn(`[litepsm] LitePSM will resolve or re-attempt on first invocation.`);
  }
}

if (require.main === module) {
  installBinary().catch((err) => {
    console.warn(`[litepsm] Postinstall notice: ${err.message}`);
  });
}

module.exports = { installBinary, computeFileSHA256, checksumFor };
