package maccamera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const audioStartupTimeout = 20 * time.Second
const audioStallTimeout = 5 * time.Second
const audioDiscoveryLimit = 128 * 1024

var audioMessages = map[string]string{
	"audio_selection_invalid":   "Select a microphone by its device ID, or disable microphone audio.",
	"audio_device_unavailable":  "Selected microphone is unavailable; select it again or disable audio, then explicitly restart.",
	"audio_discovery_failed":    "Microphone discovery failed; check permission and the bundled runtime, then retry.",
	"audio_runtime_unsupported": "Microphone audio requires a verified audio-capable runtime; restore the complete bundle or disable audio.",
	"audio_startup_timeout":     "No published audio within 20 seconds; check microphone permission and selection, then explicitly restart.",
	"audio_stalled":             "Microphone audio stopped progressing for 5 seconds; check the device, then explicitly restart.",
	"audio_observer_failed":     "Audio verification failed; capture stopped. Check the bundled runtime, then explicitly restart.",
	"audio_capture_failed":      "Audio capture stopped; check camera and microphone permission, device availability and runtime, then explicitly restart.",
}

type audioError string

func (e audioError) Error() string   { return audioMessages[string(e)] }
func audioFailure(code string) error { return audioError(code) }

// CaptureFailure crosses the worker boundary using only allowlisted codes and
// fixed text, never runtime diagnostics, private device IDs or credentials.
func CaptureFailure(err error) *AppError {
	if e, ok := errors.AsType[audioError](err); ok {
		return &AppError{Code: string(e), Message: e.Error()}
	}
	if errors.Is(err, errCameraAmbiguous) {
		return appError("device_ambiguous")
	}
	if errors.Is(err, errCameraUnavailable) {
		return appError("device_unavailable")
	}
	if errors.Is(err, errCameraFormat) {
		return appError("capture_format_unsupported")
	}
	if errors.Is(err, errCameraNoFrames) {
		return appError("capture_no_frames")
	}
	return appError("capture_failed")
}

func validAudioID(id string) bool {
	if len(id) == 0 || len(id) > 1024 || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type AudioDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.limit-b.Len() {
		b.overflow = true
		p = p[:b.limit-b.Len()]
	}
	_, _ = b.Buffer.Write(p)
	if b.overflow {
		return len(p), errors.New("runtime output exceeds limit")
	}
	return n, nil
}

// boundedOutput bounds memory, execution and inherited-pipe lifetime. Stderr is
// discarded: FFmpeg can include device identifiers and authenticated URLs there.
func boundedOutput(ctx context.Context, tool string, limit int, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = cleanEnvironment()
	cmd.WaitDelay = time.Second
	b := &boundedBuffer{limit: limit}
	cmd.Stdout = b
	err := cmd.Run()
	if b.overflow {
		return nil, errors.New("runtime output exceeds limit")
	}
	return b.Bytes(), err
}

func parseAudioDevices(b []byte) ([]AudioDevice, error) {
	var payload struct {
		Version int           `json:"version"`
		Devices []AudioDevice `json:"devices"`
	}
	if len(b) > audioDiscoveryLimit || !utf8.Valid(b) {
		return nil, audioFailure("audio_discovery_failed")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&payload) != nil || d.Decode(new(any)) != io.EOF || payload.Version != 1 || payload.Devices == nil {
		return nil, audioFailure("audio_discovery_failed")
	}
	seen := make(map[string]bool)
	for _, device := range payload.Devices {
		if !validAudioID(device.ID) || device.Name == "" || seen[device.ID] {
			return nil, audioFailure("audio_discovery_failed")
		}
		seen[device.ID] = true
	}
	// Bound the re-encoded helper result too: HTML/Unicode escaping can expand
	// an otherwise bounded discovery document beyond the native reply buffer.
	encoded, err := json.Marshal(payload.Devices)
	if err != nil || len(encoded) > audioDiscoveryLimit {
		return nil, audioFailure("audio_discovery_failed")
	}
	return payload.Devices, nil
}

// AudioDevices is metadata-only; the patched demuxer does not create a capture
// input/session. Exit 251 is the pinned FFmpeg enumeration exit, not a fallback.
func (s *Service) AudioDevices(ctx context.Context) ([]AudioDevice, error) {
	ctx, cancel := context.WithTimeout(ctx, audioStartupTimeout)
	defer cancel()
	b, err := boundedOutput(ctx, s.Tools.FFmpeg, audioDiscoveryLimit,
		"-hide_banner", "-loglevel", "error", "-f", "avfoundation", "-camstream_list_audio_devices", "true", "-i", "")
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 251 {
		return nil, audioFailure("audio_discovery_failed")
	}
	return parseAudioDevices(b)
}

func (s *Service) validateAudio(ctx context.Context, c Config) error {
	if !c.AudioEnabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, audioStartupTimeout)
	defer cancel()
	if s.Tools.FFprobe == "" {
		return audioFailure("audio_runtime_unsupported")
	}
	help, err := boundedOutput(ctx, s.Tools.FFmpeg, 128*1024, "-hide_banner", "-h", "demuxer=avfoundation")
	if err != nil || !bytes.Contains(help, []byte("camstream_audio_uid")) || !bytes.Contains(help, []byte("camstream_list_audio_devices")) {
		return audioFailure("audio_runtime_unsupported")
	}
	encoders, err := boundedOutput(ctx, s.Tools.FFmpeg, 128*1024, "-hide_banner", "-encoders")
	if err != nil || !bytes.Contains(encoders, []byte("libopus")) {
		return audioFailure("audio_runtime_unsupported")
	}
	probe, err := boundedOutput(ctx, s.Tools.FFprobe, 128*1024, "-version")
	if err != nil || !strings.HasPrefix(string(probe), "ffprobe version 9.0.2") {
		return audioFailure("audio_runtime_unsupported")
	}
	devices, err := s.AudioDevices(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices {
		if device.ID == c.AudioDeviceID {
			return nil
		}
	}
	return audioFailure("audio_device_unavailable")
}

type AudioStatus struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	Packets int64  `json:"packets"`
}

type audioProgress struct {
	packets  atomic.Int64
	first    atomic.Pointer[time.Time]
	deadline time.Time
}

func (p *audioProgress) status(c Config, state string) AudioStatus {
	a := AudioStatus{Enabled: c.AudioEnabled, State: "disabled"}
	if !c.AudioEnabled {
		return a
	}
	a.State = state
	if p != nil {
		a.Packets = p.packets.Load()
		if state == "starting" || state == "streaming" {
			a.State = "waiting"
			if p.ready() {
				a.State = "live"
			}
		}
	}
	return a
}

func (p *audioProgress) ready() bool {
	first := p.first.Load()
	return p.packets.Load() > 0 && first != nil && (p.deadline.IsZero() || !first.After(p.deadline))
}

func (s *Service) audioPreflight(ctx context.Context, c Config, deadline time.Time) error {
	if !c.AudioEnabled {
		return nil
	}
	preflight, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	err := s.validateAudio(preflight, c)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if preflight.Err() != nil {
		return audioFailure("audio_startup_timeout")
	}
	return err
}
