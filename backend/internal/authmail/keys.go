// Package authmail owns mailbox proofs for registration and password recovery:
// challenges, keyed digests, sealed mail jobs, the delivery worker, shared database
// rate limits and cleanup. API handlers only translate input and stable errors.
package authmail

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Purposes bound into every digest and challenge row.
const (
	PurposeRegister = "register"
	PurposeReset    = "reset"
)

// Keys are independent sub-keys derived from AUTH_MAIL_SECRET with distinct labels,
// so a digest key can never decrypt payloads and vice versa.
type Keys struct {
	code, token, rate, payload []byte
}

// DeriveKeys expands the configured secret. The secret must not be JWT or node key material.
func DeriveKeys(secret []byte) (Keys, error) {
	if len(secret) < 32 {
		return Keys{}, errors.New("auth mail secret too short")
	}
	derive := func(label string) []byte {
		out := make([]byte, 32)
		if _, err := io.ReadFull(hkdf.New(sha256.New, secret, nil, []byte("asterlink/authmail/"+label+"/v1")), out); err != nil {
			panic(err) // HKDF-SHA256 cannot fail for 32 bytes of output.
		}
		return out
	}
	return Keys{code: derive("code-digest"), token: derive("token-digest"), rate: derive("rate-key"), payload: derive("payload-aead")}, nil
}

func mac(key []byte, parts ...string) string {
	h := hmac.New(sha256.New, key)
	for _, part := range parts {
		// Length-prefix every field so ("ab","c") and ("a","bc") never collide.
		var n [4]byte
		n[0], n[1], n[2], n[3] = byte(len(part)>>24), byte(len(part)>>16), byte(len(part)>>8), byte(len(part))
		h.Write(n[:])
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CodeDigest binds a six-digit code to its purpose, mailbox and challenge, so the
// tiny code space cannot be enumerated offline from a database copy.
func (k Keys) CodeDigest(purpose, email, challengeID, code string) string {
	return mac(k.code, purpose, email, challengeID, code)
}

// TokenDigest is the unique lookup key for a long random reset token.
func (k Keys) TokenDigest(purpose, token string) string { return mac(k.token, purpose, token) }

// RateKey hides raw emails and addresses inside rate-limit rows.
func (k Keys) RateKey(kind, value string) string { return mac(k.rate, kind, value) }

// DigestEqual compares digests in constant time.
func DigestEqual(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

// Seal encrypts a job payload with AES-256-GCM, authenticating the job ID and template
// so a ciphertext cannot be replayed into another job or another kind of email.
func (k Keys) Seal(jobID, template string, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(k.payload)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, []byte(jobID+"\x00"+template)), nil
}

// Open reverses Seal; any tampering or mismatched binding fails.
func (k Keys) Open(jobID, template string, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(k.payload)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("sealed payload too short")
	}
	return aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(jobID+"\x00"+template))
}
