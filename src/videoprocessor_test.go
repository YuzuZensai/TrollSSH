package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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

	got, err := loadTSF(output)
	if err != nil {
		t.Fatalf("loadTSF: %v", err)
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
