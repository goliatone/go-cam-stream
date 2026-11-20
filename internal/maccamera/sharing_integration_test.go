package maccamera

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

const fixtureViewer = `
async function fixtureReport(value) {
  await fetch("/fixture/seen", { method: "POST", body: value });
}
async function run() {
  const base = "/share-api/v1/shares/" + location.pathname.split("/").pop();
  const token = location.hash.slice(1);
  history.replaceState(null, "", location.pathname);
  async function send(path, method, value, csrf) {
    return fetch(base + path, {
      method, headers: {"Content-Type": "application/json", ...(csrf ? {"X-CSRF-Token":csrf} : {})},
      body: JSON.stringify(value)
    });
  }
  const exchange = await send("/redeem", "POST", {invitation:token});
  if (exchange.status !== 200) throw new Error("exchange_" + exchange.status);
  const {csrf_token:csrf} = await exchange.json();
  const refused = await send("/viewers", "POST", {}, null);
  if (refused.status !== 403) throw new Error("csrf_" + refused.status);
  async function connect(id) {
    const admission = await send("/viewers", "POST", id ? {viewer_id:id} : {}, csrf);
    if (admission.status !== 201) throw new Error("admit_" + admission.status);
    const reservation = await admission.json();
    const pc = new RTCPeerConnection();
    const video = document.querySelector("video");
    pc.addTransceiver("video", {direction:"recvonly"});
    pc.ontrack = ({track}) => {
      if (track.kind !== "video") void fixtureReport("audio");
      else video.srcObject = new MediaStream([track]);
    };
    await pc.setLocalDescription(await pc.createOffer());
    if (pc.iceGatheringState !== "complete") {
      await new Promise(resolve => {
        pc.addEventListener("icegatheringstatechange", () => {
          if (pc.iceGatheringState === "complete") resolve();
        });
      });
    }
    const path = "/viewers/" + reservation.viewer_id;
    const offer = await send(path + "/offer", "POST", {
      attempt_id: reservation.attempt_id,
      offer: {type:pc.localDescription.type, sdp:pc.localDescription.sdp}
    }, csrf);
    if (offer.status !== 200) throw new Error("offer_" + offer.status);
    const {answer} = await offer.json();
    if (/^m=audio /m.test(answer.sdp)) throw new Error("negotiated_audio");
    await pc.setRemoteDescription(answer);
    await new Promise(resolve => {
      let frames = 0;
      function count() {
        if (++frames >= 10) resolve();
        else video.requestVideoFrameCallback(count);
      }
      video.requestVideoFrameCallback(count);
    });
    return {pc, reservation, path, video};
  }
  const first = await connect();
  await fixtureReport("live");
  first.pc.close();
  const second = await connect(first.reservation.viewer_id);
  if (second.reservation.attempt_id === first.reservation.attempt_id) throw new Error("attempt_reused");
  await fixtureReport("reconnected");
  const stale = await send(first.path, "DELETE", {attempt_id:first.reservation.attempt_id}, csrf);
  if (stale.status !== 204) throw new Error("stale_close_" + stale.status);
  for (;;) {
    await new Promise(resolve => setTimeout(resolve, 300));
    const replay = await send("/redeem", "POST", {invitation:token});
    if (replay.status === 410) {
      await new Promise(resolve => setTimeout(resolve, 750));
      let newFrame = false;
      const callback = second.video.requestVideoFrameCallback(() => { newFrame = true; });
      await new Promise(resolve => setTimeout(resolve, 700));
      second.video.cancelVideoFrameCallback(callback);
      second.pc.close();
      if (newFrame) throw new Error("revoked_media_continued");
      await fixtureReport("revoked");
      return;
    }
    if (replay.status === 502) {
      second.pc.close();
      await fixtureReport("owner_offline");
      return;
    }
    if (replay.status !== 200) throw new Error("replay_" + replay.status);
  }
}
run().catch(err => fixtureReport(err.message));
`

func probePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	name := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func startSharingFixtureProcess(t *testing.T, tool string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() { _ = cmd.Wait(); close(finished) }()
	t.Cleanup(func() { cancel(); <-finished })
}

func nativeFixtureSource(t *testing.T, tools Tools) string {
	t.Helper()
	address := probePort(t)
	settings := map[string]any{"logLevel": "error", "rtsp": true, "rtspAddress": address, "rtspTransports": []string{"tcp"}, "rtmp": false, "hls": false, "webrtc": false, "srt": false, "moq": false,
		"paths": map[string]any{"synthetic": map[string]any{"source": "publisher"}}}
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := writeJSON(path, settings); err != nil {
		t.Fatal(err)
	}
	startSharingFixtureProcess(t, tools.MediaMTX, path)
	for deadline := time.Now().Add(2 * time.Second); ; {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic fixture source did not listen")
		}
		time.Sleep(20 * time.Millisecond)
	}
	source := "rtsp://" + address + "/synthetic"
	startSharingFixtureProcess(t, tools.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000", "-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-pix_fmt", "yuv420p", "-g", "30", "-bf", "0", "-c:a", "libopus",
		"-f", "rtsp", "-rtsp_transport", "tcp", source)
	return source
}

type sharingPipe struct {
	writer  *io.PipeWriter
	replies chan AppReply
	done    chan error
}

