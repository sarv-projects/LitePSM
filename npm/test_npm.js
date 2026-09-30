const assert = require("assert");
const { getBinaryName, resolveBinary } = require("./bin/litepsm.js");

console.log("Testing npm wrapper resolution...");

// 1. Check getBinaryName returns valid string
const binName = getBinaryName();
assert(typeof binName === "string" && binName.startsWith("litepsm-"), "binary name must start with litepsm-");
console.log(`✓ Detected binary name: ${binName}`);

// 2. Check resolveBinary finds pre-compiled binary from dist/
const binPath = resolveBinary();
assert(binPath !== null, "resolveBinary should locate binary in dist or system path");
console.log(`✓ Resolved binary path: ${binPath}`);

console.log("All npm wrapper tests passed!");
