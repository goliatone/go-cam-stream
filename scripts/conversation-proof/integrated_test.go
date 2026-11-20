package conversationproof

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

//go:embed integrated-media.js
var integratedMedia string

// endpointInfo declares camera and microphone use like the app bundle does.
// WebKit leaves navigator.mediaDevices undefined in an app without these
// keys, and a bare swiftc binary has no Info.plist of its own.
const endpointInfo = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.goliatone.camstream.conversation-proof</string>
	<key>NSCameraUsageDescription</key>
	<string>The conversation proof sends synthetic camera video.</string>
	<key>NSMicrophoneUsageDescription</key>
	<string>The conversation proof sends synthetic microphone audio.</string>
</dict>
</plist>
`

type proofProcess struct {
	input io.WriteCloser
	lines *bufio.Reader
	wait  func() error
}

func launch(t *testing.T, ctx context.Context, exe string, args ...string) proofProcess {
	t.Helper()
	process := exec.CommandContext(ctx, exe, args...)
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	wait := sync.OnceValue(process.Wait)
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = process.Process.Kill()
		_ = wait()
	})
	return proofProcess{stdin, bufio.NewReader(stdout), wait}
}

func (p proofProcess) read(result any) error {
	line, err := p.lines.ReadBytes('\n')
	if err != nil {
		return err
	}
	if err := json.Unmarshal(line, result); err != nil {
		return fmt.Errorf("invalid proof reply")
	}
	return nil
}

type ownerReply struct {
	ID     string
	Event  string
	Result json.RawMessage
	Error  *struct{ Code string }
}

func ownerPipe(t *testing.T, ctx context.Context, helper string, trusted ...string) proofProcess {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	configJSON, err := json.Marshal(config.Conversations{Loopback: true, TrustedOrigins: trusted})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation-settings.json"), configJSON, 0600); err != nil {
		t.Fatal(err)
	}
	owner := launch(t, ctx, helper, "app-session", "--dir", dir)
	var reply ownerReply
	if err := owner.read(&reply); err != nil || reply.Event != "ready" {
		t.Fatalf("helper ready: event %q, error %v", reply.Event, err)
	}
	return owner
}

func command(t *testing.T, owner proofProcess, method, room, invitation string, result any) string {
	t.Helper()
	input := api.NativeConversationRequest{Version: 1, ID: method, Method: method, RoomID: room, InvitationURL: invitation}
	if err := json.NewEncoder(owner.input).Encode(input); err != nil {
		t.Fatal(err)
	}
	for {
		var reply ownerReply
		if err := owner.read(&reply); err != nil {
			t.Fatal(err)
		}
		if reply.Event != "" {
			continue
		}
		if reply.ID != input.ID {
			t.Fatal("unexpected helper reply")
		}
		if reply.Error != nil {
			return reply.Error.Code
		}
		if result != nil {
			if err := json.Unmarshal(reply.Result, result); err != nil {
				t.Fatal("invalid helper result")
			}
		}
		return ""
	}
}

type integratedPeer struct {
	received func(int) (stats, error)
	stop     func(bool) error
	step     func(string) error
}

func integratedBrowser(t *testing.T, browser context.Context, link string, guest, denyMic bool) integratedPeer {
	t.Helper()
	tab, stop := chromedp.NewContext(browser)
	t.Cleanup(stop)
	if err := chromedp.Run(tab); err != nil {
		t.Fatal(err)
	}
	actions := []chromedp.Action{chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(integratedMedia).Do(ctx)
		return err
	}), chromedp.Navigate(link)}
	actions = append(actions, chromedp.WaitVisible("#join-call"),
		chromedp.PollFunction("async (guest, denyMic) => await integratedProof.start(guest, denyMic)", nil,
			chromedp.WithPollingArgs(guest, denyMic), chromedp.WithPollingInterval(150*time.Millisecond)),
		chromedp.WaitVisible("#remote-video"))
	timer, cancel := context.WithTimeout(tab, 10*time.Second)
	defer cancel()
	if err := chromedp.Run(timer, actions...); err != nil {
		var state string
		_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify({ title:document.querySelector('h1')?.textContent,
			problem:document.querySelector('[role=alert]')?.textContent })`, &state))
		t.Fatalf("browser prejoin: %v (%s)", err, state)
	}
	return integratedPeer{
		received: func(minimumPeers int) (stats, error) {
			var received stats
			timer, cancel := context.WithTimeout(tab, 13*time.Second)
			defer cancel()
			err := chromedp.Run(timer, chromedp.PollFunction(`async (minimumPeers) => {
				const sample = await integratedProof.sample();
				return sample.audio && sample.video && sample.peers >= minimumPeers ? sample : false;
			}`, &received, chromedp.WithPollingArgs(minimumPeers), chromedp.WithPollingInterval(150*time.Millisecond)))
			if err != nil {
				var state string
				_ = chromedp.Run(tab, chromedp.Evaluate("JSON.stringify(integratedProof.status())", &state))
				return received, fmt.Errorf("%w (%s)", err, state)
			}
			return received, nil
		},
		stop: func(click bool) error {
			actions := []chromedp.Action{}
			if click {
				// The dock fades while the call is idle and ignores a press
				// until focus or pointer movement brings it back. A host
				// confirms End.
				actions = append(actions, chromedp.Focus("#leave-call", chromedp.ByQuery),
					chromedp.Click("#leave-call", chromedp.ByQuery))
				if !guest {
					actions = append(actions, chromedp.Click("#end-conversation", chromedp.ByQuery))
				}
			}
			return chromedp.Run(tab, append(actions,
				chromedp.Poll("integratedProof.closed()", nil,
					chromedp.WithPollingInterval(150*time.Millisecond), chromedp.WithPollingTimeout(15*time.Second)))...)
		},
		step: func(action string) error {
			return chromedp.Run(tab, chromedp.PollFunction(`async (action) => {
				await integratedProof.step(action); return true;
			}`, nil, chromedp.WithPollingArgs(action), chromedp.WithPollingTimeout(8*time.Second)))
		},
	}
}

