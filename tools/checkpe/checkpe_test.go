package main

import (
	"debug/pe"
	"os"
	"path/filepath"
	"testing"
)

func TestExpectedMachineAndRejectNonPE(t *testing.T) {
	mArm, err := ExpectedMachine("arm64")
	if err != nil || mArm != pe.IMAGE_FILE_MACHINE_ARM64 {
		t.Fatalf("arm64 = 0x%X err=%v", mArm, err)
	}
	mAmd, err := ExpectedMachine("amd64")
	if err != nil || mAmd != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Fatalf("amd64 = 0x%X err=%v", mAmd, err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fake.exe"), []byte("not-a-pe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckDir(dir, "arm64"); err == nil {
		t.Fatal("expected CheckDir to fail on invalid PE binary")
	}
}
