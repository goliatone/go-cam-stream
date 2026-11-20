package conversationproof

import (
	"bufio"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

//go:embed participant.html
var participantPage []byte

//go:embed participant.js
var participantScript []byte

type signal struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// signalFixture is a disposable exchange for two preassigned local peers.
// It expires with the test server and retains one description per endpoint.
func signalFixture(t *testing.T) (*httptest.Server, [2]string) {
	t.Helper()
	var tickets [2]string
	for i := range tickets {
		tickets[i] = "Bearer " + rand.Text()
	}
	var mu sync.Mutex
	var descriptions [2]*signal
	mux := http.NewServeMux()
	for _, path := range []string{"/offer", "/answer"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(participantPage)
		})
	}
	mux.HandleFunc("GET /participant.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write(participantScript)
	})
	mux.HandleFunc("/signal", func(w http.ResponseWriter, r *http.Request) {
		var side int
		switch r.Header.Get("Authorization") {
		case tickets[0]:
			side = 0
		case tickets[1]:
			side = 1
		default:
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			answer := descriptions[1-side]
			mu.Unlock()
			if answer == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(answer)
		case http.MethodPut:
			var value signal
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
			expected := [2]string{"offer", "answer"}[side]
			if err := decoder.Decode(&value); err != nil || decoder.Decode(new(any)) != io.EOF ||
				value.Type != expected || value.SDP == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if descriptions[side] != nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
			descriptions[side] = &value
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; media-src 'self' blob:; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=()")
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, [2]string{
		server.URL + "/offer#" + strings.TrimPrefix(tickets[0], "Bearer "),
		server.URL + "/answer#" + strings.TrimPrefix(tickets[1], "Bearer "),
	}
}

type stats struct {
	Audio int `json:"audio"`
	Video int `json:"video"`
}

type endpoint struct {
	received func() (stats, error)
	leave    func() error
}

func chromium(t *testing.T, ctx context.Context, executable string) func(string) endpoint {
	t.Helper()
	browser := chromiumContext(t, ctx, executable)
	return func(url string) endpoint {
		tab, closeTab := chromedp.NewContext(browser)
		t.Cleanup(closeTab)
		if err := chromedp.Run(tab, chromedp.Navigate(url), chromedp.WaitVisible("#join"),
			chromedp.Evaluate(`(() => {
				if (window.proofProgress || document.querySelector("video").srcObject) throw new Error("prejoin media");
				document.querySelector("#join").click();
			})()`, nil)); err != nil {
			t.Fatal("could not join Chromium fixture")
		}
		return endpoint{
			received: func() (stats, error) {
				var received stats
				timer, cancel := context.WithTimeout(tab, 18*time.Second)
				defer cancel()
				err := chromedp.Run(timer, chromedp.Poll(`window.proofReceived || false`, &received,
					chromedp.WithPollingInterval(100*time.Millisecond)))
				if err != nil {
					var state string
					_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify(window.proofProgress?.())`, &state))
					return received, fmt.Errorf("%w (%s)", err, state)
				}
				return received, err
			},
			leave: func() error {
				return chromedp.Run(tab, chromedp.Evaluate(`document.querySelector("#leave").click()`, nil),
					chromedp.Poll(`window.proofClosed === true`, nil, chromedp.WithPollingInterval(100*time.Millisecond)))
			},
		}
	}
}

func chromiumContext(t *testing.T, ctx context.Context, executable string) context.Context {
	t.Helper()
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(executable), chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.Flag("no-proxy-server", true), chromedp.Flag("use-mock-keychain", true),
		chromedp.Flag("host-resolver-rules", "MAP * ~NOTFOUND, EXCLUDE 127.0.0.1"),
		chromedp.Flag("disable-features", "WebRtcHideLocalIpsWithMdns"),
		chromedp.Flag("disable-breakpad", true), chromedp.Flag("disable-crash-reporter", true))
	allocator, stop := chromedp.NewExecAllocator(ctx, options...)
	t.Cleanup(stop)
	browser, stop := chromedp.NewContext(allocator)
	t.Cleanup(stop)
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	return browser
}

func webKit(t *testing.T, ctx context.Context, executable, endpointURL string) endpoint {
	t.Helper()
	process := exec.CommandContext(ctx, executable, endpointURL)
	out, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	var reap sync.Once
	var processErr error
	wait := func() error {
		reap.Do(func() { processErr = process.Wait() })
		return processErr
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = process.Process.Kill()
		_ = wait()
	})
	reader := bufio.NewReader(out)
	var result struct {
		Received stats  `json:"received"`
		Closed   bool   `json:"closed"`
		Status   string `json:"status"`
	}
	read := func() error {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}
		if err := json.Unmarshal(line, &result); err != nil {
			return fmt.Errorf("invalid WebKit proof status")
		}
		return nil
	}
	return endpoint{
		received: func() (stats, error) { err := read(); return result.Received, err },
		leave: func() error {
			if err := in.Close(); err != nil {
				return err
			}
			if err := read(); err != nil {
				return err
			}
			if !result.Closed {
				return fmt.Errorf("WebKit endpoint did not close")
			}
			return wait()
		},
	}
}

func TestLocalDuplexMedia(t *testing.T) {
	chrome := os.Getenv("CAMSTREAM_PROOF_CHROME")
	if runtime.GOOS != "darwin" || chrome == "" {
		t.Skip("set CAMSTREAM_PROOF_CHROME on a Mac to run the local media proof")
	}
	native := filepath.Join(t.TempDir(), "conversation-endpoint")
	build := exec.CommandContext(t.Context(), "xcrun", "swiftc", "-swift-version", "5",
		"-module-cache-path", filepath.Join(t.TempDir(), "swift"), "Endpoint.swift", "-o", native)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build WebKit endpoint: %v\n%s", err, output)
	}
	for _, pair := range []struct {
		label                     string
		nativeOffer, nativeAnswer bool
	}{
		{"Chromium-Chromium", false, false},
		{"Chromium-WebKit", false, true},
		{"WebKit-WebKit", true, true},
	} {
		t.Run(pair.label, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			_, urls := signalFixture(t)
			start := func(index int, nativeSide bool) endpoint {
				if nativeSide {
					return webKit(t, ctx, native, urls[index])
				}
				// Each endpoint owns a browser so the two video renderers
				// never compete for the foreground tab.
				return chromium(t, ctx, chrome)(urls[index])
			}
			peers := []endpoint{start(0, pair.nativeOffer), start(1, pair.nativeAnswer)}
			for i, peer := range peers {
				got, err := peer.received()
				if err != nil || got.Audio <= 10 || got.Video <= 10 {
					t.Fatalf("endpoint %d did not receive both media types: %+v, %v", i+1, got, err)
				}
				t.Logf("endpoint %d received %d audio and %d video packets", i+1, got.Audio, got.Video)
			}
			for _, peer := range peers {
				if err := peer.leave(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
