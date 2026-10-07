package receipts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

func nextID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "invocation_" + strings.ToLower(crockford.EncodeToString(b[:]))
}

// DigestOf returns "sha256:<hex>" for canonical bytes; callers use it for
// input/output digests without importing hash plumbing.
func DigestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
