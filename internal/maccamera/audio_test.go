package maccamera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudioConfigurationAndExactSelection(t *testing.T) {
	c := DefaultConfig()
	if c.AudioEnabled || c.AudioDeviceID != "" {
		t.Fatal("audio default changed")
	}
	for _, id := range []string{"", "0\x00", "bad\n", strings.Repeat("a", 1025), string([]byte{255})} {
		c.AudioEnabled, c.AudioDeviceID = true, id
		if c.Validate() == nil {
			t.Fatal("invalid UID accepted")
		}
	}
	c.AudioDeviceID = "opaque: ID with spaces"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	args := ffmpegArgs(c)
	found := false
	for i, arg := range args {
		if arg == "-camstream_audio_uid" {
			found = args[i+1] == c.AudioDeviceID
		}
	}
	joined := strings.Join(args, " ")
	if !found || !strings.Contains(joined, "-map 0:v:0 -map 0:a:0 -c:a libopus -ac 1 -ar 48000 -b:a 64k -frame_duration 20") || strings.Contains(joined, " -an ") {
		t.Fatal(args)
	}
	if !bytes.Contains(snippet(c), []byte("audio: true")) {
		t.Fatal("audio export missing")
	}
	c.AudioEnabled = false
	joined = strings.Join(ffmpegArgs(c), " ")
	if strings.Contains(joined, "camstream_audio") || !strings.Contains(joined, "-i auto:none -an") {
		t.Fatal(joined)
	}
	if bytes.Contains(snippet(c), []byte("audio: true")) {
		t.Fatal("disabled export advertises audio")
	}
}

func TestAudioDiscoveryPayloads(t *testing.T) {
	expanded := []byte(`{"version":1,"devices":[{"id":"a","name":"` + strings.Repeat("<", 30000) + `"}]}`)
	if len(expanded) > audioDiscoveryLimit {
		t.Fatal("bad expansion fixture")
	}
	if _, err := parseAudioDevices(expanded); err == nil {
		t.Fatal("expanded reply exceeds native envelope budget")
	}

	for _, raw := range []string{
		`{}`, `{"version":2,"devices":[]}`, `{"version":1,"devices":null}`, `{"version":1,"devices":[]}{}`,
		`{"version":1,"devices":[{"id":"a","name":"Mic"},{"id":"a","name":"Other"}]}`,
		`{"version":1,"devices":[{"id":"","name":"Mic"}]}`,
		`{"version":1,"devices":[{"id":"a","name":"Mic","index":0}]}`,
		strings.Repeat(" ", audioDiscoveryLimit+1),
	} {
		if _, err := parseAudioDevices([]byte(raw)); err == nil {
			t.Fatal("malformed inventory accepted")
		}
	}
	raw := `{"version":1,"devices":[{"id":"b: 2","name":"Mic"},{"id":"a","name":"Mic"},{"id":"c","name":"Mic Pro"}]}`
	list, err := parseAudioDevices([]byte(raw))
	if err != nil || len(list) != 3 || list[0].ID != "b: 2" {
		t.Fatal(list, err)
	}
}

