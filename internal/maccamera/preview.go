package maccamera

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const previewHeaderSize = 48
const previewMaxBytes = 640 * 360 * 4

// PreviewEndpoint contains no media credentials. Generation is a 16-digit hex ID.
type PreviewEndpoint struct {
	Version    int    `json:"version"`
	Socket     string `json:"socket"`
	Generation string `json:"generation"`
}

type AttachPreview struct{}

func (AttachPreview) Type() string { return "camera.preview.attach.v1" }

type AttachPreviewHandler struct{ Service *Service }

func (h AttachPreviewHandler) Run(ctx context.Context, _ AttachPreview) (PreviewEndpoint, error) {
	return h.Service.AttachPreview(ctx)
}
func (h AttachPreviewHandler) Execute(ctx context.Context, m AttachPreview) error {
	_, err := h.Run(ctx, m)
	return err
}

type DetachPreview struct{ Generation string }

func (DetachPreview) Type() string      { return "camera.preview.detach.v1" }
func (m DetachPreview) Validate() error { _, err := parsePreviewGeneration(m.Generation); return err }

type DetachPreviewHandler struct{ Service *Service }

func (h DetachPreviewHandler) Execute(ctx context.Context, m DetachPreview) error {
	if err := m.Validate(); err != nil {
		return err
	}
	return h.Service.DetachPreview(ctx, m.Generation)
}
func parsePreviewGeneration(value string) (uint64, error) {
	if len(value) != 16 {
		return 0, errors.New("invalid preview generation")
	}
	n, err := strconv.ParseUint(value, 16, 64)
	if err != nil || n == 0 {
		return 0, errors.New("invalid preview generation")
	}
	return n, nil
}
func (s *Service) AttachPreview(ctx context.Context) (PreviewEndpoint, error) {
	status, err := s.request(ctx, "preview_attach")
	if err != nil {
		return PreviewEndpoint{}, err
	}
	if status.State != "streaming" || status.Preview == nil {
		return PreviewEndpoint{}, errors.New("preview unavailable")
	}
	return *status.Preview, nil
}
func (s *Service) DetachPreview(ctx context.Context, generation string) error {
	if _, err := parsePreviewGeneration(generation); err != nil {
		return err
	}
	_, err := s.request(ctx, "preview_detach:"+generation)
	return err
}

type previewSession struct {
	endpoint PreviewEndpoint
	cancel   context.CancelFunc
	done     chan struct{}
}

// previewManager is owned only by the capture supervisor, including all decoders.
type previewManager struct {
	ctx       context.Context
	config    Config
	secret    credentials
	tool, dir string
	frames    *atomic.Int64
	mu        sync.Mutex
	active    *previewSession
	closed    bool
}

