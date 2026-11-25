// Package bundlesig signs and verifies released helper binary bundles (`bundle.json`
// + `bundle.sig`) with an Ed25519 release key, following Kyokai's bundle format.
package bundlesig

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manifest lists the product version and lowercase SHA-256 hex digest of each bundled binary.
type Manifest struct {
	Version string            `json:"version"`
	Files   map[string]string `json:"files"`
}

// Sign hashes every binary/helper file in dir and writes `bundle.json` + Ed25519 `bundle.sig`.
//
// Inputs: target bundle directory, release version string, and 64-byte Ed25519 private key.
// Side effects: writes `bundle.json` and `bundle.sig` into dir.
func Sign(dir, version string, priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return errors.New("invalid Ed25519 private key size")
	}
	if strings.TrimSpace(version) == "" {
		return errors.New("version is required")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "bundle.json" || name == "bundle.sig" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".exe" && ext != ".dll" && ext != ".jar" {
			continue
		}
		sum, err := hashFileHex(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		files[name] = sum
	}
	if len(files) == 0 {
		return fmt.Errorf("no signable binaries (.exe/.dll/.jar) found in %s", dir)
	}
	man := Manifest{
		Version: version,
		Files:   files,
	}
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), raw, 0o644); err != nil {
		return err
	}
	sig := ed25519.Sign(priv, raw)
	return os.WriteFile(filepath.Join(dir, "bundle.sig"), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644)
}

// Verify validates `bundle.sig` against `bundle.json` with pub and verifies every listed file digest.
func Verify(dir string, pub ed25519.PublicKey) (Manifest, error) {
	if len(pub) != ed25519.PublicKeySize {
		return Manifest{}, errors.New("invalid Ed25519 public key size")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bundle.json"))
	if err != nil {
		return Manifest{}, err
	}
	sigB64, err := os.ReadFile(filepath.Join(dir, "bundle.sig"))
	if err != nil {
		return Manifest{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil {
		return Manifest{}, fmt.Errorf("decode bundle.sig: %w", err)
	}
	if !ed25519.Verify(pub, raw, sig) {
		return Manifest{}, errors.New("bundle.sig verification failed")
	}
	var man Manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return Manifest{}, err
	}
	if len(man.Files) == 0 {
		return Manifest{}, errors.New("empty bundle manifest")
	}
	for name, wantHex := range man.Files {
		clean := filepath.Base(name)
		if clean != name || clean == "." || clean == ".." {
			return Manifest{}, fmt.Errorf("unsafe manifest filename %q", name)
		}
		gotHex, err := hashFileHex(filepath.Join(dir, clean))
		if err != nil {
			return Manifest{}, fmt.Errorf("read %s: %w", clean, err)
		}
		if !strings.EqualFold(gotHex, wantHex) {
			return Manifest{}, fmt.Errorf("sha256 mismatch for %s", clean)
		}
	}
	return man, nil
}

func hashFileHex(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
