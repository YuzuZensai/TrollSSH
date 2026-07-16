package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

type cliArgs struct {
	generate   bool
	video      string
	resolution int
}

func parseArgs(argv []string) cliArgs {
	args := cliArgs{resolution: 512}
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--generate", "-g":
			args.generate = true
		case "--video", "-v":
			if i+1 < len(argv) {
				i++
				args.video = argv[i]
			}
		case "--resolution", "-r":
			if i+1 < len(argv) {
				i++
				if n, err := strconv.Atoi(argv[i]); err == nil {
					args.resolution = max(n, 16)
				}
			}
		}
	}
	return args
}

func fail(message string) {
	logError(message)
	os.Exit(1)
}

func resolveVideoPath(explicitPath string) string {
	abs, err := filepath.Abs(explicitPath)
	if err != nil {
		return ""
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return abs
	}
	return ""
}

func generateFrames(framesDir, videoArg string, resolution int) {
	if videoArg == "" {
		fail("No source video given. Pass --video <path>.")
	}
	videoPath := resolveVideoPath(videoArg)
	if videoPath == "" {
		fail(fmt.Sprintf("Source video %q does not exist or is not a file.", videoArg))
	}

	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		fail(fmt.Sprintf("Failed to create frames directory %q: %s", framesDir, err.Error()))
	}
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	output := filepath.Join(framesDir, base+".tsf")

	logInfo(fmt.Sprintf("Generating frames from %q -> %s", videoPath, output))
	if err := processVideo(videoPath, output, resolution); err != nil {
		fail(fmt.Sprintf("Failed to generate frames from %q: %s", videoPath, err.Error()))
	}
}

const frameDataWarnBytes = 2 << 30

func loadAllFrames(framesDir string) []*FramesContainer {
	entries, err := os.ReadDir(framesDir)
	var files []string
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(strings.ToLower(e.Name()), ".tsf") {
				files = append(files, e.Name())
			}
		}
		sort.Strings(files)
	}

	if len(files) == 0 {
		fail(fmt.Sprintf(
			"No frame sets found in %q. Generate one first with: trollssh --generate --video <path>",
			framesDir,
		))
	}
	var totalBytes int64
	for _, file := range files {
		info, err := os.Stat(filepath.Join(framesDir, file))
		if err != nil {
			fail(err.Error())
		}
		totalBytes += info.Size()
	}
	if totalBytes > frameDataWarnBytes {
		logWarn(fmt.Sprintf(
			"Frame data is %.1f MB of mapped memory; make sure the container memory limit leaves headroom",
			float64(totalBytes)/(1<<20),
		))
	} else {
		logInfo(fmt.Sprintf("Frame data: %.1f MB", float64(totalBytes)/(1<<20)))
	}

	concurrency := min(len(files), max(1, min(runtime.NumCPU(), 4)))

	results := make([]*FramesContainer, len(files))
	errs := make([]error, len(files))
	var next int
	var nextMu sync.Mutex
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for {
			nextMu.Lock()
			i := next
			next++
			nextMu.Unlock()
			if i >= len(files) {
				return
			}
			file := files[i]
			filePath := filepath.Join(framesDir, file)
			info, err := os.Stat(filePath)
			if err != nil {
				errs[i] = err
				return
			}
			logInfo(fmt.Sprintf("Loading %s (%.1f MB)...", file, float64(info.Size())/1024/1024))
			data, err := loadTSF(filePath)
			if err != nil {
				errs[i] = err
				return
			}
			data.Name = file
			logInfo(fmt.Sprintf("  %s: %d frames @ %gfps", file, len(data.ColorFrames), data.FPS))
			results[i] = data
		}
	}

	wg.Add(concurrency)
	for range concurrency {
		go worker()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			fail(err.Error())
		}
	}
	return results
}

func applyMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	for _, path := range []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || n <= 0 || n > 1<<48 {
			return
		}
		limit := n * 9 / 10
		debug.SetMemoryLimit(limit)
		logInfo(fmt.Sprintf("Memory limit set to %d MB (90%% of cgroup limit)", limit>>20))
		return
	}
}

func main() {
	_ = godotenv.Load()
	logThreshold = resolveThreshold()
	applyMemoryLimit()

	config := loadConfig()
	args := parseArgs(os.Args[1:])

	cwd, err := os.Getwd()
	if err != nil {
		fail(err.Error())
	}
	dataDir := filepath.Join(cwd, "data")
	framesDir := filepath.Join(cwd, "frames")

	if args.generate {
		generateFrames(framesDir, args.video, args.resolution)
		return
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fail(fmt.Sprintf("Failed to create data directory %q: %s", dataDir, err.Error()))
	}

	var bannerText, fakeLoginText, goodbyeText *string
	if text, ok := loadOptionalTextFile(filepath.Join(dataDir, "banner.txt")); ok {
		bannerText = &text
	}
	if text, ok := loadOptionalTextFile(filepath.Join(dataDir, "fakelogin.txt")); ok {
		fakeLoginText = &text
	}
	if text, ok := loadOptionalTextFile(filepath.Join(dataDir, "goodbye.txt")); ok {
		goodbyeText = &text
	}

	hostKeys, err := ensureHostKeys(dataDir)
	if err != nil {
		fail(err.Error())
	}
	videoSets := loadAllFrames(framesDir)
	logInfo(fmt.Sprintf("Loaded %d frame set(s)", len(videoSets)))

	server := createServer(ServerDeps{
		Config:        config,
		HostKeys:      hostKeys,
		BannerText:    bannerText,
		FakeLoginText: fakeLoginText,
		GoodbyeText:   goodbyeText,
		VideoSets:     videoSets,
	})
	defer server.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logInfo(fmt.Sprintf("Received %s, shutting down...", sig))
		forceExit := time.AfterFunc(5*time.Second, func() { os.Exit(0) })
		server.Close()
		forceExit.Stop()
	}()

	if err := server.Listen(config.Host, config.Port); err != nil {
		logError("Server error:", err.Error())
		os.Exit(1)
	}
}
