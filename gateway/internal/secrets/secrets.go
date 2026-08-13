package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Box is an AES-256-GCM envelope encryptor. The data key is injected via env at
// deploy time and is never committed.
type Box struct {
	gcm  cipher.AEAD
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
