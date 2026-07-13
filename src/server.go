package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const clearScreen = "\x1b[2J\x1b[0f"

type ConnectionTracker struct {
	mu     sync.Mutex
	counts map[string]int
	total  int
}

func newConnectionTracker() *ConnectionTracker {
	return &ConnectionTracker{counts: make(map[string]int)}
}

func (t *ConnectionTracker) increment(ip string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[ip]++
	t.total++
	return t.counts[ip]
}

func (t *ConnectionTracker) decrement(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.counts[ip]; !ok {
		return
	}
	t.counts[ip]--
	if t.total > 0 {
		t.total--
	}
	if t.counts[ip] <= 0 {
		delete(t.counts, ip)
	}
}

func (t *ConnectionTracker) totalCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

func (t *ConnectionTracker) hasReachedLimits(ip string, maxPerIP, maxTotal int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total >= maxTotal || t.counts[ip] >= maxPerIP
}

type frameSet struct {
	data     *FramesContainer
	renderer *FrameRenderer
}

type Server struct {
	config    Config
	sshConfig *ssh.ServerConfig
	sets      []frameSet
	tracker   *ConnectionTracker
	fakeLogin *string
	goodbye   *string
	listener  net.Listener
	closeOnce sync.Once
}

type ServerDeps struct {
	Config        Config
	HostKeys      []ssh.Signer
	BannerText    *string
	FakeLoginText *string
	GoodbyeText   *string
	VideoSets     []*FramesContainer
}

func clampDimension(value, max int) int {
	if value < 1 {
		return 1
	}
	if value > max {
		return max
	}
	return value
}

func createServer(deps ServerDeps) *Server {
	config := deps.Config

	sets := make([]frameSet, len(deps.VideoSets))
	for i, data := range deps.VideoSets {
		sets[i] = frameSet{
			data: data,
			renderer: newFrameRenderer(data.ColorFrames, asciiOptions{
				brightnessThreshold: config.BrightnessThreshold,
				charset:             config.Charset,
				invert:              config.Invert,
			}),
		}
	}

	sshConfig := &ssh.ServerConfig{
		MaxAuthTries: config.MaxAuthAttempts,
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			ip := hostOnly(conn.RemoteAddr().String())
			if config.LogCredentials {
				logInfo(fmt.Sprintf(
					`Auth attempt from %s method=password user="%s" pass="%s"`,
					ip, sanitizeN(conn.User(), 128), sanitizeN(string(password), 128),
				))
			}
			if conn.User() == "" || len(password) == 0 {
				return nil, errors.New("password rejected")
			}
			return nil, nil
		},
	}
	sshConfig.KeyExchanges = []string{
		"mlkem768x25519-sha256",
		"curve25519-sha256",
		"curve25519-sha256@libssh.org",
		"ecdh-sha2-nistp256",
		"ecdh-sha2-nistp384",
		"ecdh-sha2-nistp521",
		"diffie-hellman-group14-sha256",
	}
	if deps.BannerText != nil {
		banner := *deps.BannerText
		sshConfig.BannerCallback = func(_ ssh.ConnMetadata) string { return banner }
	}
	for _, key := range deps.HostKeys {
		sshConfig.AddHostKey(key)
	}

	return &Server{
		config:    config,
		sshConfig: sshConfig,
		sets:      sets,
		tracker:   newConnectionTracker(),
		fakeLogin: deps.FakeLoginText,
		goodbye:   deps.GoodbyeText,
	}
}

func hostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func (s *Server) Listen(host string, port int) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return err
	}
	s.listener = listener
	logInfo(fmt.Sprintf("TrollSSH listening on %s:%d", host, port))
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
}