func integratedWebKit(t *testing.T, ctx context.Context, executable, script string, prejoin api.NativeConversationPrejoin, startMuted bool) integratedPeer {
	t.Helper()
	args := []string{script}
	if startMuted {
		args = append(args, "--start-muted")
	}
	native := launch(t, ctx, executable, args...)
	if err := json.NewEncoder(native.input).Encode(prejoin); err != nil {
		t.Fatal(err)
	}
	type report struct {
		Received stats
		Closed   bool
		OK       bool
		Problem  string
	}
	return integratedPeer{
		received: func(_ int) (stats, error) {
			var value report
			err := native.read(&value)
			if value.Problem != "" {
				return value.Received, fmt.Errorf("WebKit media: %s", value.Problem)
			}
			return value.Received, err
		},
		stop: func(click bool) error {
			if click {
				_ = native.input.Close()
			}
			var value report
			if err := native.read(&value); err != nil {
				return err
			}
			if !value.Closed {
				return fmt.Errorf("WebKit did not close the media surface")
			}
			return native.wait()
		},
		step: func(action string) error {
			if _, err := fmt.Fprintln(native.input, action); err != nil {
				return err
			}
			var value report
			if err := native.read(&value); err != nil {
				return err
			}
			if !value.OK {
				return fmt.Errorf("WebKit action %s: %s", action, value.Problem)
			}
			return nil
		},
	}
}

