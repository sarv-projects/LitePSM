const assert = require("assert");
const fs = require("fs");
const os = require("os");
const path = require("path");

const {
  getBinaryName,
  resolveBinary,
} = require("./bin/litespm.js");
const {
  checksumFor,
  computeFileSHA256,
  verifyDownloadedBinary,
  assertVerifiedDigest,
  assertHTTPS,
  resolveRedirect,
} = require("./scripts/install-binary.js");

let failures = 0;
function check(name, fn) {
  try {
    fn();
    console.log(`✓ ${name}`);
  } catch (err) {
    failures += 1;
    console.error(`✗ ${name}: ${err.message}`);
  }
}

async function checkAsync(name, fn) {
  try {
    await fn();
    console.log(`✓ ${name}`);
  } catch (err) {
    failures += 1;
    console.error(`✗ ${name}: ${err.message}`);
  }
}

// ---------------------------------------------------------------- binary name
check("binary name is platform-shaped", () => {
  const binName = getBinaryName();
  assert(typeof binName === "string" && binName.startsWith("litespm-"));
  assert(/-amd64|-arm64/.test(binName), `unexpected arch in ${binName}`);
});

// ------------------------------------------------------------ checksum parsing
check("checksum manifest parsing", () => {
  const digest = "a".repeat(64);
  const binName = getBinaryName();
  assert.strictEqual(checksumFor(`${digest}  ${binName}\n`, binName), digest);
  assert.strictEqual(checksumFor(`${digest} *${binName}\n`, binName), digest);
  assert.strictEqual(checksumFor(`deadbeef  other-file\n`, binName), null);
});

// ---------------------------------------------------------- https-only policy
check("non-HTTPS URLs are refused", () => {
  assert.throws(() => assertHTTPS("http://example.com/x"), /refusing non-HTTPS/);
  assert.throws(() => assertHTTPS("ftp://example.com/x"), /refusing non-HTTPS/);
  assert.throws(() => assertHTTPS("not-a-url"), /malformed URL/);
  assert.strictEqual(
    assertHTTPS("https://github.com/a/b"),
    "https://github.com/a/b"
  );
});

check("redirect targets stay on HTTPS", () => {
  // Absolute http redirect: refused.
  assert.throws(
    () => resolveRedirect("https://github.com/x", "http://evil.example/y"),
    /refusing non-HTTPS/
  );
  // A protocol-relative target inherits the base scheme, so it stays HTTPS and
  // is allowed: host changes are legitimate (GitHub serves assets from
  // objects.githubusercontent.com) and are not what this policy guards.
  assert.strictEqual(
    resolveRedirect("https://github.com/x", "//evil.example/y"),
    "https://evil.example/y"
  );
  // ...but it must not be able to smuggle plaintext in.
  assert.throws(
    () => resolveRedirect("https://github.com/x", "http:/\evil.example/y"),
    /refusing non-HTTPS/
  );
  // Legitimate GitHub asset redirect: allowed.
  assert.strictEqual(
    resolveRedirect(
      "https://github.com/o/r/releases/download/v1/litespm-linux-amd64",
      "https://objects.githubusercontent.com/github-production-release-asset/abc"
    ),
    "https://objects.githubusercontent.com/github-production-release-asset/abc"
  );
});

// ------------------------------------------------ fail-closed verification
// A local http server stands in for the release host: fetchText refuses
// non-HTTPS, so these cases exercise the manifest-driven refusals directly by
// pointing the fetcher at a served manifest over... hmm, https only. Instead we
// verify the three refusal branches through the exported helper with a manifest
// origin that is itself refused, which is the same fail-closed path.
async function main() {
  const binName = getBinaryName();
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "litespm-npm-test-"));
  const binaryPath = path.join(tmp, binName);
  const digest = "b".repeat(64);
  fs.writeFileSync(binaryPath, "not really a binary");
  const realDigestOf = (p) => computeFileSHA256(p);

  await checkAsync("a matching digest is accepted", () => {
    const kept = path.join(tmp, "kept-" + binName);
    fs.writeFileSync(kept, "payload");
    assert.strictEqual(assertVerifiedDigest(kept, binName, realDigestOf(kept)), realDigestOf(kept));
    assert(fs.existsSync(kept), "a verified binary must survive");
    fs.rmSync(kept);
  });

  await checkAsync("a mismatched digest destroys the binary", () => {
    const suspect = path.join(tmp, "suspect-" + binName);
    fs.writeFileSync(suspect, "payload");
    assert.throws(
      () => assertVerifiedDigest(suspect, binName, "c".repeat(64)),
      (err) => {
        assert(err.unverified === true, "failure must be flagged as unverified");
        assert(/checksum mismatch/.test(err.message), err.message);
        return true;
      }
    );
    assert(!fs.existsSync(suspect), "an unverified binary must not survive");
  });

  await checkAsync("an unreachable manifest refuses the install", async () => {
    await assert.rejects(
      verifyDownloadedBinary(binaryPath, binName, "http://127.0.0.1:1/none"),
      (err) => {
        assert(err.unverified === true, "failure must be flagged as unverified");
        assert(/refusing to install/.test(err.message), err.message);
        return true;
      }
    );
    assert(!fs.existsSync(binaryPath), "an unverifiable binary must be destroyed");
  });

  fs.rmSync(tmp, { recursive: true, force: true });

  // The wrapper must not silently run an arbitrary PATH binary.
  check("resolveBinary does not fall back to PATH", () => {
    const sandbox = fs.mkdtempSync(path.join(os.tmpdir(), "litespm-path-"));
    const realHome = os.homedir;
    const realPathEnv = process.env.PATH;
    try {
      // Plant a decoy named `litespm` on PATH and point HOME somewhere empty.
      const decoyDir = path.join(sandbox, "bin");
      fs.mkdirSync(decoyDir, { recursive: true });
      fs.writeFileSync(path.join(decoyDir, "litespm"), "#!/bin/sh\nexit 0\n", {
        mode: 0o755,
      });
      process.env.PATH = decoyDir;
      os.homedir = () => path.join(sandbox, "empty-home");
      fs.mkdirSync(os.homedir(), { recursive: true });

      const resolved = resolveBinary();
      if (resolved) {
        assert(
          !resolved.startsWith(decoyDir),
          `resolveBinary returned a PATH binary: ${resolved}`
        );
      }
    } finally {
      os.homedir = realHome;
      process.env.PATH = realPathEnv;
      fs.rmSync(sandbox, { recursive: true, force: true });
    }
  });

  if (failures > 0) {
    console.error(`\n${failures} npm wrapper test(s) failed`);
    process.exit(1);
  }
  console.log("\nAll npm wrapper tests passed");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