func audioFixtureTools(t *testing.T, inventory, capture string) Tools {
	t.Helper()
	tools := fakeTools(t)
	// Synthetic metadata and capture only; no AVFoundation invocation.
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\ncase \" $* \" in\n*' -h demuxer=avfoundation '*) echo 'camstream_audio_uid camstream_list_audio_devices';;\n*' -encoders '*) echo 'libopus';;\n*' -camstream_list_audio_devices '*) cat <<'INVENTORY'\n" + inventory + "\nINVENTORY\nexit 251;;\n*' -list_devices '*) echo '[avf] AVFoundation video devices:' >&2; echo '[avf] [0] MacBook Pro Camera' >&2; echo '[avf] AVFoundation audio devices:' >&2; exit 1;;\n*) " + capture + ";;\nesac\n"
	if err := os.WriteFile(ffmpeg, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nif [ \"$1\" = '-version' ]; then echo 'ffprobe version 9.0.2'; exit; fi\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return Tools{FFmpeg: ffmpeg, FFprobe: probe, MediaMTX: tools.MediaMTX}
}

func TestAudioAdmissionAndConfigure(t *testing.T) {
	const inventory = `{"version":1,"devices":[{"id":"b","name":"Mic"},{"id":"a","name":"Mic"},{"id":"prefix","name":"Mic Pro"}]}`
	s := testService(t)
	s.Tools = audioFixtureTools(t, inventory, "exit 90")
	c, secret, loadErr := load(s.Dir)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	c.AudioEnabled, c.AudioDeviceID = true, "a"
	if err := (ConfigureHandler{Service: s}).Execute(context.Background(), Configure{Config: c}); err != nil {
		t.Fatal(err)
	}
	saved, after, err := load(s.Dir)
	if err != nil || saved.AudioDeviceID != "a" || after != secret {
		t.Fatal("selection/credentials changed", err)
	}
	// Order and labels are never identities; same-name replacement cannot match.
	for _, id := range []string{"Mic", "0", "A", "replacement"} {
		c.AudioDeviceID = id
		err := s.validateAudio(context.Background(), c)
		if CaptureFailure(err).Code != "audio_device_unavailable" {
			t.Fatal(id, err)
		}
	}
	s.Tools.FFprobe = ""
	c.AudioDeviceID = "a"
	if err := s.validateAudio(context.Background(), c); CaptureFailure(err).Code != "audio_runtime_unsupported" {
		t.Fatal(err)
	}
	c.AudioEnabled = false
	s.Tools = Tools{}
	if err := s.validateAudio(context.Background(), c); err != nil {
		t.Fatal("audio-off touched runtime", err)
	}
}

func TestAudioDiscoveryExecutionBounds(t *testing.T) {
	for _, script := range []string{
		"printf '{\"version\":1,\"devices\":[]}'; exit 0",
		"printf '{\"version\":1,\"devices\":[]}'; exit 1",
		"printf '{\"version\":1,\"devices\":[]}'; exit 251",
		"yes x | head -c 140000; exit 251",
		"exec sleep 5",
	} {
		t.Run(script, func(t *testing.T) {
			tool := filepath.Join(t.TempDir(), "discovery")
			if err := os.WriteFile(tool, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			timeout := 2 * time.Second
			if script == "exec sleep 5" {
				timeout = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			s := &Service{Tools: Tools{FFmpeg: tool}}
			devices, err := s.AudioDevices(ctx)
			valid := strings.HasSuffix(script, "exit 251") && !strings.Contains(script, "yes")
			if (err == nil) != valid || (valid && devices == nil) {
				t.Fatal(devices, err)
			}
		})
	}
}

func TestAudioLegacyDowngrade(t *testing.T) {
	s := testService(t)
	c, secret, loadErr := load(s.Dir)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	c.AudioEnabled, c.AudioDeviceID = true, "a"
	if err := writeJSON(filepath.Join(s.Dir, "camera.json"), c); err != nil {
		t.Fatal(err)
	}
	path, err := (ExportLegacyHandler{Service: s}).Run(context.Background(), ExportLegacy{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || bytes.Contains(b, []byte("audio")) {
		t.Fatal("legacy keys", err)
	}
	var legacy Config
	if err = readJSON(path, &legacy); err != nil || legacy.AudioEnabled || legacy.AudioDeviceID != "" {
		t.Fatal(legacy, err)
	}
	before := c
	before.AudioEnabled = false
	before.AudioDeviceID = ""
	if legacy != before {
		t.Fatal("video settings changed")
	}
	saved, after, err := load(s.Dir)
	if err != nil || saved != c || secret != after {
		t.Fatal("export rewrote current settings")
	}
	lock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer closePreviewIO(lock)
	if _, err := s.ExportLegacy(); err == nil {
		t.Fatal("export while active")
	}
}

func TestAudioPacketValidation(t *testing.T) {
	if _, ok := audioPacketTime("codec_type=audio|pts_time=0.020000|duration_time=0.020000|"); !ok {
		t.Fatal("empty side-data section rejected")
	}
	if _, ok := audioPacketTime("codec_type=audio|pts_time=0.020000|duration_time=0.020000|side_datum/rtcp_sender_report:side_data_type=RTCP Sender Report"); !ok {
		t.Fatal("packet timing side data rejected")
	}
	if _, ok := audioPacketTime("codec_type=audio|pts_time=-0.006500|duration_time=0.020000"); !ok {
		t.Fatal("valid silent packet rejected")
	}
	for _, line := range []string{"frame=10", "codec_type=video|pts_time=0|duration_time=.02", "codec_type=audio|pts_time=NaN|duration_time=.02", "codec_type=audio|pts_time=0|duration_time=0", "codec_type=audio|pts_time=0|pts_time=1"} {
		if _, ok := audioPacketTime(line); ok {
			t.Fatal(line)
		}
	}
}

func TestAudioHelperContractAndFailure(t *testing.T) {
	s := testService(t)
	c, _, loadErr := load(s.Dir)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	c.AudioEnabled, c.AudioDeviceID = true, "a"
	if err := writeJSON(filepath.Join(s.Dir, "camera.json"), c); err != nil {
		t.Fatal(err)
	}
	p := newSessionProbe(t, s, func(context.Context) error { return audioFailure("audio_stalled") })
	ready := p.receive(t)
	b := fixtureJSON(t, ready.Result)
	if !bytes.Contains(b, []byte(`"audio":1`)) {
		t.Fatal(string(b))
	}
	r := p.request(t, "start", nil)
	b = fixtureJSON(t, r.Result)
	if !bytes.Contains(b, []byte(`"state":"waiting"`)) {
		t.Fatal(string(b))
	}
	stopped := p.receive(t)
	if stopped.Event != "capture_stopped" || stopped.Error.Code != "audio_stalled" {
		t.Fatal(stopped)
	}
	b = fixtureJSON(t, stopped.Result)
	if !bytes.Contains(b, []byte(`"state":"failed"`)) {
		t.Fatal(string(b))
	}
	if r := p.request(t, "status", nil); r.Error == nil || r.Error.Code != "audio_stalled" {
		t.Fatal(r)
	}
	p.request(t, "stop", nil)
	if r := p.request(t, "status", nil); r.Error != nil {
		t.Fatal("stale failure after stop", r)
	}
	p.request(t, "quit", nil)
	p.finished(t)
}

func TestAudioRemovalBeforeOpenAndStartupDeadline(t *testing.T) {
	for _, mode := range []string{"removed", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(t)
			s.Tools = audioFixtureTools(t, `{"version":1,"devices":[{"id":"a","name":"Mic"}]}`, "exit 91")
			c, _, loadErr := load(s.Dir)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			c.AudioEnabled, c.AudioDeviceID = true, "a"
			if err := writeJSON(filepath.Join(s.Dir, "camera.json"), c); err != nil {
				t.Fatal(err)
			}
			if mode == "deadline" {
				s.StartupDeadline = time.Now().Add(-time.Second)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			in, out := io.Pipe()
			defer closePreviewIO(in)
			defer closePreviewIO(out)
			err := s.Supervise(ctx, in, io.Discard)
			wanted := "audio_capture_failed"
			if mode == "deadline" {
				wanted = "audio_startup_timeout"
			}
			var ae audioError
			if !errors.As(err, &ae) || string(ae) != wanted {
				t.Fatal(err)
			}
			dir, dirErr := s.runtimeDir()
			if dirErr != nil {
				t.Fatal(dirErr)
			}
			if _, err := os.Stat(filepath.Join(dir, "mediamtx.json")); !os.IsNotExist(err) {
				t.Fatal("credentials retained")
			}
		})
	}
}

func fixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAudioSharedProtocolFixtures(t *testing.T) {
	b, err := os.ReadFile("../../macos/fixtures/audio-protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		LegacyReady    AppReply          `json:"legacy_ready"`
		Ready          AppReply          `json:"ready"`
		LegacyConfig   Config            `json:"legacy_config"`
		DisabledConfig Config            `json:"disabled_config"`
		Requests       []json.RawMessage `json:"requests"`
		DiscoveryReply AppReply          `json:"discovery_reply"`
		Statuses       []Status          `json:"statuses"`
		FailureEvent   AppReply          `json:"failure_event"`
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, raw := range fixture.Requests {
		if _, err = decodeAppRequest(raw); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.LegacyConfig.AudioEnabled || fixture.LegacyConfig.AudioDeviceID != "" || fixture.DisabledConfig.AudioEnabled {
		t.Fatal("legacy/off fixture enables audio")
	}
	if fixture.FailureEvent.Error.Message != audioMessages[fixture.FailureEvent.Error.Code] {
		t.Fatal("error fixture drift")
	}
	if len(fixture.Statuses) != 6 {
		t.Fatal("missing presentation state")
	}
}

func TestAudioLateStartDoesNotSurviveStop(t *testing.T) {
	s := testService(t)
	p := newSessionProbe(t, s, func(ctx context.Context) error { <-ctx.Done(); return audioFailure("audio_startup_timeout") })
	p.receive(t)
	p.request(t, "start", nil)
	if r := p.request(t, "stop", nil); r.Error != nil {
		t.Fatal(r)
	}
	// No stale capture_stopped event or failed status may override this Stop.
	if r := p.request(t, "status", nil); r.Error != nil {
		t.Fatal(r)
	}
	p.request(t, "start", nil)
	p.request(t, "quit", nil)
	p.finished(t)
}

func TestAudioReadinessDeadline(t *testing.T) {
	now := time.Now()
	progress := audioProgress{deadline: now}
	late := now.Add(time.Millisecond)
	progress.first.Store(&late)
	progress.packets.Store(1)
	c := DefaultConfig()
	c.AudioEnabled = true
	if mediaReady(c, 10, &progress) {
		t.Fatal("late audio admitted as streaming")
	}
	early := now.Add(-time.Millisecond)
	progress.first.Store(&early)
	if !mediaReady(c, 10, &progress) {
		t.Fatal("timely silent audio rejected")
	}
	progress.packets.Store(0)
	if mediaReady(c, 10, &progress) {
		t.Fatal("video-only progress admitted")
	}
}

func TestAudioObserverRejectsMalformedProgress(t *testing.T) {
	for _, output := range []string{
		"frame=10\n",
		"codec_type=audio|pts_time=0|duration_time=.02\ncodec_type=audio|pts_time=0|duration_time=.02\n",
		strings.Repeat("x", 5000) + "\n",
	} {
		s := testService(t)
		c, secret, err := load(s.Dir)
		if err != nil {
			t.Fatal(err)
		}
		c.AudioEnabled = true
		dir, err := s.runtimeDir()
		if err != nil {
			t.Fatal(err)
		}
		if err = privateDir(dir); err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(t.TempDir(), "metadata")
		if err = os.WriteFile(source, []byte(output), 0600); err != nil {
			t.Fatal(err)
		}
		tool := filepath.Join(t.TempDir(), "observer")
		quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
		if err = os.WriteFile(tool, []byte("#!/bin/sh\ncat "+quote(source)+"\nexec sleep 10\n"), 0700); err != nil {
			t.Fatal(err)
		}
		s.Tools.FFprobe = tool
		observer, err := s.observeAudio(c, secret, dir, &audioProgress{})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case failure := <-observer.done:
			if CaptureFailure(failure).Code != "audio_observer_failed" {
				t.Fatal(failure)
			}
		case <-time.After(time.Second):
			observer.close()
			t.Fatal("malformed observer output did not fail")
		}
		observer.close()
		if _, err = os.Stat(filepath.Join(dir, "audio.ffconcat")); !os.IsNotExist(err) {
			t.Fatal("observer credentials retained")
		}
	}
}