func (p *previewManager) attach() (*PreviewEndpoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.ctx.Err() != nil || p.frames.Load() == 0 {
		return nil, errors.New("preview not ready")
	}
	if p.active != nil {
		select {
		case <-p.active.done:
			p.active = nil
		default:
			return nil, errors.New("preview busy")
		}
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	nonce[0] |= 1 // Keep the generation nonzero.
	generation := hex.EncodeToString(nonce[:])
	path := filepath.Join(p.dir, "preview-"+generation+".sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		closePreviewIO(listener)
		return nil, err
	}
	ctx, cancel := context.WithCancel(p.ctx)
	session := &previewSession{endpoint: PreviewEndpoint{Version: 1, Socket: path, Generation: generation}, cancel: cancel, done: make(chan struct{})}
	p.active = session
	go func() {
		defer close(session.done)
		defer cancel()
		defer closePreviewIO(listener)
		stop := context.AfterFunc(ctx, func() { closePreviewIO(listener) })
		defer stop()
		if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return
		}
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		closePreviewIO(listener) // One connection; remove the rendezvous socket immediately.
		defer closePreviewIO(conn)
		stopConn := context.AfterFunc(ctx, func() { closePreviewIO(conn) })
		defer stopConn()
		go func() { var b [1]byte; _, _ = conn.Read(b[:]); cancel() }() //nolint:errcheck // Any input, EOF or read error ends the subscription.
		p.stream(ctx, cancel, conn, session.endpoint)
	}()
	endpoint := session.endpoint
	return &endpoint, nil
}
func (p *previewManager) detach(generation string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil && p.active.endpoint.Generation == generation {
		p.active.cancel()
		<-p.active.done
		p.active = nil
	}
}
func (p *previewManager) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.active != nil {
		p.active.cancel()
		<-p.active.done
		p.active = nil
	}
}
func previewDimensions(c Config) (int, int) {
	w, h := c.Width, c.Height
	if w > 640 {
		h = h * 640 / w
		w = 640
	}
	if h > 360 {
		w = w * 360 / h
		h = 360
	}
	return max(1, w), max(1, h)
}
func previewArgs(input string, w, h int) []string {
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-protocol_whitelist", "file,tcp,rtsp,rtp,udp",
		"-f", "concat", "-safe", "0", "-i", input, "-an", "-vf", fmt.Sprintf("fps=15,scale=%d:%d", w, h),
		"-pix_fmt", "bgra", "-c:v", "rawvideo", "-threads", "1", "-f", "rawvideo", "pipe:1"}
}
func frameHeader(generation, sequence uint64, w, h int) []byte {
	b := make([]byte, previewHeaderSize)
	copy(b, "CSPR")
	binary.BigEndian.PutUint16(b[4:], 1)
	binary.BigEndian.PutUint16(b[6:], previewHeaderSize)
	binary.BigEndian.PutUint64(b[8:], generation)
	binary.BigEndian.PutUint64(b[16:], sequence)
	binary.BigEndian.PutUint64(b[24:], uint64(time.Now().UnixMilli()))
	binary.BigEndian.PutUint32(b[32:], uint32(w))
	binary.BigEndian.PutUint32(b[36:], uint32(h))
	binary.BigEndian.PutUint32(b[40:], uint32(w*4))
	binary.BigEndian.PutUint32(b[44:], uint32(w*h*4))
	return b
}
func (p *previewManager) stream(ctx context.Context, cancel context.CancelFunc, conn net.Conn, endpoint PreviewEndpoint) {
	w, h := previewDimensions(p.config)
	source := filepath.Join(p.dir, "preview-"+endpoint.Generation+".ffconcat")
	// Generated credentials/path are restricted to safe ASCII by config validation.
	input := fmt.Sprintf("ffconcat version 1.0\nfile 'rtsp://%s:%s@127.0.0.1:%d/%s'\noption rtsp_transport tcp\noption rw_timeout 5000000\noption probesize 32768\noption analyzeduration 500000\n", p.secret.Username, p.secret.Password, p.config.Port, p.config.Path)
	if err := writePrivate(source, []byte(input)); err != nil {
		return
	}
	defer os.Remove(source) //nolint:errcheck // Private input is removed after decoder reaping.
	cmd := exec.CommandContext(ctx, p.tool, previewArgs(source, w, h)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	raw, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	producer, err := startChild(cmd)
	if err != nil {
		return
	}
	defer producer.stop()
	// A watchdog cancels a stuck decoder even if it has not produced its first frame.
	watchdog := time.AfterFunc(8*time.Second, cancel)
	defer watchdog.Stop()
	latest := make(chan []byte, 1)
	readDone := make(chan struct{})
	generation, err := parsePreviewGeneration(endpoint.Generation)
	if err != nil {
		return
	}
	go func() {
		defer close(readDone)
		defer cancel()
		for sequence := uint64(1); ; sequence++ {
			frame := make([]byte, previewHeaderSize+w*h*4)
			if _, err := io.ReadFull(raw, frame[previewHeaderSize:]); err != nil {
				return
			}
			watchdog.Reset(5 * time.Second)
			copy(frame, frameHeader(generation, sequence, w, h))
			select {
			case latest <- frame:
			default:
				select {
				case <-latest:
				default:
				}
				select {
				case latest <- frame:
				default:
				}
			}
		}
	}()
	defer func() { cancel(); closePreviewIO(raw); <-readDone }()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-latest:
			if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
				return
			}
			if _, err := io.Copy(conn, bytes.NewReader(frame)); err != nil {
				return
			}
		}
	}
}

func closePreviewIO(c io.Closer) {
	_ = c.Close() //nolint:errcheck // Cancellation and deferred cleanup may both close owned I/O.
}
