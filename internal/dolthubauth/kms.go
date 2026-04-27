package dolthubauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/api/option"
)

const kmsEnvelopePayloadVersion = 1

type kmsEnvelopeClient interface {
	Encrypt(context.Context, *kmspb.EncryptRequest) (*kmspb.EncryptResponse, error)
	Decrypt(context.Context, *kmspb.DecryptRequest) (*kmspb.DecryptResponse, error)
	GetCryptoKey(context.Context, *kmspb.GetCryptoKeyRequest) (*kmspb.CryptoKey, error)
	Close() error
}

type realKMSEnvelopeClient struct {
	*kms.KeyManagementClient
}

func (c realKMSEnvelopeClient) Encrypt(ctx context.Context, req *kmspb.EncryptRequest) (*kmspb.EncryptResponse, error) {
	return c.KeyManagementClient.Encrypt(ctx, req)
}

func (c realKMSEnvelopeClient) Decrypt(ctx context.Context, req *kmspb.DecryptRequest) (*kmspb.DecryptResponse, error) {
	return c.KeyManagementClient.Decrypt(ctx, req)
}

func (c realKMSEnvelopeClient) GetCryptoKey(ctx context.Context, req *kmspb.GetCryptoKeyRequest) (*kmspb.CryptoKey, error) {
	return c.KeyManagementClient.GetCryptoKey(ctx, req)
}

// GCPKMSEnvelopeCipher encrypts credentials with a local DEK and wraps the DEK
// with Google Cloud KMS.
type GCPKMSEnvelopeCipher struct {
	client   kmsEnvelopeClient
	keyName  string
	dekCache *kmsDEKCache
}

