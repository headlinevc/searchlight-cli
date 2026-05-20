package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

type PKCE struct {
	Verifier  string
	Challenge string
	Method    string
}

// NewPKCE returns a 43-byte verifier (256 bits of entropy) and its S256 challenge.
// Length is intentional: the eva-web authorization_service.rb requires the S256
// challenge to be exactly 43 base64url chars (i.e. 32 raw bytes), matching RFC 7636.
func NewPKCE() (*PKCE, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("pkce: read random: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return &PKCE{Verifier: verifier, Challenge: challenge, Method: "S256"}, nil
}

func RandomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
