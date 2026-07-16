package sshserver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/YuzuZensai/TrollSSH/internal/config"
	"github.com/YuzuZensai/TrollSSH/internal/logx"
	"github.com/YuzuZensai/TrollSSH/internal/render"
	"github.com/YuzuZensai/TrollSSH/internal/tsf"
)

const (
	clearScreen         = "\x1b[2J\x1b[0f"
	hideCursor          = "\x1b[?25l"
	showCursor          = "\x1b[?25h"
	syncStart           = "\x1b[?2026h"
	syncEnd             = "\x1b[?2026l"
	homeCursor          = "\x1b[H"
	maxSessionsPerConn  = 1
	terminalSizeQuantum = 4
	resizeDebounce      = 200 * time.Millisecond
	outputStallTimeout  = 15 * time.Second
)

var errOutputStalled = errors.New("SSH output stalled")

func writePartsWithTimeout(
	conn *ssh.ServerConn,
	channel ssh.Channel,
	timeout time.Duration,
	parts ...string,
) error {
	write := func() error {
		for _, part := range parts {
			if _, err := io.WriteString(channel, part); err != nil {
				return err
			}
		}
		return nil
	}
	if timeout <= 0 {
		return write()
	}

	fired := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		_ = conn.Close()
		close(fired)
	})
	err := write()
	if timer.Stop() {
		return err
	}
	<-fired
	return errOutputStalled
}

func writeFrameWithTimeout(
	conn *ssh.ServerConn,
	channel ssh.Channel,
	timeout time.Duration,
	prefix string,
	frame []byte,
) error {
	write := func() error {
		if _, err := io.WriteString(channel, syncStart+prefix); err != nil {
			return err
		}
		if _, err := channel.Write(frame); err != nil {
			return err
		}
		_, err := io.WriteString(channel, syncEnd)
		return err
	}
	if timeout <= 0 {
		return write()
	}
	fired := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		_ = conn.Close()
		close(fired)
	})
	err := write()
	if timer.Stop() {
		return err
	}
	<-fired
	return errOutputStalled
}

type ConnectionTracker struct {
	mu     sync.Mutex
	counts map[string]int
	total  int
}

func newConnectionTracker() *ConnectionTracker {
	return &ConnectionTracker{counts: make(map[string]int)}
}

func (t *ConnectionTracker) tryAcquire(ip string, maxPerIP, maxTotal int) (int, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total >= maxTotal || t.counts[ip] >= maxPerIP {
		return t.counts[ip], t.total, false
	}
	t.counts[ip]++
	t.total++
	return t.counts[ip], t.total, true
}

