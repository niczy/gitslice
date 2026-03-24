package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const agentKeyAlgorithmEd25519 = "ed25519"

func agentKeyFingerprint(algorithm string, publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return strings.ToLower(strings.TrimSpace(algorithm)) + ":" + hex.EncodeToString(sum[:])
}

func generateAgentPrivateKeyFile(privateKeyPath string) (ed25519.PublicKey, ed25519.PrivateKey, string, error) {
	privateKeyPath = strings.TrimSpace(privateKeyPath)
	if privateKeyPath == "" {
		return nil, nil, "", errors.New("private key path is required")
	}
	publicKeyPath := privateKeyPath + ".pub"
	if err := ensureMissingPath(privateKeyPath); err != nil {
		return nil, nil, "", err
	}
	if err := ensureMissingPath(publicKeyPath); err != nil {
		return nil, nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0o755); err != nil {
		return nil, nil, "", err
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, "", err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, nil, "", err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	if err := os.WriteFile(privateKeyPath, privatePEM, 0o600); err != nil {
		return nil, nil, "", err
	}
	if err := os.WriteFile(publicKeyPath, publicPEM, 0o644); err != nil {
		return nil, nil, "", err
	}
	return publicKey, privateKey, publicKeyPath, nil
}

func ensureMissingPath(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func loadAgentPrivateKey(path string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	privateKey, err := parsePEMPrivateKey(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse private key %s: %w", path, err)
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, nil, errors.New("private key does not contain an ed25519 public key")
	}
	return privateKey, publicKey, nil
}

func loadAgentPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	publicKey, err := parsePEMPublicKey(data)
	if err == nil {
		return publicKey, nil
	}
	privateKey, _, privateErr := parsePEMPrivateKeyWithPublic(data)
	if privateErr == nil {
		publicKey, ok := privateKey.Public().(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("private key does not contain an ed25519 public key")
		}
		return publicKey, nil
	}
	return nil, fmt.Errorf("parse public key %s: %w", path, err)
}

func parsePEMPrivateKey(data []byte) (ed25519.PrivateKey, error) {
	privateKey, _, err := parsePEMPrivateKeyWithPublic(data)
	return privateKey, err
}

func parsePEMPrivateKeyWithPublic(data []byte) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	rest := data
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			continue
		}
		privateKey, ok := keyAny.(ed25519.PrivateKey)
		if !ok {
			return nil, nil, errors.New("unsupported private key type")
		}
		publicKey, ok := privateKey.Public().(ed25519.PublicKey)
		if !ok {
			return nil, nil, errors.New("private key does not contain an ed25519 public key")
		}
		return privateKey, publicKey, nil
	}
	return nil, nil, errors.New("no ed25519 private key found")
}

func parsePEMPublicKey(data []byte) (ed25519.PublicKey, error) {
	rest := data
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		keyAny, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			continue
		}
		publicKey, ok := keyAny.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("unsupported public key type")
		}
		return publicKey, nil
	}
	return nil, errors.New("no ed25519 public key found")
}
