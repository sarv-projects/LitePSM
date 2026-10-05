const assert = require("assert");
const { getBinaryName, resolveBinary } = require("./bin/litespm.js");
const { checksumFor, computeFileSHA256 } = require("./scripts/install-binary.js");
const fs = require("fs");
const os = require("os");
const path = require("path");

console.log("Testing npm wrapper resolution...");

// 1. Check getBinaryName returns valid string
const binName = getBinaryName();
assert(typeof binName === "string" && binName.startsWith("litespm-"), "binary name must start with litespm-");
console.log(`✓ Detected binary name: ${binName}`);

// 2. Check resolveBinary finds pre-compiled binary from dist/
const binPath = resolveBinary();
assert(binPath !== null, "resolveBinary should locate binary in dist or system path");
console.log(`✓ Resolved binary path: ${binPath}`);

// 3. Checksum parsing from a sha256sum-style manifest
const digest = "a".repeat(64);
assert.strictEqual(checksumFor(`${digest}  ${binName}\n`, binName), digest, "should parse plain sha256sum line");
assert.strictEqual(checksumFor(`${digest} *${binName}\n`, binName), digest, "should parse binary-mode sha256sum line");
assert.strictEqual(checksumFor(`deadbeef  other-file\n`, binName), null, "should ignore unrelated entries");
console.log("✓ Checksum manifest parsing verified");

// 4. computeFileSHA256 matches a known digest
const tmp = path.join(os.tmpdir(), `litespm-hash-${process.pid}.tmp`);
fs.writeFileSync(tmp, "litespm");
const expected = "sha256:" + require("crypto").createHash("sha256").update("litespm").digest("hex");
assert.strictEqual("sha256:" + computeFileSHA256(tmp), expected, "file hash should match");
fs.unlinkSync(tmp);
console.log("✓ File hashing verified");

console.log("All npm wrapper tests passed!");
