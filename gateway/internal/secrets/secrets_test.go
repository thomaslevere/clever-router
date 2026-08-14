package secrets

import (
	"testing"
)

func TestSecretsEncryptionAndDecryption(t *testing.T) {
	keyHex := GenerateRandomHex(32)
	box, err := New(keyHex)
	if err != nil {
		t.Fatalf("failed to create secrets box: %v", err)
	}

	rawSecret := "MySuperSecretPassword123!@#"

	// Encrypt
	encVal, err := EncryptValue(box, rawSecret)
	if err != nil {
		t.Fatalf("EncryptValue failed: %v", err)
	}
	if !IsEncrypted(encVal) {
		t.Fatalf("expected IsEncrypted(%q) to be true", encVal)
	}

	// Decrypt
	decVal, err := DecryptValue(box, encVal)
	if err != nil {
		t.Fatalf("DecryptValue failed: %v", err)
	}
	if decVal != rawSecret {
		t.Fatalf("expected decrypted value %q, got %q", rawSecret, decVal)
	}

	// Non-encrypted value pass-through
	plainVal := "http://proxy.internal:8080"
	if IsEncrypted(plainVal) {
		t.Fatalf("expected IsEncrypted(%q) to be false", plainVal)
	}
	decPlain, err := DecryptValue(box, plainVal)
	if err != nil {
		t.Fatalf("DecryptValue on plain value failed: %v", err)
	}
	if decPlain != plainVal {
		t.Fatalf("expected plain value unchanged, got %q", decPlain)
	}
}

func TestRandomGenerators(t *testing.T) {
	h := GenerateRandomHex(32)
	if len(h) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(h))
	}

	b := GenerateRandomBase64(48)
	if len(b) < 48 {
		t.Fatalf("expected at least 48 characters base64, got %d", len(b))
	}
}