func (s *Server) handleConn(conn net.Conn) {
	ip := hostOnly(conn.RemoteAddr().String())

	if s.tracker.hasReachedLimits(ip, s.config.MaxConnections, s.config.MaxTotalConnections) {
		_ = conn.Close()
		logWarn("Connection rejected (limit reached) from", ip)
		return
	}

	activeForIP := s.tracker.increment(ip)
	defer s.tracker.decrement(ip)

	if s.config.HandshakeTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(s.config.HandshakeTimeout))
	}

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshConfig)
	if err != nil {
		if strings.Contains(err.Error(), "i/o timeout") {
			logWarn("Handshake timeout for", ip)
		} else {
			logWarn(fmt.Sprintf("Client error from %s:", ip), sanitize(err.Error()))
		}
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	logDebug("Handshake from", ip)
	defer func() { _ = sshConn.Close() }()

	setIndex := rand.Intn(len(s.sets))
	logInfo(fmt.Sprintf(
		"New connection from %s (ip=%d, total=%d) -> playing %q",
		ip, activeForIP, s.tracker.totalCount(), s.sets[setIndex].data.Name,
	))

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(sshConn, channel, requests, ip, setIndex)
	}
	logInfo("Client closed connection from", ip)
}

type termSize struct {
	mu     sync.Mutex
	width  int
	height int
}

func (t *termSize) set(w, h, maxDim int) {
	t.mu.Lock()
	t.width = clampDimension(w, maxDim)
	t.height = clampDimension(h, maxDim)
	t.mu.Unlock()
}

func (t *termSize) get() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.width, t.height
}

func parseDims(payload []byte) (cols, rows int, ok bool) {
	if len(payload) < 8 {
		return 0, 0, false
	}
	// pty-req prefixes cols/rows with a TERM string; window-change does not.
	offset := 0
	strLen := binary.BigEndian.Uint32(payload)
	if int(strLen)+12 <= len(payload) {
		offset = 4 + int(strLen)
	}
	if len(payload) < offset+8 {
		return 0, 0, false
	}
	cols = int(binary.BigEndian.Uint32(payload[offset:]))
	rows = int(binary.BigEndian.Uint32(payload[offset+4:]))
	return cols, rows, true
}

// parsePtyTerm extracts the TERM string prefixing a pty-req payload.
func parsePtyTerm(payload []byte) (term string, ok bool) {
	if len(payload) < 4 {
		return "", false
	}
	strLen := binary.BigEndian.Uint32(payload)
	if int(strLen)+16 > len(payload) {
		return "", false
	}
	return string(payload[4 : 4+strLen]), true
}

