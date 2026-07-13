package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// .tsf container, little-endian: "TSFR" | version uint16 | fps float64 |
// count uint32 | count × (colorLen uint32, color JPEG).
const (
	tsfMagic   = "TSFR"
	tsfVersion = 1
)

func writeTSF(output string, data *FramesContainer) error {
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.WriteString(tsfMagic); err != nil {
		return err
	}
	var hdr [14]byte
	binary.LittleEndian.PutUint16(hdr[0:], tsfVersion)
	binary.LittleEndian.PutUint64(hdr[2:], math.Float64bits(data.FPS))
	binary.LittleEndian.PutUint32(hdr[10:], uint32(len(data.ColorFrames)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}

	var lenBuf [4]byte
	for _, frame := range data.ColorFrames {
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(frame)))
		if _, err := w.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
	}
	return w.Flush()
}

func loadTSF(filename string) (*FramesContainer, error) {
	raw, err := readFrameFile(filename)
	if err != nil {
		return nil, err
	}
	invalid := func() error {
		return fmt.Errorf("invalid frames file %q: corrupt .tsf container", filename)
	}

	if len(raw) < 18 || string(raw[:4]) != tsfMagic {
		return nil, invalid()
	}
	version := binary.LittleEndian.Uint16(raw[4:])
	if version != tsfVersion {
		return nil, fmt.Errorf("unsupported .tsf version %d in %q", version, filename)
	}
	fps := math.Float64frombits(binary.LittleEndian.Uint64(raw[6:]))
	count := binary.LittleEndian.Uint32(raw[14:])

	colorFrames := make([][]byte, 0, count)
	off := 18
	for range count {
		if off+4 > len(raw) {
			return nil, invalid()
		}
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+n > len(raw) {
			return nil, invalid()
		}
		colorFrames = append(colorFrames, raw[off:off+n])
		off += n
	}

	if len(colorFrames) == 0 || fps <= 0 {
		return nil, fmt.Errorf(
			"invalid frames file %q: expected non-empty frames and a positive fps",
			filename,
		)
	}
	return &FramesContainer{ColorFrames: colorFrames, FPS: fps}, nil
}
