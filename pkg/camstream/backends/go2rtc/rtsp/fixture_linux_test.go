//go:build linux

package rtsp_test

import (
	"bufio"
	"context"
	"crypto/md5" // Pinned RTSP Digest uses MD5; synthetic fixture verification only.
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const fixtureUser = "alex+tag%#@example.test"
const fixturePassword = "password:@#%/日本語"
const fixtureQuery = "x=1&x=2&q=%23%2f+"
const fixturePath = "/cam//../日本語 space%"

// The upstream is a stock go2rtc instance serving the existing H.264/Opus clip.
// Helpers are fixture-only; the backend under test keeps AllowExec false.
func startOrigin(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "origin.yaml")
	config := `api:
  listen: "127.0.0.1:34000"
rtsp:
  listen: "127.0.0.1:34001"
webrtc:
  listen: ""
streams:
  stream: "exec:ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i /fixture/clip.mkv -c copy -rtsp_transport tcp -f rtsp {output}"
`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/usr/local/bin/go2rtc", "-config", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			t.Error(err)
		}
		cancel()
		_ = cmd.Wait() //nolint:errcheck // Fixture group was intentionally killed; Wait reaps it.
	})
	waitFor(t, func() bool {
		conn, err := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(ctx, "tcp", "127.0.0.1:34001")
		if err != nil {
			return false
		}
		return conn.Close() == nil
	})
}

// authProxy challenges RTSP requests before forwarding them to the synthetic
// media origin. Counters verify exact camera URI, TCP SETUP and no backchannel.
type authProxy struct {
	mode                                                     string
	challenges, authenticated, bad, describes, setups, plays atomic.Uint64
	mu                                                       sync.Mutex
	connections                                              []net.Conn
	wg                                                       sync.WaitGroup
}

func startProxy(t *testing.T, mode string) *authProxy {
	t.Helper()
	p := &authProxy{mode: mode}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:34003")
	if err != nil {
		t.Fatal(err)
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			p.mu.Lock()
			p.connections = append(p.connections, conn)
			p.mu.Unlock()
			p.wg.Add(1)
			go func() { defer p.wg.Done(); p.serve(conn) }()
		}
	}()
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Error(err)
		}
		p.mu.Lock()
		for _, conn := range p.connections {
			_ = conn.Close() //nolint:errcheck // Connections may already be closed by their handler.
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	return p
}

type rtspMessage struct {
	line   string
	header http.Header
	body   []byte
}

func readMessage(reader *bufio.Reader) (rtspMessage, error) {
	r := textproto.NewReader(reader)
	line, err := r.ReadLine()
	if err != nil {
		return rtspMessage{}, err
	}
	headers, err := r.ReadMIMEHeader()
	if err != nil {
		return rtspMessage{}, err
	}
	message := rtspMessage{line: line, header: http.Header(headers)}
	if length := message.header.Get("Content-Length"); length != "" {
		n, parseErr := strconv.Atoi(length)
		if parseErr != nil || n < 0 || n > 1<<20 {
			return message, fmt.Errorf("invalid fixture body length")
		}
		message.body = make([]byte, n)
		_, err = io.ReadFull(reader, message.body)
	}
	return message, err
}

func writeMessage(writer io.Writer, m rtspMessage) error {
	if _, err := fmt.Fprintf(writer, "%s\r\n", m.line); err != nil {
		return err
	}
	if err := m.header.Write(writer); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, "\r\n"); err != nil {
		return err
	}
	_, err := writer.Write(m.body)
	return err
}

func md5hex(parts ...string) string {
	digest := md5.Sum([]byte(strings.Join(parts, ":")))
	return hex.EncodeToString(digest[:])
}