func (t *ConnectionTracker) release(ip string) {
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

type SessionTracker struct {
	mu      sync.Mutex
	perConn map[*ssh.ServerConn]int
	total   int
}

func newSessionTracker() *SessionTracker {
	return &SessionTracker{perConn: make(map[*ssh.ServerConn]int)}
}

func (t *SessionTracker) tryAcquire(conn *ssh.ServerConn, maxPerConn, maxTotal int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total >= maxTotal || t.perConn[conn] >= maxPerConn {
		return false
	}
	t.perConn[conn]++
	t.total++
	return true
}

func (t *SessionTracker) release(conn *ssh.ServerConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := t.perConn[conn]
	if count <= 0 {
		return
	}
	if count == 1 {
		delete(t.perConn, conn)
	} else {
		t.perConn[conn] = count - 1
	}
	t.total--
}

type frameSet struct {
	data     *tsf.FramesContainer
	renderer *render.Renderer
}

type Server struct {
	config    config.Config
	sshConfig *ssh.ServerConfig
	sets      []frameSet
	cache     *render.Cache
	tracker   *ConnectionTracker
	sessions  *SessionTracker
	fakeLogin *string
	goodbye   *string
	mu        sync.Mutex
	listener  net.Listener
	conns     map[net.Conn]struct{}
	connWG    sync.WaitGroup
	closing   bool
	closeOnce sync.Once
}

type ServerDeps struct {
	Config        config.Config
	HostKeys      []ssh.Signer
	BannerText    *string
	FakeLoginText *string
	GoodbyeText   *string
	VideoSets     []*tsf.FramesContainer
}

func clampTermSize(cols, rows, maxDimension, maxCells, quantum int) (int, int) {
	cols = max(cols, 1)
	rows = max(rows, 1)
	scale := min(1.0, float64(maxDimension)/float64(cols), float64(maxDimension)/float64(rows))
	area := float64(cols) * float64(rows)
	if area*scale*scale > float64(maxCells) {
		scale = min(scale, math.Sqrt(float64(maxCells)/area))
	}
	cols = max(1, int(math.Floor(float64(cols)*scale)))
	rows = max(1, int(math.Floor(float64(rows)*scale)))
	if quantum > 1 {
		if cols >= quantum {
			cols -= cols % quantum
		}
		if rows >= quantum {
			rows -= rows % quantum
		}
	}
	return cols, rows
}

func New(deps ServerDeps) *Server {
	cfg := deps.Config

	cache := render.NewCache(int64(cfg.RenderCacheMB) << 20)
	sets := make([]frameSet, len(deps.VideoSets))
	for i, data := range deps.VideoSets {
		sets[i] = frameSet{
			data: data,
			renderer: render.NewRenderer(i, data.ColorFrames, render.Options{
				BrightnessThreshold: cfg.BrightnessThreshold,
				Charset:             cfg.Charset,
				Invert:              cfg.Invert,
			}, cache),
		}
	}

	sshConfig := &ssh.ServerConfig{
		MaxAuthTries: cfg.MaxAuthAttempts,
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			ip := hostOnly(conn.RemoteAddr().String())
			if cfg.LogCredentials {
				logx.Info(fmt.Sprintf(
					`Auth attempt from %s method=password user="%s" pass="%s"`,
					ip, logx.SanitizeN(conn.User(), 128), logx.SanitizeN(string(password), 128),
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
		config:    cfg,
		sshConfig: sshConfig,
		sets:      sets,
		cache:     cache,
		tracker:   newConnectionTracker(),
		sessions:  newSessionTracker(),
		fakeLogin: deps.FakeLoginText,
		goodbye:   deps.GoodbyeText,
		conns:     make(map[net.Conn]struct{}),
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
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		_ = listener.Close()
		return nil
	}
	s.listener = listener
	s.mu.Unlock()
	logx.Info(fmt.Sprintf("TrollSSH listening on %s:%d", host, port))
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		ip := hostOnly(conn.RemoteAddr().String())
		activeForIP, total, ok := s.tracker.tryAcquire(ip, s.config.MaxConnections, s.config.MaxTotalConnections)
		if !ok {
			_ = conn.Close()
			logx.Warn("Connection rejected (limit reached) from", ip)
			continue
		}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			s.tracker.release(ip)
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.connWG.Add(1)
		s.mu.Unlock()
		go s.handleConn(conn, ip, activeForIP, total)
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		listener := s.listener
		conns := make([]net.Conn, 0, len(s.conns))
		for conn := range s.conns {
			conns = append(conns, conn)
		}
		s.mu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}
		for _, conn := range conns {
			_ = conn.Close()
		}
		s.connWG.Wait()
		stats := s.cache.Stats()
		if stats.Hits+stats.Misses > 0 {
			logx.Info(fmt.Sprintf(
				"Render cache: size=%.1fMB hits=%d misses=%d evictions=%d rejected=%d renders=%d render_time=%s",
				float64(stats.SizeBytes)/(1<<20), stats.Hits, stats.Misses, stats.Evictions,
				stats.Rejections, stats.Renders, stats.RenderTime,
			))
		}
		for _, set := range s.sets {
			if err := set.data.Close(); err != nil {
				logx.Warn("Failed to release frame set", set.data.Name, logx.Sanitize(err.Error()))
			}
		}
	})
}

func (s *Server) handleConn(conn net.Conn, ip string, activeForIP, total int) {
	defer func() {
		s.tracker.release(ip)
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		s.connWG.Done()
	}()

	if s.config.HandshakeTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(s.config.HandshakeTimeout))
	}

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshConfig)
	if err != nil {
		if strings.Contains(err.Error(), "i/o timeout") {
			logx.Warn("Handshake timeout for", ip)
		} else {
			logx.Warn(fmt.Sprintf("Client error from %s:", ip), logx.Sanitize(err.Error()))
		}
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	logx.Debug("Handshake from", ip)
	defer func() { _ = sshConn.Close() }()

	setIndex := rand.Intn(len(s.sets))
	logx.Info(fmt.Sprintf(
		"New connection from %s (ip=%d, total=%d) -> playing %q",
		ip, activeForIP, total, s.sets[setIndex].data.Name,
	))

	go ssh.DiscardRequests(reqs)

	var sessionWG sync.WaitGroup
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		if !s.sessions.tryAcquire(sshConn, maxSessionsPerConn, s.config.MaxTotalConnections) {
			_ = newChannel.Reject(ssh.ResourceShortage, "session limit reached")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			s.sessions.release(sshConn)
			continue
		}
		sessionWG.Add(1)
		go func() {
			defer sessionWG.Done()
			defer s.sessions.release(sshConn)
			var timer *time.Timer
			if s.config.SessionTimeout > 0 {
				timer = time.AfterFunc(s.config.SessionTimeout, func() { _ = sshConn.Close() })
				defer timer.Stop()
			}
			s.handleSession(sshConn, channel, requests, ip, setIndex)
		}()
	}
	_ = sshConn.Close()
	sessionWG.Wait()
	logx.Info("Client closed connection from", ip)
}

