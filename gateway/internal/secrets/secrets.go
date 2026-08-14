package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Box is an AES-256-GCM envelope encryptor. The data key is injected via env at
// deploy time and is never committed.
type Box struct {
	gcm   cipher.AEAD
	keyID string
}

func New(hexKey string) (*Box, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("ENCRYPTION_KEY must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{gcm: gcm, keyID: "env-1"}, nil
}

func (b *Box) Encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return b.gcm.Seal(nonce, nonce, plain, nil), nil
}

func (b *Box) Decrypt(ciphertext []byte) ([]byte, error) {
	ns := b.gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	return b.gcm.Open(nil, nonce, ct, nil)
}

func (b *Box) KeyID() string { return b.keyID }

// GenerateRandomHex returns a hex-encoded string of n cryptographically secure random bytes.
func GenerateRandomHex(n int) string {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// GenerateRandomBase64 returns a base64-encoded string of n cryptographically secure random bytes.
func GenerateRandomBase64(n int) string {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// IsEncrypted returns true if the value starts with the encrypted value prefix.
func IsEncrypted(val string) bool {
	return len(val) > 4 && val[:4] == "enc:"
}

// EncryptValue encrypts a string using Box and returns "enc:<hex_ciphertext>".
func EncryptValue(b *Box, plain string) (string, error) {
	if b == nil {
		return "", errors.New("secrets box is nil")
	}
	ct, err := b.Encrypt([]byte(plain))
	if err != nil {
		return "", err
	}
	return "enc:" + hex.EncodeToString(ct), nil
}

// DecryptValue decrypts a string if it starts with "enc:", otherwise returns the plain value.
func DecryptValue(b *Box, val string) (string, error) {
	if !IsEncrypted(val) {
		return val, nil
	}
	if b == nil {
		return "", errors.New("secrets box is nil")
	}
	rawHex := val[4:]
	ct, err := hex.DecodeString(rawHex)
	if err != nil {
		return "", fmt.Errorf("invalid hex ciphertext: %w", err)
	}
	plain, err := b.Decrypt(ct)
	if err != nil {
		return "", fmt.Errorf("decrypt ciphertext: %w", err)
	}
	return string(plain), nil
}