func openSharingPipe(t *testing.T, s *Service, inputs appSharingInputs) sharingPipe {
	t.Helper()
	reader, writer := io.Pipe()
	output, encoder := io.Pipe()
	p := sharingPipe{writer: writer, replies: make(chan AppReply, 32), done: make(chan error, 1)}
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	t.Cleanup(func() { cancel(); _ = writer.Close(); _ = output.Close() })
	a := &appSession{service: s, encoder: json.NewEncoder(encoder), sharingInputs: inputs, run: func(ctx context.Context) error { <-ctx.Done(); return nil }}
	go func() {
		p.done <- a.serve(ctx, reader)
		_ = encoder.Close()
	}()
	go func() {
		defer close(p.replies)
		decoder := json.NewDecoder(bufio.NewReader(output))
		for {
			var r AppReply
			if decoder.Decode(&r) != nil {
				return
			}
			select {
			case p.replies <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case r := <-p.replies:
		ready, ok := r.Result.(map[string]any)
		if r.Event != "ready" || !ok || ready["sharing"] != float64(api.ShareNativeVersion) {
			t.Fatal("no sharing capability")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no sharing handshake")
	}
	return p
}

func (p sharingPipe) call(t *testing.T, method string, args *api.NativeShareArguments) AppReply {
	t.Helper()
	if err := json.NewEncoder(p.writer).Encode(AppRequest{Version: 1, ID: method, Method: method, Sharing: args}); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(12 * time.Second)
	for {
		select {
		case r, ok := <-p.replies:
			if !ok {
				t.Fatal("owner pipe closed")
			}
			if r.ID == method {
				return r
			}
		case <-timeout:
			t.Fatal("owner request timed out")
		}
	}
}

func (p sharingPipe) end(t *testing.T) {
	t.Helper()
	if err := p.writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("owner pipe did not reap sharing")
	}
}

func TestNativeSharingBackendChrome(t *testing.T) {
	tools, chrome := os.Getenv("CAMSTREAM_TEST_SHARING_TOOLS"), os.Getenv("CAMSTREAM_TEST_SHARING_CHROME")
	if tools == "" || chrome == "" {
		t.Skip("set the lock-verified tools and Chrome paths")
	}
	s := testService(t)
	s.Tools = Tools{MediaMTX: filepath.Join(tools, "mediamtx"), FFmpeg: filepath.Join(tools, "ffmpeg"), FFprobe: filepath.Join(tools, "ffprobe")}
	source := nativeFixtureSource(t, s.Tools)
	address := probePort(t)
	target, err := url.Parse("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	observed := make(chan string, 8)
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/seen" {
			value, err := io.ReadAll(io.LimitReader(r.Body, 128))
			if err == nil {
				observed <- string(value)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reverse.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	inputs := appSharingInputs{
		assets: fstest.MapFS{api.ShareAssetEntry: {Data: []byte(`<!doctype html><video autoplay muted playsinline></video><script defer src="/share-assets/fixture.js"></script>`)},
			"assets/fixture.js": {Data: []byte(fixtureViewer)}},
		proxyClient: proxy.Client(), supervisor: os.Getenv("CAMSTREAM_TEST_SHARING_HELPER"), source: func(context.Context, string) (string, error) { return source, nil }, localMedia: true,
	}
	p := openSharingPipe(t, s, inputs)
	settings := config.Sharing{Ingress: config.SharingIngress{Mode: config.SharingIngressExternal, PublicBaseURL: proxy.URL, GuestListen: address}}
	if r := p.call(t, api.ShareNativeConfigure, &api.NativeShareArguments{Settings: &settings}); r.Error != nil {
		t.Fatal(r.Error)
	}
	create := &api.NativeShareArguments{CameraID: sharedCameraID}
	if r := p.call(t, api.ShareNativeCreate, create); r.Error == nil || r.Error.Code != string(api.ShareCameraUnavailable) {
		t.Fatal("sharing started capture implicitly")
	}
	if r := p.call(t, "start", nil); r.Error != nil {
		t.Fatal(r.Error)
	}
	r := p.call(t, api.ShareNativeCreate, create)
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	resultBytes, err := json.Marshal(r.Result)
	if err != nil {
		t.Fatal(err)
	}
	var created api.CreateShareResult
	if err = json.Unmarshal(resultBytes, &created); err != nil {
		t.Fatal(err)
	}
	spki := sha256.Sum256(proxy.Certificate().RawSubjectPublicKeyInfo)
	startSharingFixtureProcess(t, chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-breakpad", "--disable-crash-reporter",
		"--disable-sync", "--disable-extensions", "--no-proxy-server", "--autoplay-policy=no-user-gesture-required", "--use-mock-keychain",
		"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1",
		"--ignore-certificate-errors-spki-list="+base64.StdEncoding.EncodeToString(spki[:]), "--user-data-dir="+t.TempDir(), created.Link.URL)
	expect := func(value string) {
		t.Helper()
		select {
		case got := <-observed:
			if got != value {
				t.Fatalf("browser reported %q want %q", got, value)
			}
		case <-time.After(22 * time.Second):
			t.Fatalf("no browser observation %q", value)
		}
	}
	expect("live")
	expect("reconnected")
	if r := p.call(t, api.ShareNativeRevoke, &api.NativeShareArguments{ShareID: created.Share.ShareID}); r.Error != nil {
		t.Fatal(r.Error)
	}
	expect("revoked")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	audio, err := exec.CommandContext(ctx, s.Tools.FFprobe, "-v", "error", "-rtsp_transport", "tcp", "-select_streams", "a",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0", source).Output()
	if err != nil || !bytes.Contains(audio, []byte("opus")) {
		t.Fatal("independent audio consumer unavailable")
	}
	if r := p.call(t, api.ShareNativeCreate, create); r.Error != nil {
		t.Fatal(r.Error)
	}
	p.end(t)
	check, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("owner loss left a guest listener")
	}
	_ = check.Close()

	relaunch := openSharingPipe(t, s, inputs)
	status := relaunch.call(t, api.ShareNativeStatus, create)
	statusBytes, err := json.Marshal(status.Result)
	if err != nil || strings.Contains(string(statusBytes), "share_id") || !bytes.Contains(statusBytes, []byte(`"state":"stopped"`)) {
		t.Fatal("sharing resumed after owner relaunch")
	}
	relaunch.end(t)
}