func TestIntegratedConversation(t *testing.T) {
	chrome, app := os.Getenv("CAMSTREAM_PROOF_CHROME"), os.Getenv("CAMSTREAM_PROOF_APP")
	if runtime.GOOS != "darwin" || chrome == "" || app == "" {
		t.Skip("set CAMSTREAM_PROOF_CHROME and CAMSTREAM_PROOF_APP to run the Mac loopback integration proof")
	}
	native := filepath.Join(t.TempDir(), "conversation-endpoint")
	source := filepath.Join("..", "..", "macos", "CameraApp")
	swift, err := filepath.Glob(filepath.Join(source, "*.swift"))
	if err != nil || len(swift) == 0 {
		t.Fatal("missing native source")
	}
	args := []string{"swiftc", "-swift-version", "5", "-module-cache-path", filepath.Join(t.TempDir(), "swift"),
		"-import-objc-header", filepath.Join(source, "HostPowerMessages.h"), "IntegratedEndpoint.swift"}
	for _, name := range swift {
		if filepath.Base(name) != "main.swift" {
			args = append(args, name)
		}
	}
	info := filepath.Join(t.TempDir(), "Info.plist")
	if err := os.WriteFile(info, []byte(endpointInfo), 0600); err != nil {
		t.Fatal(err)
	}
	args = append(args, "-o", native,
		"-Xlinker", "-sectcreate", "-Xlinker", "__TEXT", "-Xlinker", "__info_plist", "-Xlinker", info)
	if output, err := exec.CommandContext(t.Context(), "xcrun", args...).CombinedOutput(); err != nil {
		t.Fatalf("build native WebKit integration endpoint: %v\n%s", err, output)
	}
	script, err := filepath.Abs("integrated-media.js")
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(app, "Contents", "Helpers", "camstream-camera")

	for _, pair := range []struct {
		name                                     string
		macHost, macGuest, ownerLoss, mutedStart bool
	}{
		{"Chromium-Chromium", false, false, true, false},
		{"Chromium-WebKit", false, true, false, false},
		{"WebKit-WebKit", true, true, false, false},
		{"Mac-start-Chrome-join", true, false, false, true},
	} {
		t.Run(pair.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 47*time.Second)
			defer cancel()
			owner := ownerPipe(t, ctx, helper)
			var host api.NativeConversationPrejoin
			if fault := command(t, owner, api.ConversationNativeCreate, "", "", &host); fault != "" {
				t.Fatalf("create: %s", fault)
			}
			base, err := url.Parse(host.PageURL)
			if err != nil || base.Scheme != "http" || base.Hostname() != "127.0.0.1" {
				t.Fatal("expected isolated loopback room")
			}
			if err := rejectOperatorPath(ctx, base); err != nil {
				t.Fatal(err)
			}
			origin := base.Scheme + "://" + base.Host
			guestOwner := ownerPipe(t, ctx, helper, origin)
			var browser context.Context
			if !pair.macHost || !pair.macGuest {
				browser = chromiumContext(t, ctx, chrome)
			}
			var caller, callee integratedPeer
			if pair.macHost {
				caller = integratedWebKit(t, ctx, native, script, host, pair.mutedStart)
				if pair.mutedStart {
					waitForMutedHost(t, ctx, owner, host.Admission.Room.RoomID)
				}
			} else {
				caller = integratedBrowser(t, browser, host.PageURL+"#host="+host.Admission.Token, false, false)
			}
			if pair.macGuest {
				var guest api.NativeConversationPrejoin
				if fault := command(t, guestOwner, api.ConversationNativeOpen, "", host.InvitationURL, &guest); fault != "" {
					t.Fatalf("native paste invitation: %s", fault)
				}
				if !pair.macHost {
					if fault := command(t, guestOwner, api.ConversationNativeLeave, guest.Admission.Room.RoomID, "", nil); fault != "" {
						t.Fatalf("native close before page load: %s", fault)
					}
					if fault := command(t, guestOwner, api.ConversationNativeOpen, "", host.InvitationURL, &guest); fault != "" {
						t.Fatalf("reopen after releasing the native guest seat: %s", fault)
					}
				}
				callee = integratedWebKit(t, ctx, native, script, guest, false)
			} else {
				callee = integratedBrowser(t, browser, host.InvitationURL, true, pair.mutedStart)
			}
			if fault := command(t, guestOwner, api.ConversationNativeOpen, "", host.InvitationURL, nil); fault != string(api.RoomFull) {
				t.Fatalf("third participant: %s", fault)
			}
			ref := host.Admission.Room.RoomID
			received, err := caller.received(1)
			if err != nil || received.Video == 0 {
				t.Fatalf("host received media: %+v %v", received, err)
			}
			if pair.mutedStart {
				if received.Audio != 0 {
					t.Fatal("Chrome published audio despite the simulated denial")
				}
				mustStep(t, caller, "audio:off")
				mustStep(t, callee, "mic:on")
				mustStep(t, caller, "audio:active")
				mustStep(t, caller, "devices:on")
				waitForPublication(t, ctx, owner, ref, true)
				mustStep(t, callee, "mic:off")
				mustStep(t, caller, "audio:off")
				t.Log("Camstream decoded Chrome video despite mic denial, detected audio after retry, and cleared activity when Chrome muted")
			} else if received.Audio == 0 {
				t.Fatal("host did not receive audio")
			}
			received, err = callee.received(1)
			if err != nil || received.Audio == 0 || received.Video == 0 {
				t.Fatalf("guest received media: %+v %v", received, err)
			}
			t.Logf("Guest decoded video with %d inbound video packets and %d inbound audio packets", received.Video, received.Audio)
			if pair.mutedStart {
				mustStep(t, caller, "camera:off")
				mustStep(t, caller, "mic:off")
				waitForPublication(t, ctx, owner, ref, false)
			}
			var room api.RoomView
			if fault := command(t, owner, api.ConversationNativeStatus, ref, "", &room); fault != "" {
				t.Fatalf("owner room status: %s", fault)
			}
			if pair.ownerLoss {
				mustStep(t, caller, "camera:off")
				mustStep(t, caller, "mic:off")
				waitForPublication(t, ctx, owner, ref, false)
				mustStep(t, caller, "camera:on")
				mustStep(t, caller, "mic:on")
				room = waitForPublication(t, ctx, owner, ref, true)
				if err := interruptConnection(ctx, host, room.Generation); err != nil {
					t.Fatal(err)
				}
				for _, peer := range []integratedPeer{caller, callee} {
					if recovered, err := peer.received(2); err != nil || recovered.Audio == 0 || recovered.Video == 0 {
						t.Fatalf("local generation recovery: %+v %v", recovered, err)
					}
				}
				t.Log("Both peers recovered duplex media in a new generation after mute and camera-off checks")
				_ = owner.input.Close()
				if err := owner.wait(); err != nil {
					t.Fatal(err)
				}
				if err := caller.stop(false); err != nil {
					t.Fatalf("host teardown on owner EOF: %v", err)
				}
			} else {
				if err := caller.stop(true); err != nil {
					t.Fatalf("host End: %v", err)
				}
				if fault := command(t, guestOwner, api.ConversationNativeOpen, "", host.InvitationURL, nil); fault != string(api.RoomAccessEnded) {
					t.Fatalf("stale invitation after End: %s", fault)
				}
			}
			if err := callee.stop(false); err != nil {
				t.Fatalf("guest teardown after host termination: %v", err)
			}
		})
	}
}

