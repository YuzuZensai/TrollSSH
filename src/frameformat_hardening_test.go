package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tsfHeader(fps float64, count uint32) []byte {
	raw := make([]byte, 18)
	copy(raw, tsfMagic)
	binary.LittleEndian.PutUint16(raw[4:], tsfVersion)
	binary.LittleEndian.PutUint64(raw[6:], math.Float64bits(fps))
	binary.LittleEndian.PutUint32(raw[14:], count)
	return raw
}

func writeRawTSF(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frames.tsf")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTSFRejectsInvalidFPS(t *testing.T) {
	for _, fps := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 0, 240.01} {
		raw := append(tsfHeader(fps, 1), 0, 0, 0, 0)
		if _, err := loadTSF(writeRawTSF(t, raw)); err == nil {
			t.Errorf("loadTSF accepted fps %v", fps)
		}
	}
}

func TestTSFRejectsImpossibleCountsAndLengths(t *testing.T) {
	if _, err := loadTSF(writeRawTSF(t, tsfHeader(30, math.MaxUint32))); err == nil {
		t.Fatal("loadTSF accepted impossible frame count")
	}

	raw := append(tsfHeader(30, 1), 0xff, 0xff, 0xff, 0xff)
	if _, err := loadTSF(writeRawTSF(t, raw)); err == nil {
		t.Fatal("loadTSF accepted overflowing frame length")
	}
}

func TestTSFCloseReleasesOwnedFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frames.tsf")
	if err := writeTSF(path, &FramesContainer{FPS: 30, ColorFrames: [][]byte{{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	frames, err := loadTSF(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := frames.ColorFrames[0]; len(got) != 3 || got[0] != 1 {
		t.Fatalf("unexpected zero-copy frame data: %v", got)
	}
	if err := frames.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if frames.ColorFrames != nil {
		t.Fatal("Close retained references to released frame data")
	}
	if err := frames.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTSFWriteRejectsInvalidHeaderValuesBeforeCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frames.tsf")
	err := writeTSF(path, &FramesContainer{FPS: math.NaN(), ColorFrames: [][]byte{{1}}})
	if err == nil || !strings.Contains(err.Error(), "fps") {
		t.Fatalf("writeTSF error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid write created output: %v", err)
	}
}
