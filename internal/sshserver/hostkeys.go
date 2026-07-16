package sshserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

func generateAndSave(keyPath, keyType string) error {
	fmt.Printf("Generating %s host key...\n", keyType)

	var key any
	var err error
	if keyType == "rsa" {
		key, err = rsa.GenerateKey(rand.Reader, 4096)
	} else {
		_, key, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		return err
	}

	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600)
}

func EnsureHostKeys(configDir string) ([]ssh.Signer, error) {
	keys := []struct{ file, keyType string }{
		{"id_rsa", "rsa"},
		{"id_ed25519", "ed25519"},
	}

	signers := make([]ssh.Signer, 0, len(keys))
	for _, k := range keys {
		keyPath := filepath.Join(configDir, k.file)
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			if err := generateAndSave(keyPath, k.keyType); err != nil {
				return nil, fmt.Errorf("failed to generate %s host key: %w", k.keyType, err)
			}
		}
		raw, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		signer, err := ssh.ParsePrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse host key %q: %w", keyPath, err)
		}
		signers = append(signers, signer)
	}
	return signers, nil
}
