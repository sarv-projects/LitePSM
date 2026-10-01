#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const os = require("os");
const crypto = require("crypto");
const https = require("https");
const { getBinaryName } = require("../bin/litepsm.js");

const VERSION = "0.1.0";
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
  const releaseUrl = `https://github.com/${REPO}/releases/download/v${VERSION}/${binName}`;
  console.log(`[litepsm] Downloading native binary from GitHub Releases: ${releaseUrl}...`);
  try {
    const tempTarget = `${targetPath}.tmp.${Date.now()}`;
    await downloadFile(releaseUrl, tempTarget);
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

module.exports = { installBinary, computeFileSHA256 };
