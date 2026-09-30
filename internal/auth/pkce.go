package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// PKCEPair holds a generated code verifier and code challenge per RFC 7636.
type PKCEPair struct {
	Verifier  string `json:"verifier"`
	Challenge string `json:"challenge"`
	Method    string `json:"method"` // Always "S256"
}

// GeneratePKCE generates an RFC 7636 compliant PKCE code verifier and SHA-256 code challenge.
func GeneratePKCE() (*PKCEPair, error) {
	// Generate 32 bytes of cryptographically secure randomness
	randomBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, randomBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes for PKCE: %w", err)
	}

	verifier := base64.RawURLEncoding.EncodeToString(randomBytes)

	// Compute SHA-256 hash of the verifier
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEPair{
		Verifier:  verifier,
		Challenge: challenge,
		Method:    "S256",
	}, nil
}

// VerifyPKCE verifies that a given verifier produces the expected challenge.
func VerifyPKCE(verifier, expectedChallenge string) bool {
	hash := sha256.Sum256([]byte(verifier))
	actual := base64.RawURLEncoding.EncodeToString(hash[:])
	return actual == expectedChallenge
}
