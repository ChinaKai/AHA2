package sync

import (
	"bytes"
	"testing"
)

func TestEncryptedBundleRoundTripAndWrongPassphrase(t *testing.T) {
	plaintext := []byte(`{"provider":"opaque-secret"}`)
	encrypted, err := EncryptBundle(plaintext, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("opaque-secret")) {
		t.Fatal("ciphertext leaked plaintext")
	}
	decrypted, err := DecryptBundle(encrypted, "correct horse battery staple")
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := DecryptBundle(encrypted, "wrong"); err == nil {
		t.Fatal("wrong passphrase decrypted bundle")
	}
}
