package tsf

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YuzuZensai/TrollSSH/internal/logx"
)

var (
	jpegSOI = []byte{0xff, 0xd8}
	jpegEOI = []byte{0xff, 0xd9}
)

const (
	maxJPEGFrameBytes = 64 << 20
	maxFFmpegLogBytes = 64 << 10
)

type jpegFrameSplitter struct {
	buffer []byte
	scan   int
	inJPEG bool
}

func (s *jpegFrameSplitter) push(chunk []byte, emit func([]byte) error) (int, error) {
	s.buffer = append(s.buffer, chunk...)
	emitted := 0
	for {
		if !s.inJPEG {
			start := bytes.Index(s.buffer[s.scan:], jpegSOI)
			if start == -1 {
				// Retain only a possible marker prefix spanning two reads.
				if len(s.buffer) > 0 && s.buffer[len(s.buffer)-1] == jpegSOI[0] {
					s.buffer = s.buffer[len(s.buffer)-1:]
				} else {
					s.buffer = s.buffer[:0]
				}
				s.scan = 0
				return emitted, nil
			}
			start += s.scan
			s.buffer = s.buffer[start:]
			s.scan = len(jpegSOI)
			s.inJPEG = true
		}

		end := bytes.Index(s.buffer[s.scan:], jpegEOI)
		if end == -1 {
			if len(s.buffer) > maxJPEGFrameBytes {
				return emitted, fmt.Errorf("JPEG frame exceeds %d MiB limit", maxJPEGFrameBytes>>20)
			}
			s.scan = max(len(jpegSOI), len(s.buffer)-1)
			return emitted, nil
		}
		frameEnd := s.scan + end + len(jpegEOI)
		if frameEnd > maxJPEGFrameBytes {
			return emitted, fmt.Errorf("JPEG frame exceeds %d MiB limit", maxJPEGFrameBytes>>20)
		}
		if err := emit(s.buffer[:frameEnd]); err != nil {
			return emitted, err
		}
		emitted++
		s.buffer = s.buffer[frameEnd:]
		s.scan = 0
		s.inJPEG = false
	}
}

func (s *jpegFrameSplitter) finish() error {
	if s.inJPEG {
		return fmt.Errorf("ffmpeg produced a truncated JPEG frame")
	}
	return nil
}

type boundedLog struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedLog) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := w.limit - w.buffer.Len(); remaining > 0 {
		_, _ = w.buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func (w *boundedLog) String() string {
	return strings.TrimSpace(w.buffer.String())
}

type streamingTSF struct {
	file   *os.File
	writer *bufio.Writer
	path   string
	count  uint32
}

func newStreamingTSF(output string, fps float64) (*streamingTSF, error) {
	if math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 || fps > maxTSFFPS {
		return nil, fmt.Errorf("cannot write .tsf: fps must be finite and between 0 and %d", maxTSFFPS)
	}
	dir := filepath.Dir(output)
	f, err := os.CreateTemp(dir, "."+filepath.Base(output)+"-*.tmp")
	if err != nil {
		return nil, err
	}
	s := &streamingTSF{file: f, writer: bufio.NewWriterSize(f, 1<<20), path: f.Name()}
	if err := f.Chmod(0o644); err != nil {
		s.abort()
		return nil, err
	}
	if _, err := s.writer.WriteString(tsfMagic); err != nil {
		s.abort()
		return nil, err
	}
	var hdr [14]byte
	binary.LittleEndian.PutUint16(hdr[0:], tsfVersion)
	binary.LittleEndian.PutUint64(hdr[2:], math.Float64bits(fps))
	if _, err := s.writer.Write(hdr[:]); err != nil {
		s.abort()
		return nil, err
	}
	return s, nil
}

func (s *streamingTSF) addFrame(frame []byte) error {
	if s.count >= maxTSFFrameCount {
		return fmt.Errorf("too many video frames")
	}
	if uint64(len(frame)) > math.MaxUint32 {
		return fmt.Errorf("JPEG frame is too large")
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(frame)))
	if _, err := s.writer.Write(size[:]); err != nil {
		return err
	}
	if _, err := s.writer.Write(frame); err != nil {
		return err
	}
	s.count++
	return nil
}

func (s *streamingTSF) commit(output string) error {
	if s.count == 0 {
		return fmt.Errorf("no frames were decoded from the video")
	}
	if err := s.writer.Flush(); err != nil {
		return err
	}
	if _, err := s.file.Seek(14, io.SeekStart); err != nil {
		return err
	}
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], s.count)
	if _, err := s.file.Write(count[:]); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	if err := s.file.Close(); err != nil {
		return err
	}
	s.file = nil
	if err := os.Rename(s.path, output); err != nil {
		return err
	}
	s.path = ""
	return nil
}

