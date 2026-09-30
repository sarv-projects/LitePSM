#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const os = require("os");
const crypto = require("crypto");
const https = require("https");
const { getBinaryName } = require("../bin/litepsm.js");

const VERSION = "0.1.0";

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

function installBinary() {
  const binName = getBinaryName();
  const targetDir = path.join(os.homedir(), ".litepsm", "bin");
  ensureDir(targetDir);
  const targetPath = path.join(targetDir, binName);

  // If already installed, skip
  if (fs.existsSync(targetPath)) {
    return;
  }

  // Check if dist/ contains the pre-compiled binary (e.g. during local build or release)
  const localDist = path.join(__dirname, "..", "..", "dist", binName);
  if (fs.existsSync(localDist)) {
    fs.copyFileSync(localDist, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    console.log(`[litepsm] Installed ${binName} to ${targetPath}`);
    return;
  }

  console.log(`[litepsm] Native binary ${binName} will be acquired on first invocation or release download.`);
}

if (require.main === module) {
  try {
    installBinary();
  } catch (err) {
    console.warn(`[litepsm] Postinstall notice: ${err.message}`);
  }
}

module.exports = { installBinary, computeFileSHA256 };