type kmsEnvelopePayload struct {
	Version    int    `json:"version"`
	WrappedDEK []byte `json:"wrapped_dek"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// NewGCPKMSEnvelopeCipher constructs a KMS-backed envelope encryption cipher.
func NewGCPKMSEnvelopeCipher(ctx context.Context, keyName, credentialsJSON string) (*GCPKMSEnvelopeCipher, error) {
	keyName = strings.TrimSpace(keyName)
	if keyName == "" {
		return nil, fmt.Errorf("DOLTHUB_AUTH_KMS_KEY_NAME is required")
	}

	opts := []option.ClientOption{}
	if strings.TrimSpace(credentialsJSON) != "" {
		opts = append(opts, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(credentialsJSON)))
	}
	client, err := kms.NewKeyManagementClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("open gcp kms client: %w", err)
	}
	return newGCPKMSEnvelopeCipherWithClient(realKMSEnvelopeClient{KeyManagementClient: client}, keyName)
}

func newGCPKMSEnvelopeCipherWithClient(client kmsEnvelopeClient, keyName string) (*GCPKMSEnvelopeCipher, error) {
	keyName = strings.TrimSpace(keyName)
	if keyName == "" {
		return nil, fmt.Errorf("DOLTHUB_AUTH_KMS_KEY_NAME is required")
	}
	if client == nil {
		return nil, fmt.Errorf("kms client is required")
	}
	return &GCPKMSEnvelopeCipher{
		client:   client,
		keyName:  keyName,
		dekCache: newKMSDEKCache(5*time.Minute, 1024),
	}, nil
}

// Check verifies the configured crypto key is reachable.
func (c *GCPKMSEnvelopeCipher) Check(ctx context.Context) error {
	_, err := c.client.GetCryptoKey(ctx, &kmspb.GetCryptoKeyRequest{Name: c.keyName})
	if err != nil {
		return fmt.Errorf("get crypto key: %w", err)
	}
	return nil
}

// Encrypt generates a DEK, wraps it with KMS, and encrypts the credential with AES-GCM.
func (c *GCPKMSEnvelopeCipher) Encrypt(ctx context.Context, plaintext []byte) ([]byte, string, string, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, "", "", fmt.Errorf("generate dek: %w", err)
	}
	defer clearBytes(dek)

	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, "", "", fmt.Errorf("construct cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", "", fmt.Errorf("construct gcm: %w", err)
	}

	wrapped, err := c.client.Encrypt(ctx, &kmspb.EncryptRequest{
		Name:      c.keyName,
		Plaintext: dek,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("wrap dek: %w", err)
	}
	keyVersion := strings.TrimSpace(wrapped.GetName())
	if keyVersion == "" {
		keyVersion = c.keyName
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", "", fmt.Errorf("generate nonce: %w", err)
	}

	payload := kmsEnvelopePayload{
		Version:    kmsEnvelopePayloadVersion,
		WrappedDEK: append([]byte(nil), wrapped.GetCiphertext()...),
		Nonce:      append([]byte(nil), nonce...),
		Ciphertext: gcm.Seal(nil, nonce, plaintext, kmsEnvelopeAAD(keyVersion)),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", "", fmt.Errorf("encode envelope payload: %w", err)
	}
	return encoded, keyVersion, kmsEnvelopeEncryptionBackend, nil
}

// Decrypt unwraps the DEK with KMS and decrypts the stored envelope payload.
func (c *GCPKMSEnvelopeCipher) Decrypt(ctx context.Context, ciphertext []byte, keyVersion, backend string) ([]byte, error) {
	if backend != kmsEnvelopeEncryptionBackend {
		return nil, fmt.Errorf("unsupported encryption backend %q", backend)
	}

	var payload kmsEnvelopePayload
	if err := json.Unmarshal(ciphertext, &payload); err != nil {
		return nil, fmt.Errorf("decode envelope payload: %w", err)
	}
	if payload.Version != kmsEnvelopePayloadVersion {
		return nil, fmt.Errorf("unsupported envelope payload version %d", payload.Version)
	}

	keyVersion = strings.TrimSpace(keyVersion)
	if keyVersion == "" {
		keyVersion = c.keyName
	}

	cacheKey := kmsEnvelopeDEKCacheKey(keyVersion, payload.WrappedDEK)
	dek, ok := c.dekCache.Get(cacheKey)
	if !ok {
		unwrapped, err := c.client.Decrypt(ctx, &kmspb.DecryptRequest{
			Name:       kmsEnvelopeCryptoKeyName(keyVersion, c.keyName),
			Ciphertext: payload.WrappedDEK,
		})
		if err != nil {
			return nil, fmt.Errorf("unwrap dek: %w", err)
		}
		dek = append([]byte(nil), unwrapped.GetPlaintext()...)
		c.dekCache.Set(cacheKey, dek)
	}
	defer clearBytes(dek)

	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("construct cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("construct gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, payload.Nonce, payload.Ciphertext, kmsEnvelopeAAD(keyVersion))
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	return plaintext, nil
}

// Close releases the underlying KMS client.
func (c *GCPKMSEnvelopeCipher) Close() error {
	return c.client.Close()
}

func kmsEnvelopeCryptoKeyName(keyVersion, fallback string) string {
	keyVersion = strings.TrimSpace(keyVersion)
	if idx := strings.LastIndex(keyVersion, "/cryptoKeyVersions/"); idx > 0 {
		return keyVersion[:idx]
	}
	return fallback
}

func kmsEnvelopeAAD(keyVersion string) []byte {
	return []byte("wasteland:dolthub-auth:gcp-kms-envelope:" + strings.TrimSpace(keyVersion))
}

func kmsEnvelopeDEKCacheKey(keyVersion string, wrappedDEK []byte) string {
	sum := sha256.Sum256(wrappedDEK)
	return strings.TrimSpace(keyVersion) + ":" + fmt.Sprintf("%x", sum[:])
}

type kmsDEKCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]kmsDEKCacheEntry
}

type kmsDEKCacheEntry struct {
	dek       []byte
	expiresAt time.Time
	usedAt    time.Time
}

func newKMSDEKCache(ttl time.Duration, maxEntries int) *kmsDEKCache {
	if ttl <= 0 || maxEntries <= 0 {
		return nil
	}
	return &kmsDEKCache{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[string]kmsDEKCacheEntry),
	}
}

func (c *kmsDEKCache) Get(key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if now.After(entry.expiresAt) {
		clearBytes(entry.dek)
		delete(c.entries, key)
		return nil, false
	}
	entry.usedAt = now
	c.entries[key] = entry
	return append([]byte(nil), entry.dek...), true
}

func (c *kmsDEKCache) Set(key string, dek []byte) {
	if c == nil || key == "" || len(dek) == 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[key]; ok {
		clearBytes(old.dek)
	}
	c.entries[key] = kmsDEKCacheEntry{
		dek:       append([]byte(nil), dek...),
		expiresAt: now.Add(c.ttl),
		usedAt:    now,
	}
	c.evictLocked(now)
}

func (c *kmsDEKCache) evictLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			clearBytes(entry.dek)
			delete(c.entries, key)
		}
	}
	for len(c.entries) > c.maxEntries {
		var oldestKey string
		var oldestAt time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.usedAt.Before(oldestAt) {
				oldestKey = key
				oldestAt = entry.usedAt
			}
		}
		entry := c.entries[oldestKey]
		clearBytes(entry.dek)
		delete(c.entries, oldestKey)
	}
}

func clearBytes(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