func (s *streamingTSF) abort() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if s.path != "" {
		_ = os.Remove(s.path)
		s.path = ""
	}
}

type ffprobeOutput struct {
	Streams []struct {
		RFrameRate string `json:"r_frame_rate"`
		NbFrames   string `json:"nb_frames"`
		Duration   string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func parseFrameRate(rate string) float64 {
	if rate == "" {
		return math.NaN()
	}
	parts := strings.SplitN(rate, "/", 2)
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return math.NaN()
	}
	if len(parts) == 2 {
		den, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || den == 0 {
			return math.NaN()
		}
		return num / den
	}
	return num
}

func extractFrames(path, vf, label string, totalFrames int, emit func([]byte) error) (int, error) {
	cmd := exec.Command(
		"ffmpeg", "-hide_banner", "-loglevel", "error", "-nostats",
		"-i", path,
		"-c:v", "mjpeg",
		"-q:v", "3",
		"-vf", vf,
		"-f", "image2pipe",
		"pipe:1",
	)
	stderr := &boundedLog{limit: maxFFmpegLogBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg failed: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("ffmpeg failed: %w", err)
	}

	splitter := &jpegFrameSplitter{}
	frameCount := 0
	lastReport := time.Now()
	reportProgress := func(force bool) {
		if !force && time.Since(lastReport) < 250*time.Millisecond {
			return
		}
		if totalFrames > 0 {
			pct := min(100, int(math.Round(float64(frameCount)/float64(totalFrames)*100)))
			fmt.Printf("\rGenerating %s frames: %d/%d (%d%%)", label, frameCount, totalFrames, pct)
		} else {
			fmt.Printf("\rGenerating %s frames: %d", label, frameCount)
		}
		lastReport = time.Now()
	}
	failStream := func(err error) (int, error) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return frameCount, err
	}

	buf := make([]byte, 256*1024)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			emitted, splitErr := splitter.push(buf[:n], emit)
			frameCount += emitted
			if splitErr != nil {
				return failStream(fmt.Errorf("ffmpeg stream error: %w", splitErr))
			}
			if emitted > 0 {
				reportProgress(false)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return failStream(fmt.Errorf("ffmpeg stream error: %w", readErr))
		}
	}
	if err := cmd.Wait(); err != nil {
		if msg := stderr.String(); msg != "" {
			return frameCount, fmt.Errorf("ffmpeg failed: %s", msg)
		}
		return frameCount, fmt.Errorf("ffmpeg failed: %w", err)
	}
	if err := splitter.finish(); err != nil {
		return frameCount, err
	}
	if frameCount == 0 {
		return 0, fmt.Errorf("no frames were decoded from the video")
	}
	reportProgress(true)
	fmt.Println()
	return frameCount, nil
}

func ProcessVideo(path, output string, maxDimension int) error {
	probeCmd := exec.Command(
		"ffprobe", "-v", "error",
		"-show_streams", "-show_format",
		"-of", "json", path,
	)
	probeOut, err := probeCmd.Output()
	if err != nil {
		msg := err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			msg = strings.TrimSpace(string(exitErr.Stderr))
		}
		return fmt.Errorf("ffprobe failed: %s", msg)
	}

	var probe ffprobeOutput
	if err := json.Unmarshal(probeOut, &probe); err != nil {
		return fmt.Errorf("ffprobe failed: %w", err)
	}
	if len(probe.Streams) == 0 {
		return fmt.Errorf("unable to determine a valid video fps")
	}
	stream := probe.Streams[0]

	fps := parseFrameRate(stream.RFrameRate)
	if math.IsNaN(fps) || fps <= 0 {
		return fmt.Errorf("unable to determine a valid video fps")
	}

	totalFrames := 0
	if n, err := strconv.Atoi(stream.NbFrames); err == nil {
		totalFrames = n
	} else {
		durStr := probe.Format.Duration
		if durStr == "" {
			durStr = stream.Duration
		}
		if d, err := strconv.ParseFloat(durStr, 64); err == nil {
			totalFrames = int(math.Round(d * fps))
		}
	}

	scaleFilter := fmt.Sprintf(
		"scale=w=%d:h=%d:force_original_aspect_ratio=decrease",
		maxDimension, maxDimension,
	)

	outputFile, err := newStreamingTSF(output, fps)
	if err != nil {
		return err
	}
	defer outputFile.abort()

	frameCount, err := extractFrames(path, scaleFilter, "color", totalFrames, outputFile.addFrame)
	if err != nil {
		return err
	}
	if err := outputFile.commit(output); err != nil {
		return err
	}
	logx.Info(fmt.Sprintf("Saved %d frames to %s", frameCount, output))
	return nil
}
