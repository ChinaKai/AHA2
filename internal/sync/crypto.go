package sync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"golang.org/x/crypto/argon2"
)

type encryptedEnvelope struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func EncryptBundle(plaintext []byte, passphrase string) ([]byte, error) {
	if len(plaintext) == 0 || passphrase == "" {
		return nil, errors.New("plaintext and passphrase are required")
	}
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte("AHA2-PROFILE-SYNC-V1"))
	return json.Marshal(encryptedEnvelope{Version: 1, KDF: "argon2id", Salt: base64.RawStdEncoding.EncodeToString(salt), Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext)})
}

func DecryptBundle(payload []byte, passphrase string) ([]byte, error) {
	var envelope encryptedEnvelope
	if json.Unmarshal(payload, &envelope) != nil || envelope.Version != 1 || envelope.KDF != "argon2id" || passphrase == "" {
		return nil, errors.New("invalid encrypted bundle")
	}
	salt, err := base64.RawStdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) != 16 {
		return nil, errors.New("invalid encrypted bundle salt")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != 12 {
		return nil, errors.New("invalid encrypted bundle nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid encrypted bundle ciphertext")
	}
	key := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte("AHA2-PROFILE-SYNC-V1"))
	if err != nil {
		return nil, errors.New("decrypt encrypted bundle")
	}
	return plaintext, nil
}