func (s *Server) handleSession(
	sshConn *ssh.ServerConn,
	channel ssh.Channel,
	requests <-chan *ssh.Request,
	ip string,
	initialSetIndex int,
) {
	size := &termSize{}
	size.set(80, 24, s.config.MaxDimension)
	tier := colorTierTrueColor
	if s.config.ForceGrayscale {
		tier = colorTierNone
	}

	started := false
	for req := range requests {
		switch req.Type {
		case "pty-req":
			logDebug("Opening pty for session", ip)
			if cols, rows, ok := parseDims(req.Payload); ok {
				size.set(cols, rows, s.config.MaxDimension)
			}
			if term, ok := parsePtyTerm(req.Payload); ok {
				tier = detectColorTier(term)
				if s.config.ForceGrayscale {
					tier = colorTierNone
				}
				logDebug(fmt.Sprintf("Client %s TERM=%q -> color tier %d", ip, sanitizeN(term, 64), tier))
			}
			_ = req.Reply(true, nil)
		case "window-change":
			if len(req.Payload) >= 8 {
				cols := int(binary.BigEndian.Uint32(req.Payload))
				rows := int(binary.BigEndian.Uint32(req.Payload[4:]))
				size.set(cols, rows, s.config.MaxDimension)
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "exec":
			command := ""
			if len(req.Payload) >= 4 {
				n := binary.BigEndian.Uint32(req.Payload)
				if int(n)+4 <= len(req.Payload) {
					command = string(req.Payload[4 : 4+n])
				}
			}
			logInfo(fmt.Sprintf("Client %s attempted exec: %q", ip, sanitizeN(command, 512)))
			_ = req.Reply(true, nil)
			if !started {
				started = true
				go s.playVideo(sshConn, channel, size, ip, initialSetIndex, false, tier)
			}
		case "shell":
			logDebug("Opening shell for session", ip)
			_ = req.Reply(true, nil)
			if !started {
				started = true
				go s.playVideo(sshConn, channel, size, ip, initialSetIndex, false, tier)
			}
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

func (s *Server) pickNextSetIndex(exclude int) int {
	if len(s.sets) <= 1 {
		return exclude
	}
	next := exclude
	for next == exclude {
		next = rand.Intn(len(s.sets))
	}
	return next
}

func (s *Server) playVideo(
	sshConn *ssh.ServerConn,
	channel ssh.Channel,
	size *termSize,
	ip string,
	setIndex int,
	keepAspectRatio bool,
	tier colorTier,
) {
	config := s.config
	current := s.sets[setIndex]

	w, h := size.get()
	logDebug(fmt.Sprintf("Terminal size %dx%d for %s", w, h, ip))

	if s.fakeLogin != nil {
		_, _ = channel.Write([]byte(clearScreen))
		_, _ = channel.Write([]byte(*s.fakeLogin))
	}

	done := make(chan struct{})
	var doneOnce sync.Once
	closeSession := func() {
		doneOnce.Do(func() { close(done) })
	}

	switchCh := make(chan int, 8)
	go func() {
		buf := make([]byte, 256)
		var lastSwitch time.Time
		for {
			n, err := channel.Read(buf)
			if err != nil {
				closeSession()
				return
			}
			if !config.AllowUserControl {
				continue
			}
			str := string(buf[:n])
			delta := 0
			if strings.Contains(str, "\x1b[C") || strings.Contains(str, "\x1b[A") {
				delta = 1
			} else if strings.Contains(str, "\x1b[D") || strings.Contains(str, "\x1b[B") {
				delta = -1
			}
			if delta == 0 {
				continue
			}
			now := time.Now()
			if now.Sub(lastSwitch) < config.SwitchDebounce {
				continue
			}
			lastSwitch = now
			select {
			case switchCh <- delta:
			default:
			}
		}
	}()

	select {
	case <-time.After(config.LoginDelay):
	case <-done:
		return
	}

	frameInterval := func() time.Duration {
		return time.Duration(float64(time.Second) / current.data.FPS)
	}

	ticker := time.NewTicker(frameInterval())
	defer ticker.Stop()

	currentFrame := 0
	loopCount := 0

	for {
		select {
		case <-done:
			return

		case delta := <-switchCh:
			if len(s.sets) <= 1 {
				continue
			}
			setIndex = (setIndex + delta + len(s.sets)) % len(s.sets)
			current = s.sets[setIndex]
			currentFrame = 0
			logDebug(fmt.Sprintf("%s switched to %q", ip, current.data.Name))
			ticker.Reset(frameInterval())

		case <-ticker.C:
			w, h := size.get()
			ascii, err := current.renderer.render(currentFrame, w, h, keepAspectRatio, tier)
			if err != nil {
				logError("Render error for", ip, sanitize(err.Error()))
				_ = sshConn.Close()
				return
			}

			if _, err := channel.Write([]byte(clearScreen + ascii)); err != nil {
				closeSession()
				return
			}

			currentFrame++
			if currentFrame < len(current.data.ColorFrames) {
				continue
			}

			currentFrame = 0
			loopCount++
			if config.MaxLoop > 0 && loopCount >= config.MaxLoop {
				_, _ = channel.Write([]byte(clearScreen))
				if s.goodbye != nil {
					_, _ = channel.Write([]byte(*s.goodbye))
				}
				time.Sleep(1 * time.Second)
				logInfo("Playback finished, closing session", ip)
				_ = channel.Close()
				_ = sshConn.Close()
				return
			}

			if config.PlaybackMode == PlaybackRandom {
				setIndex = s.pickNextSetIndex(setIndex)
				current = s.sets[setIndex]
				logInfo(fmt.Sprintf(
					"Playthrough done for %s, switching to %q", ip, current.data.Name,
				))
				ticker.Reset(frameInterval())
			} else if config.MaxLoop > 0 {
				logInfo(fmt.Sprintf(
					"Playthrough done for %s, looping %q (%d/%d)",
					ip, current.data.Name, loopCount, config.MaxLoop,
				))
			} else {
				logInfo(fmt.Sprintf(
					"Playthrough done for %s, looping %q (%d)",
					ip, current.data.Name, loopCount,
				))
			}
		}
	}
}