type termSize struct {
	mu      sync.Mutex
	width   int
	height  int
	updated time.Time
}

func (t *termSize) set(w, h, maxDimension, maxCells int, force bool) {
	t.mu.Lock()
	if !force && time.Since(t.updated) < resizeDebounce {
		t.mu.Unlock()
		return
	}
	t.width, t.height = clampTermSize(w, h, maxDimension, maxCells, terminalSizeQuantum)
	t.updated = time.Now()
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
	defer func() { _ = channel.Close() }()
	size := &termSize{}
	size.set(80, 24, s.config.MaxDimension, s.config.MaxTerminalCells, true)
	tier := render.ColorTierTrueColor
	if s.config.ForceGrayscale {
		tier = render.ColorTierNone
	}

	started := false
	var playDone chan struct{}
	for req := range requests {
		switch req.Type {
		case "pty-req":
			logx.Debug("Opening pty for session", ip)
			if cols, rows, ok := parseDims(req.Payload); ok {
				size.set(cols, rows, s.config.MaxDimension, s.config.MaxTerminalCells, true)
			}
			if term, ok := parsePtyTerm(req.Payload); ok {
				tier = render.DetectColorTier(term)
				if s.config.ForceGrayscale {
					tier = render.ColorTierNone
				}
				logx.Debug(fmt.Sprintf("Client %s TERM=%q -> color tier %d", ip, logx.SanitizeN(term, 64), tier))
			}
			_ = req.Reply(true, nil)
		case "window-change":
			if len(req.Payload) >= 8 {
				cols := int(binary.BigEndian.Uint32(req.Payload))
				rows := int(binary.BigEndian.Uint32(req.Payload[4:]))
				size.set(cols, rows, s.config.MaxDimension, s.config.MaxTerminalCells, false)
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
			logx.Info(fmt.Sprintf("Client %s attempted exec: %q", ip, logx.SanitizeN(command, 512)))
			_ = req.Reply(true, nil)
			if !started {
				started = true
				playDone = make(chan struct{})
				playTier := tier
				go func(tier render.ColorTier) {
					defer close(playDone)
					s.playVideo(sshConn, channel, size, ip, initialSetIndex, false, tier)
				}(playTier)
			}
		case "shell":
			logx.Debug("Opening shell for session", ip)
			_ = req.Reply(true, nil)
			if !started {
				started = true
				playDone = make(chan struct{})
				playTier := tier
				go func(tier render.ColorTier) {
					defer close(playDone)
					s.playVideo(sshConn, channel, size, ip, initialSetIndex, false, tier)
				}(playTier)
			}
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
	_ = channel.Close()
	if playDone != nil {
		<-playDone
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
	tier render.ColorTier,
) {
	cfg := s.config
	current := s.sets[setIndex]

	w, h := size.get()
	logx.Debug(fmt.Sprintf("Terminal size %dx%d for %s", w, h, ip))

	defer func() {
		_ = writePartsWithTimeout(sshConn, channel, outputStallTimeout, showCursor)
	}()

	if s.fakeLogin != nil {
		if err := writePartsWithTimeout(
			sshConn, channel, outputStallTimeout, clearScreen, *s.fakeLogin,
		); err != nil {
			return
		}
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
			if !cfg.AllowUserControl {
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
			if now.Sub(lastSwitch) < cfg.SwitchDebounce {
				continue
			}
			lastSwitch = now
			select {
			case switchCh <- delta:
			default:
			}
		}
	}()

	loginTimer := time.NewTimer(cfg.LoginDelay)
	select {
	case <-loginTimer.C:
	case <-done:
		if !loginTimer.Stop() {
			<-loginTimer.C
		}
		return
	}

	if err := writePartsWithTimeout(sshConn, channel, outputStallTimeout, hideCursor); err != nil {
		return
	}

	frameInterval := func() time.Duration {
		return time.Duration(float64(time.Second) / current.data.FPS)
	}

	ticker := time.NewTicker(frameInterval())
	defer ticker.Stop()

	currentFrame := 0
	loopCount := 0
	lastW, lastH := 0, 0
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
			lastW, lastH = 0, 0
			logx.Debug(fmt.Sprintf("%s switched to %q", ip, current.data.Name))
			ticker.Reset(frameInterval())

		case <-ticker.C:
			w, h := size.get()
			ascii, err := current.renderer.Render(currentFrame, w, h, keepAspectRatio, tier)
			if err != nil {
				logx.Error("Render error for", ip, logx.Sanitize(err.Error()))
				_ = sshConn.Close()
				return
			}

			prefix := homeCursor
			if w != lastW || h != lastH {
				prefix = clearScreen
				lastW, lastH = w, h
			}
			if err := writeFrameWithTimeout(
				sshConn, channel, outputStallTimeout, prefix, ascii,
			); err != nil {
				closeSession()
				return
			}

			currentFrame++
			if currentFrame < len(current.data.ColorFrames) {
				continue
			}

			currentFrame = 0
			loopCount++
			if cfg.MaxLoop > 0 && loopCount >= cfg.MaxLoop {
				if err := writePartsWithTimeout(
					sshConn, channel, outputStallTimeout, showCursor, clearScreen,
				); err != nil {
					return
				}
				if s.goodbye != nil {
					if err := writePartsWithTimeout(
						sshConn, channel, outputStallTimeout, *s.goodbye,
					); err != nil {
						return
					}
				}
				closeTimer := time.NewTimer(time.Second)
				select {
				case <-closeTimer.C:
				case <-done:
					if !closeTimer.Stop() {
						<-closeTimer.C
					}
					return
				}
				logx.Info("Playback finished, closing session", ip)
				_ = channel.Close()
				_ = sshConn.Close()
				return
			}

			if cfg.PlaybackMode == config.PlaybackRandom {
				setIndex = s.pickNextSetIndex(setIndex)
				current = s.sets[setIndex]
				logx.Info(fmt.Sprintf(
					"Playthrough done for %s, switching to %q", ip, current.data.Name,
				))
				ticker.Reset(frameInterval())
			} else if cfg.MaxLoop > 0 {
				logx.Info(fmt.Sprintf(
					"Playthrough done for %s, looping %q (%d/%d)",
					ip, current.data.Name, loopCount, cfg.MaxLoop,
				))
			} else {
				logx.Info(fmt.Sprintf(
					"Playthrough done for %s, looping %q (%d)",
					ip, current.data.Name, loopCount,
				))
			}
		}
	}
}