func (p *authProxy) authorized(method, uri string, header http.Header) bool {
	auth := header.Get("Authorization")
	if p.mode == "anonymous" {
		return auth == ""
	}
	if auth == "" {
		return false
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(fixtureUser+":"+fixturePassword))
	if p.mode == "digest" {
		response := md5hex(md5hex(fixtureUser, "fixture", fixturePassword), "fixture-nonce", md5hex(method, uri))
		want = fmt.Sprintf(`Digest username="%s", realm="fixture", nonce="fixture-nonce", uri="%s", response="%s"`, fixtureUser, uri, response)
	}
	if auth != want {
		p.bad.Add(1)
		return false
	}
	p.authenticated.Add(1)
	return true
}

func (p *authProxy) challenge(conn net.Conn, request rtspMessage) error {
	challenge := `Basic realm="fixture"`
	if p.mode == "digest" {
		challenge = `Digest realm="fixture", nonce="fixture-nonce"`
	}
	p.challenges.Add(1)
	return writeMessage(conn, rtspMessage{line: "RTSP/1.0 401 Unauthorized", header: http.Header{
		"Cseq": {request.header.Get("Cseq")}, "Www-Authenticate": {challenge}, "Content-Length": {"0"},
	}})
}

func (p *authProxy) inspect(method string, target *url.URL, header http.Header) bool {
	if target.User != nil || target.Fragment != "" || header.Get("Require") != "" {
		return false
	}
	if method == "DESCRIBE" {
		p.describes.Add(1)
		if target.Path != fixturePath || target.RawQuery != fixtureQuery {
			return false
		}
	}
	if method == "SETUP" {
		p.setups.Add(1)
		if !strings.HasPrefix(header.Get("Transport"), "RTP/AVP/TCP;unicast;interleaved=") {
			return false
		}
	}
	return true
}

func (p *authProxy) serve(conn net.Conn) {
	defer conn.Close() //nolint:errcheck // Handler cleanup closes an owned test socket.
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	origin, err := (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:34001")
	if err != nil {
		p.bad.Add(1)
		return
	}
	defer origin.Close() //nolint:errcheck // Handler cleanup closes an owned test socket.
	if err = origin.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return
	}
	clientReader, originReader := bufio.NewReader(conn), bufio.NewReader(origin)
	for {
		request, readErr := readMessage(clientReader)
		if readErr != nil {
			return
		}
		parts := strings.SplitN(request.line, " ", 3)
		if len(parts) != 3 {
			p.bad.Add(1)
			return
		}
		method, uri := parts[0], parts[1]
		if !p.authorized(method, uri, request.header) {
			if p.challenge(conn, request) != nil {
				return
			}
			continue
		}
		target, parseErr := url.Parse(uri)
		if parseErr != nil || !p.inspect(method, target, request.header) {
			p.bad.Add(1)
			return
		}
		// Rewrite the synthetic route only after verifying the camera-facing URI.
		track := ""
		if i := strings.LastIndex(target.Path, "/trackID="); i >= 0 {
			track = target.Path[i:]
		}
		target.Host, target.Path, target.RawPath, target.RawQuery = "127.0.0.1:34001", "/stream"+track, "", ""
		request.line = method + " " + target.String() + " RTSP/1.0"
		request.header.Del("Authorization")
		if writeMessage(origin, request) != nil {
			return
		}
		response, responseErr := readMessage(originReader)
		if responseErr != nil {
			return
		}
		// Origin track controls are relative; supply a query-free base for SETUP.
		response.header.Set("Content-Base", "rtsp://127.0.0.1:34003/stream/")
		if writeMessage(conn, response) != nil {
			return
		}
		if method == "PLAY" {
			p.plays.Add(1)
			done := make(chan struct{})
			go func() {
				_, _ = io.Copy(origin, clientReader) //nolint:errcheck // EOF/closed sockets end fixture relay normally.
				_ = origin.Close()                   //nolint:errcheck // Unblock the reverse relay on disconnect.
				close(done)
			}()
			_, _ = io.Copy(conn, originReader) //nolint:errcheck // EOF/closed sockets end fixture relay normally.
			_ = conn.Close()                   //nolint:errcheck // Unblock the request relay on disconnect.
			<-done
			return
		}
	}
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("controlled media condition timed out")
}
