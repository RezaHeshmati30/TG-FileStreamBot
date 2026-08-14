package secureproxy

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"EverythingSuckz/fsb/config"
)

const TokenLifetime = 24 * time.Hour

var (
	ErrInvalidToken = errors.New("invalid proxy token")
	ErrExpiredToken = errors.New("proxy token expired")
)

type tokenPayload struct {
	Version int    `json:"v"`
	URL     string `json:"u"`
	Expires int64  `json:"e"`
}

type Target struct {
	URL     string
	Expires int64
}

func EncryptTarget(targetURL string, expires int64) (string, error) {
	aead, err := tokenAEAD()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(tokenPayload{Version: 1, URL: targetURL, Expires: expires})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, payload, []byte("fsb-secure-proxy-v1"))
	return base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func DecryptTarget(token string, now time.Time) (Target, error) {
	if len(token) == 0 || len(token) > 4096 {
		return Target{}, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Target{}, ErrInvalidToken
	}
	aead, err := tokenAEAD()
	if err != nil {
		return Target{}, err
	}
	if len(raw) <= aead.NonceSize() {
		return Target{}, ErrInvalidToken
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("fsb-secure-proxy-v1"))
	if err != nil {
		return Target{}, ErrInvalidToken
	}
	var payload tokenPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil || payload.Version != 1 || payload.URL == "" || payload.Expires <= 0 {
		return Target{}, ErrInvalidToken
	}
	if now.Unix() > payload.Expires {
		return Target{}, ErrExpiredToken
	}
	return Target{URL: payload.URL, Expires: payload.Expires}, nil
}

func tokenAEAD() (cipher.AEAD, error) {
	if len(config.ValueOf.LinkSigningKey) < 32 {
		return nil, fmt.Errorf("LINK_SIGNING_KEY is too short")
	}
	key := sha256.Sum256([]byte("secure-proxy-token:" + config.ValueOf.LinkSigningKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
