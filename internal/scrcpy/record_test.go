package scrcpy

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestMP4RecorderWithFixture(t *testing.T) {
	fix, err := LoadFixture(filepath.Join("testdata", "fold8_scrcpy_session.json"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	outPath := filepath.Join(t.TempDir(), "rec", "session.mp4")
	rec, err := NewMP4Recorder(outPath, fix.InitialWidth, fix.InitialHeight)
	if err != nil {
		t.Fatalf("NewMP4Recorder: %v", err)
	}
	for _, pkt := range fix.Packets {
		if pkt.IsSession {
			continue
		}
		payload, decErr := base64.StdEncoding.DecodeString(pkt.PayloadB64)
		if decErr != nil {
			t.Fatalf("decode fixture packet: %v", decErr)
		}
		if err := rec.WriteAnnexB(payload, pkt.PtsUs, pkt.IsConfig, pkt.IsKeyframe); err != nil {
			t.Fatalf("WriteAnnexB: %v", err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, boxTag := range [][]byte{[]byte("ftyp"), []byte("moov"), []byte("avcC"), []byte("moof"), []byte("mdat")} {
		if !bytes.Contains(raw, boxTag) {
			t.Fatalf("expected MP4 box %q in output (%d bytes)", string(boxTag), len(raw))
		}
	}
}
