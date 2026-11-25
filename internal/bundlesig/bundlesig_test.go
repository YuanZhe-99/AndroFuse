package bundlesig

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestSignAndVerifyRoundtrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	svcPath := filepath.Join(dir, "androfuse-svc.exe")
	dllPath := filepath.Join(dir, "androfuse_core.dll")
	if err := os.WriteFile(svcPath, []byte("mock-svc-pe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dllPath, []byte("mock-core-dll"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Sign(dir, "0.1.0", priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	man, err := Verify(dir, pub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if man.Version != "0.1.0" || len(man.Files) != 2 {
		t.Fatalf("unexpected manifest: %+v", man)
	}

	// Tampering a binary must fail Verify.
	if err := os.WriteFile(dllPath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir, pub); err == nil {
		t.Fatal("expected Verify to reject tampered binary")
	}
}