func mustStep(t *testing.T, peer integratedPeer, action string) {
	t.Helper()
	if err := peer.step(action); err != nil {
		t.Fatalf("%s: %v", action, err)
	}
}

func waitForMutedHost(t *testing.T, ctx context.Context, owner proofProcess, roomID string) {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var room api.RoomView
		if fault := command(t, owner, api.ConversationNativeStatus, roomID, "", &room); fault != "" {
			t.Fatalf("host status: %s", fault)
		}
		for _, participant := range room.Participants {
			if participant.Role == api.RoomHost && participant.Joined {
				if participant.Publication != (api.RoomPublication{}) {
					t.Fatal("start conversation published local media")
				}
				return
			}
		}
		select {
		case <-deadline.Done():
			t.Fatal("the Mac host was not joined by Start Conversation")
		case <-ticker.C:
		}
	}
}

func waitForPublication(t *testing.T, ctx context.Context, owner proofProcess, roomID string, enabled bool) api.RoomView {
	t.Helper()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var room api.RoomView
		if fault := command(t, owner, api.ConversationNativeStatus, roomID, "", &room); fault != "" {
			t.Fatal(fault)
		}
		for _, participant := range room.Participants {
			if participant.Role == api.RoomHost && participant.Publication.Camera == enabled && participant.Publication.Microphone == enabled {
				return room
			}
		}
		select {
		case <-timer.C:
			t.Fatal("media controls did not update the host publication state")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func interruptConnection(ctx context.Context, host api.NativeConversationPrejoin, gen api.RoomGeneration) error {
	page, err := url.Parse(host.PageURL)
	if err != nil {
		return err
	}
	const route = "/connection"
	origin := page.Scheme + "://" + page.Host
	body, err := json.Marshal(api.RoomConnectionRequest{Generation: gen, Connected: false})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		origin+api.ConversationAPIPrefix+host.Admission.Room.RoomID+route, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+host.Admission.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	res, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("generation interruption rejected: HTTP %d", res.StatusCode)
	}
	return nil
}

func rejectOperatorPath(ctx context.Context, origin *url.URL) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.Scheme+"://"+origin.Host+"/api/cameras", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		return fmt.Errorf("guest origin did not isolate operator routes")
	}
	return nil
}
