package tsf

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

func Write(output string, data *FramesContainer) error {
	if data == nil {
		return fmt.Errorf("cannot write nil frames container")
	}
	if math.IsNaN(data.FPS) || math.IsInf(data.FPS, 0) || data.FPS <= 0 || data.FPS > maxTSFFPS {
		return fmt.Errorf("cannot write .tsf: fps must be finite, positive, and at most %d", maxTSFFPS)
	}
	if len(data.ColorFrames) > maxTSFFrameCount || uint64(len(data.ColorFrames)) > math.MaxUint32 {
		return fmt.Errorf("cannot write .tsf: frame count exceeds limit")
	}
	for i, frame := range data.ColorFrames {
		if uint64(len(frame)) > math.MaxUint32 {
			return fmt.Errorf("cannot write .tsf: frame %d length exceeds uint32", i)
		}
	}

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

func Load(filename string) (*FramesContainer, error) {
	file, err := readFrameFile(filename)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			_ = file.Close()
		}
	}()
	raw := file.data
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
	if math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 || fps > maxTSFFPS {
		return nil, fmt.Errorf(
			"invalid frames file %q: fps must be finite, greater than 0, and at most %d",
			filename, maxTSFFPS,
		)
	}
	if count == 0 {
		return nil, fmt.Errorf("invalid frames file %q: expected non-empty frames", filename)
	}
	if count > maxTSFFrameCount || uint64(count) > uint64((len(raw)-18)/4) {
		return nil, invalid()
	}

	colorFrames := make([][]byte, 0, int(count))
	off := 18
	for range count {
		if len(raw)-off < 4 {
			return nil, invalid()
		}
		n := uint64(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if n > uint64(len(raw)-off) {
			return nil, invalid()
		}
		nativeLen := int(n)
		colorFrames = append(colorFrames, raw[off:off+nativeLen])
		off += nativeLen
	}

	if off != len(raw) {
		return nil, invalid()
	}
	data := &FramesContainer{ColorFrames: colorFrames, FPS: fps}
	frameFileOwners.Store(data, file)
	owned = true
	return data, nil
}
