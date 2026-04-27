package dolthubauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cloud.google.com/go/kms/apiv1/kmspb"
)

type fakeKMSEnvelopeClient struct {
	lastEncryptName string
	lastDecryptName string
	lastGetName     string
	versionName     string
	decryptCalls    int
	checkErr        error
	encryptErr      error
	decryptErr      error
}

func (f *fakeKMSEnvelopeClient) Encrypt(_ context.Context, req *kmspb.EncryptRequest) (*kmspb.EncryptResponse, error) {
	if f.encryptErr != nil {
		return nil, f.encryptErr
	}
	f.lastEncryptName = req.GetName()
	return &kmspb.EncryptResponse{
		Name:       f.versionName,
		Ciphertext: append([]byte("wrapped:"), req.GetPlaintext()...),
	}, nil
}

func (f *fakeKMSEnvelopeClient) Decrypt(_ context.Context, req *kmspb.DecryptRequest) (*kmspb.DecryptResponse, error) {
	if f.decryptErr != nil {
		return nil, f.decryptErr
	}
	f.decryptCalls++
	f.lastDecryptName = req.GetName()
	if len(req.GetCiphertext()) < len("wrapped:") || string(req.GetCiphertext()[:len("wrapped:")]) != "wrapped:" {
		return nil, errors.New("unexpected wrapped dek")
	}
	return &kmspb.DecryptResponse{
		Plaintext: append([]byte(nil), req.GetCiphertext()[len("wrapped:"):]...),
	}, nil
}

func (f *fakeKMSEnvelopeClient) GetCryptoKey(_ context.Context, req *kmspb.GetCryptoKeyRequest) (*kmspb.CryptoKey, error) {
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	f.lastGetName = req.GetName()
	return &kmspb.CryptoKey{Name: req.GetName()}, nil
}

func (f *fakeKMSEnvelopeClient) Close() error { return nil }

func TestGCPKMSEnvelopeCipher_RoundTrip(t *testing.T) {
	t.Parallel()

	const keyName = "projects/example-project/locations/us-central1/keyRings/example-ring/cryptoKeys/dolthub-auth-staging"
	client := &fakeKMSEnvelopeClient{
		versionName: keyName + "/cryptoKeyVersions/7",
	}
	cipher, err := newGCPKMSEnvelopeCipherWithClient(client, keyName)
	if err != nil {
		t.Fatalf("newGCPKMSEnvelopeCipherWithClient() error = %v", err)
	}

	if err := cipher.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	encoded, keyVersion, backend, err := cipher.Encrypt(context.Background(), []byte("super-secret-token"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if backend != kmsEnvelopeEncryptionBackend {
		t.Fatalf("backend = %q", backend)
	}
	if keyVersion != client.versionName {
		t.Fatalf("keyVersion = %q", keyVersion)
	}
	if client.lastEncryptName != keyName {
		t.Fatalf("lastEncryptName = %q", client.lastEncryptName)
	}

	plaintext, err := cipher.Decrypt(context.Background(), encoded, keyVersion, backend)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if string(plaintext) != "super-secret-token" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if client.lastDecryptName != keyName {
		t.Fatalf("lastDecryptName = %q", client.lastDecryptName)
	}
	if client.lastGetName != keyName {
		t.Fatalf("lastGetName = %q", client.lastGetName)
	}
}

func TestGCPKMSEnvelopeCipher_DecryptCachesUnwrappedDEK(t *testing.T) {
	t.Parallel()

	const keyName = "projects/example-project/locations/us-central1/keyRings/example-ring/cryptoKeys/dolthub-auth-staging"
	client := &fakeKMSEnvelopeClient{
		versionName: keyName + "/cryptoKeyVersions/7",
	}
	cipher, err := newGCPKMSEnvelopeCipherWithClient(client, keyName)
	if err != nil {
		t.Fatalf("newGCPKMSEnvelopeCipherWithClient() error = %v", err)
	}

	encoded, keyVersion, backend, err := cipher.Encrypt(context.Background(), []byte("super-secret-token"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	for range 2 {
		plaintext, err := cipher.Decrypt(context.Background(), encoded, keyVersion, backend)
		if err != nil {
			t.Fatalf("Decrypt() error = %v", err)
		}
		if string(plaintext) != "super-secret-token" {
			t.Fatalf("plaintext = %q", plaintext)
		}
	}
	if client.decryptCalls != 1 {
		t.Fatalf("decryptCalls = %d, want 1", client.decryptCalls)
	}
}

func TestNewCredentialCipher_KMSPrimaryWithLegacyLocalFallback(t *testing.T) {
	t.Parallel()

	oldFactory := newGCPKMSEnvelopeCipher
	t.Cleanup(func() { newGCPKMSEnvelopeCipher = oldFactory })

	const keyName = "projects/example-project/locations/us-central1/keyRings/example-ring/cryptoKeys/dolthub-auth-staging"
	client := &fakeKMSEnvelopeClient{
		versionName: keyName + "/cryptoKeyVersions/2",
	}
	newGCPKMSEnvelopeCipher = func(context.Context, string, string) (*GCPKMSEnvelopeCipher, error) {
		return newGCPKMSEnvelopeCipherWithClient(client, keyName)
	}

	cfg := validConfig()
	cfg.EncryptionBackend = kmsEnvelopeEncryptionBackend
	cfg.KMSKeyName = keyName

	mixed, err := NewCredentialCipher(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewCredentialCipher() error = %v", err)
	}

	kmsCiphertext, kmsKeyVersion, kmsBackend, err := mixed.Encrypt(context.Background(), []byte("kms-token"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	got, err := mixed.Decrypt(context.Background(), kmsCiphertext, kmsKeyVersion, kmsBackend)
	if err != nil {
		t.Fatalf("Decrypt(kms) error = %v", err)
	}
	if string(got) != "kms-token" {
		t.Fatalf("kms plaintext = %q", got)
	}

	local := &LocalMasterKey{key: cfg.MasterKey}
	localCiphertext, localKeyVersion, localBackend, err := local.Encrypt(context.Background(), []byte("legacy-token"))
	if err != nil {
		t.Fatalf("local Encrypt() error = %v", err)
	}
	got, err = mixed.Decrypt(context.Background(), localCiphertext, localKeyVersion, localBackend)
	if err != nil {
		t.Fatalf("Decrypt(local) error = %v", err)
	}
	if string(got) != "legacy-token" {
		t.Fatalf("local plaintext = %q", got)
	}
}

func TestGCPKMSEnvelopeCipher_RejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	cipher, err := newGCPKMSEnvelopeCipherWithClient(&fakeKMSEnvelopeClient{}, "projects/example-project/locations/us-central1/keyRings/example-ring/cryptoKeys/dolthub-auth-staging")
	if err != nil {
		t.Fatalf("newGCPKMSEnvelopeCipherWithClient() error = %v", err)
	}
	_, err = cipher.Decrypt(context.Background(), []byte("not-json"), "some-version", kmsEnvelopeEncryptionBackend)
	if err == nil || !strings.Contains(err.Error(), "decode envelope payload") {
		t.Fatalf("err = %v", err)
	}
}
