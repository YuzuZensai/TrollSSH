// .tsf layout, little-endian: "TSFR" | version uint16 | fps float64 |
// count uint32 | count × (colorLen uint32, color JPEG).
package tsf

import "sync"

const (
	tsfMagic         = "TSFR"
	tsfVersion       = 1
	maxTSFFPS        = 240
	maxTSFFrameCount = 10_000_000
)

type FramesContainer struct {
	ColorFrames [][]byte
	FPS         float64
	Name        string
}

type frameFile struct {
	data    []byte
	cleanup func() error
	once    sync.Once
	err     error
}

func (f *frameFile) Close() error {
	if f == nil {
		return nil
	}
	f.once.Do(func() {
		if f.cleanup != nil {
			f.err = f.cleanup()
		}
		f.data = nil
	})
	return f.err
}

var frameFileOwners sync.Map // map[*FramesContainer]*frameFile

func (data *FramesContainer) Close() error {
	if data == nil {
		return nil
	}
	owner, ok := frameFileOwners.LoadAndDelete(data)
	if !ok {
		return nil
	}
	data.ColorFrames = nil
	return owner.(*frameFile).Close()
}
