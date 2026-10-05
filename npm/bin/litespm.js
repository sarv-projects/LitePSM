#!/usr/bin/env node

const { spawn, spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const os = require("os");

const VERSION = require("../package.json").version;

function getBinaryName() {
  const platform = process.platform;
  const arch = process.arch;

  let osName = "";
  if (platform === "win32") osName = "windows";
  else if (platform === "darwin") osName = "darwin";
  else if (platform === "linux") osName = "linux";
  else throw new Error(`Unsupported OS platform: ${platform}`);

  let archName = "";
  if (arch === "x64") archName = "amd64";
  else if (arch === "arm64") archName = "arm64";
  else throw new Error(`Unsupported CPU architecture: ${arch}`);

  const ext = platform === "win32" ? ".exe" : "";
  return `litespm-${osName}-${archName}${ext}`;
}

function resolveBinary() {
  const binName = getBinaryName();
  
  // 1. Check inside npm package bin/ or dist/
  const candidates = [
    path.join(__dirname, binName),
    path.join(__dirname, "..", "dist", binName),
    path.join(__dirname, "..", "..", "dist", binName),
    path.join(os.homedir(), ".litespm", "bin", binName),
  ];

  for (const c of candidates) {
    if (fs.existsSync(c)) {
      return c;
    }
  }

  // 2. Fallback to pre-installed system binary
  const fallback = spawnSync(process.platform === "win32" ? "where" : "which", ["litespm"], { encoding: "utf8" });
  if (fallback.status === 0 && fallback.stdout.trim()) {
    const sysPath = fallback.stdout.split("\n")[0].trim();
    if (sysPath && sysPath !== __filename) {
      return sysPath;
    }
  }

  return null;
}

function main() {
  let binaryPath = resolveBinary();

  if (!binaryPath) {
    // Attempt on-demand download / setup
    try {
      const installScript = path.join(__dirname, "..", "scripts", "install-binary.js");
      if (fs.existsSync(installScript)) {
        spawnSync(process.execPath, [installScript], { stdio: "inherit" });
        binaryPath = resolveBinary();
      }
    } catch (e) {
      // Ignored
    }
  }

  if (!binaryPath) {
    console.error(`[litespm] Error: Native binary not found for ${process.platform}/${process.arch}.`);
    console.error(`[litespm] Please download ${getBinaryName()} from https://github.com/sarv-projects/LiteSPM/releases/tag/v${VERSION}`);
    process.exit(1);
  }

  const child = spawn(binaryPath, process.argv.slice(2), {
    stdio: "inherit",
    env: process.env,
  });

  child.on("error", (err) => {
    console.error(`[litespm] Failed to spawn native binary: ${err.message}`);
    process.exit(1);
  });

  child.on("exit", (code, signal) => {
    if (signal) {
      process.kill(process.pid, signal);
    } else {
      process.exit(code || 0);
    }
  });
}

if (require.main === module) {
  main();
}

module.exports = { getBinaryName, resolveBinary };
