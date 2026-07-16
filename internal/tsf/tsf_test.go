package tsf

import (
	"bytes"
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

func TestTSFRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.tsf")
	original := &FramesContainer{
		ColorFrames: [][]byte{{100, 101, 102}, {110, 120, 130}},
		FPS:         29.97,
	}
	if err := Write(path, original); err != nil {
		t.Fatalf("Write: %v", err)
	}
	fc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() { _ = fc.Close() }()
	if fc.FPS != 29.97 {
		t.Errorf("fps = %v", fc.FPS)
	}
	if len(fc.ColorFrames) != 2 {
		t.Fatalf("frames = %d color", len(fc.ColorFrames))
	}
	if string(fc.ColorFrames[0]) != string([]byte{100, 101, 102}) {
		t.Errorf("color frame0 = %v", fc.ColorFrames[0])
	}
	if string(fc.ColorFrames[1]) != string([]byte{110, 120, 130}) {
		t.Errorf("color frame1 = %v", fc.ColorFrames[1])
	}
}

func TestTSFInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tsf")

	if err := os.WriteFile(path, []byte("not a tsf file"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected error for garbage input")
	}

	// Valid container but no frames.
	if err := Write(path, &FramesContainer{FPS: 30}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected error for empty frames")
	}

	// Valid container but fps <= 0.
	rawInvalidFPS := append(tsfHeader(0, 1), 1, 0, 0, 0, 1)
	if err := os.WriteFile(path, rawInvalidFPS, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected error for fps<=0")
	}

	// Truncated payload.
	if err := Write(path, &FramesContainer{ColorFrames: [][]byte{{1, 2, 3, 4}}, FPS: 30}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-2], 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected error for truncated file")
	}
}

func TestTSFRejectsInvalidFPS(t *testing.T) {
	for _, fps := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 0, 240.01} {
		raw := append(tsfHeader(fps, 1), 0, 0, 0, 0)
		if _, err := Load(writeRawTSF(t, raw)); err == nil {
			t.Errorf("Load accepted fps %v", fps)
		}
	}
}

func TestTSFRejectsImpossibleCountsAndLengths(t *testing.T) {
	if _, err := Load(writeRawTSF(t, tsfHeader(30, math.MaxUint32))); err == nil {
		t.Fatal("Load accepted impossible frame count")
	}

	raw := append(tsfHeader(30, 1), 0xff, 0xff, 0xff, 0xff)
	if _, err := Load(writeRawTSF(t, raw)); err == nil {
		t.Fatal("Load accepted overflowing frame length")
	}
}

func TestTSFCloseReleasesOwnedFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frames.tsf")
	if err := Write(path, &FramesContainer{FPS: 30, ColorFrames: [][]byte{{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	frames, err := Load(path)
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
	err := Write(path, &FramesContainer{FPS: math.NaN(), ColorFrames: [][]byte{{1}}})
	if err == nil || !strings.Contains(err.Error(), "fps") {
		t.Fatalf("Write error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid write created output: %v", err)
	}
}

func TestJPEGFrameSplitterAcrossChunks(t *testing.T) {
	splitter := &jpegFrameSplitter{}
	var frames [][]byte
	emit := func(frame []byte) error {
		frames = append(frames, bytes.Clone(frame))
		return nil
	}

	chunks := [][]byte{
		{0x01, 0x02, 0xff},
		{0xd8, 0x10, 0xff},
		{0xd9, 0xff, 0xd8, 0x20},
		{0x30, 0xff},
		{0xd9, 0x03},
	}
	for _, chunk := range chunks {
		if _, err := splitter.push(chunk, emit); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	if err := splitter.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}

	want := [][]byte{
		{0xff, 0xd8, 0x10, 0xff, 0xd9},
		{0xff, 0xd8, 0x20, 0x30, 0xff, 0xd9},
	}
	if len(frames) != len(want) {
		t.Fatalf("got %d frames, want %d", len(frames), len(want))
	}
	for i := range want {
		if !bytes.Equal(frames[i], want[i]) {
			t.Errorf("frame %d = %x, want %x", i, frames[i], want[i])
		}
	}
}

func TestJPEGFrameSplitterRejectsTruncatedFrame(t *testing.T) {
	splitter := &jpegFrameSplitter{}
	if _, err := splitter.push([]byte{0xff, 0xd8, 0x01}, func([]byte) error { return nil }); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := splitter.finish(); err == nil {
		t.Fatal("finish accepted a truncated JPEG")
	}
}

func TestBoundedLog(t *testing.T) {
	log := &boundedLog{limit: 4}
	if n, err := log.Write([]byte("abcdefgh")); err != nil || n != 8 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := log.String(); got != "abcd" {
		t.Fatalf("String = %q, want %q", got, "abcd")
	}
}

func TestStreamingTSFCommitAndAbort(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "frames.tsf")

	stream, err := newStreamingTSF(output, 24)
	if err != nil {
		t.Fatalf("newStreamingTSF: %v", err)
	}
	for _, frame := range [][]byte{{1, 2, 3}, {4, 5}} {
		if err := stream.addFrame(frame); err != nil {
			t.Fatalf("addFrame: %v", err)
		}
	}
	if err := stream.commit(output); err != nil {
		t.Fatalf("commit: %v", err)
	}
	stream.abort()

	got, err := Load(output)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() { _ = got.Close() }()
	if got.FPS != 24 || len(got.ColorFrames) != 2 || !bytes.Equal(got.ColorFrames[1], []byte{4, 5}) {
		t.Fatalf("unexpected streamed TSF: %+v", got)
	}

	original := []byte("existing destination")
	if err := os.WriteFile(output, original, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	failed, err := newStreamingTSF(output, 24)
	if err != nil {
		t.Fatalf("newStreamingTSF: %v", err)
	}
	if err := failed.addFrame([]byte{9}); err != nil {
		t.Fatalf("addFrame: %v", err)
	}
	failed.abort()

	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(contents, original) {
		t.Fatalf("destination changed after abort: %q", contents)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".frames.tsf-*.tmp"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain after abort: %v", matches)
	}
}
