package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionMatchesManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "mygo.json"))
	if err != nil {
		t.Fatalf("read mygo.json: %v", err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse mygo.json: %v", err)
	}
	if manifest.Version != Version {
		t.Fatalf("mygo.json version %q != version.Version %q", manifest.Version, Version)
	}
}
