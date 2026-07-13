package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

var (
	jpegSOI = []byte{0xff, 0xd8}
	jpegEOI = []byte{0xff, 0xd9}
)

type jpegFrameSplitter struct {
	buffer []byte
}

func (s *jpegFrameSplitter) push(chunk []byte) [][]byte {
	s.buffer = append(s.buffer, chunk...)
	var frames [][]byte
	for {
		start := bytes.Index(s.buffer, jpegSOI)
		if start == -1 {
			break
		}
		end := bytes.Index(s.buffer[start+len(jpegSOI):], jpegEOI)
		if end == -1 {
			break
		}
		frameEnd := start + len(jpegSOI) + end + len(jpegEOI)
		frame := make([]byte, frameEnd-start)
		copy(frame, s.buffer[start:frameEnd])
		frames = append(frames, frame)
		s.buffer = s.buffer[frameEnd:]
	}
	return frames
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

func extractFrames(path, vf, label string, maxDimension, totalFrames int) ([][]byte, error) {
	cmd := exec.Command(
		"ffmpeg", "-i", path,
		"-c:v", "mjpeg",
		"-q:v", "3",
		"-vf", vf,
		"-f", "image2pipe",
		"pipe:1",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w", err)
	}

	var frames [][]byte
	splitter := &jpegFrameSplitter{}
	reportProgress := func(count int) {
		if totalFrames > 0 {
			pct := min(100, int(math.Round(float64(count)/float64(totalFrames)*100)))
			fmt.Printf("\rGenerating %s frames: %d/%d (%d%%)", label, count, totalFrames, pct)
		} else {
			fmt.Printf("\rGenerating %s frames: %d", label, count)
		}
	}

	buf := make([]byte, 256*1024)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			frames = append(frames, splitter.push(buf[:n])...)
			reportProgress(len(frames))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("ffmpeg stream error: %s", err.Error())
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %s", strings.TrimSpace(stderr.String()))
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames were decoded from the video")
	}
	fmt.Println()
	return frames, nil
}

func processVideo(path, output string, maxDimension int) error {
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

	colorFrames, err := extractFrames(path, scaleFilter, "color", maxDimension, totalFrames)
	if err != nil {
		return err
	}

	videoData := FramesContainer{FPS: fps, ColorFrames: colorFrames}
	if err := writeTSF(output, &videoData); err != nil {
		return err
	}
	logInfo(fmt.Sprintf("Saved %d frames to %s", len(videoData.ColorFrames), output))
	return nil
}
