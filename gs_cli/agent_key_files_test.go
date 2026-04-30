package gscli

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndLoadAgentKeyFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	privateKeyPath := filepath.Join(dir, "agent_ed25519")
	publicKey, privateKey, publicKeyPath, err := generateAgentPrivateKeyFile(privateKeyPath)
	if err != nil {
		t.Fatalf("generateAgentPrivateKeyFile failed: %v", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("unexpected public key length: %d", len(publicKey))
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		t.Fatalf("unexpected private key length: %d", len(privateKey))
	}
	if publicKeyPath != privateKeyPath+".pub" {
		t.Fatalf("unexpected public key path: %s", publicKeyPath)
	}

	privateInfo, err := os.Stat(privateKeyPath)
	if err != nil {
		t.Fatalf("Stat private key failed: %v", err)
	}
	if privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected private key mode: %o", privateInfo.Mode().Perm())
	}

	loadedPrivate, loadedPublic, err := loadAgentPrivateKey(privateKeyPath)
	if err != nil {
		t.Fatalf("loadAgentPrivateKey failed: %v", err)
	}
	if string(loadedPrivate) != string(privateKey) {
		t.Fatal("loaded private key mismatch")
	}
	if string(loadedPublic) != string(publicKey) {
		t.Fatal("loaded public key mismatch")
	}

	loadedPublicOnly, err := loadAgentPublicKey(publicKeyPath)
	if err != nil {
		t.Fatalf("loadAgentPublicKey failed: %v", err)
	}
	if string(loadedPublicOnly) != string(publicKey) {
		t.Fatal("loaded public-only key mismatch")
	}
}
