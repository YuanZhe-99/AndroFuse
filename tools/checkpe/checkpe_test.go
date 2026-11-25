package main

import (
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestCheckDirRequiresGUISubsystemForDesktopApp builds a tiny Windows program as a console
// and as a GUI binary named androfuse.exe and checks that only the GUI build passes.
func TestCheckDirRequiresGUISubsystemForDesktopApp(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(ldflags string) string {
		dir := t.TempDir()
		cmd := exec.Command("go", "build", "-ldflags="+ldflags, "-o", filepath.Join(dir, "androfuse.exe"), src)
		cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=arm64", "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
		return dir
	}

	if _, err := CheckDir(build("-s -w"), "arm64"); err == nil || !strings.Contains(err.Error(), "subsystem") {
		t.Fatalf("console-subsystem androfuse.exe must be rejected, got %v", err)
	}
	if n, err := CheckDir(build("-s -w -H=windowsgui"), "arm64"); err != nil || n != 1 {
		t.Fatalf("GUI-subsystem androfuse.exe must pass, got n=%d err=%v", n, err)
	}
}
