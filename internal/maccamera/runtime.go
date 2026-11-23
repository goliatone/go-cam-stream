package maccamera

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Tools struct{ FFmpeg, MediaMTX, FFprobe string }

func ResolveTools() (Tools, error) {
	exe, err := os.Executable()
	if err != nil {
		return Tools{}, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}
	return resolveToolsAt(exe, exec.LookPath)
}

func resolveToolsAt(exe string, lookPath func(string) (string, error)) (Tools, error) {
	helperDir := filepath.Dir(exe)
	app := filepath.Base(helperDir) == "Helpers" && filepath.Base(filepath.Dir(helperDir)) == "Contents"
	find := func(name string) (string, error) {
		path := filepath.Join(filepath.Dir(exe), "tools", name)
		if app {
			path = filepath.Join(helperDir, name)
		}
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return path, nil
		}
		if app {
			return "", errors.New("bundled runtime missing")
		}
		return lookPath(name)
	}
	f, err := find("ffmpeg")
	if err != nil {
		return Tools{}, errors.New("ffmpeg missing; use the complete exported camera folder")
	}
	m, err := find("mediamtx")
	if err != nil {
		return Tools{}, errors.New("mediamtx missing; use the complete exported camera folder")
	}
	// Optional for legacy video-only folders; audio-on validates it before capture.
	p, _ := find("ffprobe") //nolint:errcheck // Optional for video-only; validateAudio rejects a missing observer.
	return Tools{FFmpeg: f, MediaMTX: m, FFprobe: p}, nil
}

type Device struct {
	Index   string `json:"index"`
	Name    string `json:"name"`
	ID      string `json:"id,omitempty"`
	builtIn bool
}

var deviceLine = regexp.MustCompile(`\] \[([0-9]+)\] (.+)$`)

func parseDevices(b []byte) []Device {
	devices := []Device{}
	video := false
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := s.Text()
		if strings.Contains(line, "AVFoundation video devices:") {
			video = true
			continue
		}
		if strings.Contains(line, "AVFoundation audio devices:") {
			break
		}
		match := deviceLine.FindStringSubmatch(line)
		if video && len(match) == 3 && !strings.Contains(strings.ToLower(match[2]), "capture screen") {
			name := strings.ToLower(match[2])
			builtIn := (strings.Contains(name, "macbook") || strings.Contains(name, "facetime")) && !strings.Contains(name, "desk view")
			devices = append(devices, Device{Index: match[1], Name: match[2], builtIn: builtIn})
		}
	}
	return devices
}

func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	read := func(options ...string) (stdout, stderr []byte, exitCode int) {
		args := append([]string{"-hide_banner", "-f", "avfoundation", "-list_devices", "true"}, options...)
		cmd := exec.CommandContext(ctx, s.Tools.FFmpeg, append(args, "-i", "")...)
		cmd.Env, cmd.WaitDelay = cleanEnvironment(), time.Second
		out := &boundedBuffer{limit: 128 << 10}
		log := &boundedBuffer{limit: 128 << 10}
		cmd.Stdout, cmd.Stderr = out, log
		// AVFoundation exits nonzero after successful enumeration.
		var exit *exec.ExitError
		if !errors.As(cmd.Run(), &exit) || out.overflow || log.overflow {
			return nil, nil, 0
		}
		return out.Bytes(), log.Bytes(), exit.ExitCode()
	}
	stdout, stderr, exitCode := read("-camstream_list_video_devices", "true")
	if bytes.Contains(stderr, []byte("Unrecognized option 'camstream_list_video_devices'")) {
		stdout, stderr, exitCode = read()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(stdout) > 0 && exitCode == 251 {
		if devices := parseIdentifiedDevices(stdout); devices != nil {
			return devices, nil
		}
	} else if len(stdout) == 0 && exitCode > 0 {
		if devices := parseDevices(stderr); len(devices) != 0 {
			return devices, nil
		}
	}
	return nil, errCameraInventory
}

const cameraUIDPrefix = "uid:"

var errCameraUnavailable = errors.New("selected webcam unavailable; check camera access or select a listed webcam")
var errCameraAmbiguous = errors.New("multiple webcams have that name; choose a webcam by ID")
var errCameraInventory = errors.New("could not read the runtime webcam inventory")
var numericCameraPrefix = regexp.MustCompile(`^\s*[+-]?[0-9]`)

func cameraUID(selector string) (string, bool) {
	encoded, ok := strings.CutPrefix(selector, cameraUIDPrefix)
	if !ok || len(encoded) > base64.RawURLEncoding.EncodedLen(1024) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	return string(raw), err == nil && base64.RawURLEncoding.EncodeToString(raw) == encoded && validAudioID(string(raw))
}

func parseIdentifiedDevices(data []byte) []Device {
	if !utf8.Valid(data) {
		return nil
	}
	var inventory struct {
		Version int `json:"version"`
		Devices []struct {
			UID     string `json:"id"`
			Name    string `json:"name"`
			BuiltIn *bool  `json:"builtin"`
		} `json:"devices"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&inventory) != nil || decoder.Decode(new(any)) != io.EOF || inventory.Version != 1 || inventory.Devices == nil {
		return nil
	}
	devices := make([]Device, 0, len(inventory.Devices))
	seen := make(map[string]bool, len(inventory.Devices))
	for index, device := range inventory.Devices {
		if !validAudioID(device.UID) || device.Name == "" || device.BuiltIn == nil || seen[device.UID] {
			return nil
		}
		seen[device.UID] = true
		devices = append(devices, Device{Index: strconv.Itoa(index), Name: device.Name,
			ID: cameraUIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(device.UID)), builtIn: *device.BuiltIn})
	}
	if body, err := json.Marshal(devices); err != nil || len(body) > audioDiscoveryLimit {
		return nil
	}
	return devices
}

func selectDevice(wanted string, devices []Device) (string, error) {
	var selected *Device
	for _, d := range devices {
		if !(wanted == "auto" && d.builtIn || wanted != "auto" && (wanted == d.Name || wanted == d.Index || wanted == d.ID)) {
			continue
		}
		if selected != nil {
			return "", errCameraAmbiguous
		}
		selected = &d
	}
	if selected == nil {
		return "", errCameraUnavailable
	}
	if selected.ID != "" {
		return selected.ID, nil
	}
	return legacyDeviceName(*selected, devices)
}

func legacyDeviceName(selected Device, devices []Device) (string, error) {
	// An older runtime can still use an unambiguous saved name. Refuse name
	// collisions even when the operator supplied a numeric discovery index.
	if len(selected.Name) > 200 || strings.ContainsAny(selected.Name, ":\r\n\x00") || strings.HasPrefix(selected.Name, "default") || strings.HasPrefix(selected.Name, "none") || numericCameraPrefix.MatchString(selected.Name) {
		return "", errCameraUnavailable
	}
	for _, d := range devices {
		if d.Index != selected.Index && strings.HasPrefix(d.Name, selected.Name) {
			return "", errCameraAmbiguous
		}
	}
	return selected.Name, nil
}

func mediaConfig(c Config, secret credentials) ([]byte, error) {
	// JSON is valid YAML. Construct data rather than interpolating device/secret text.
	permission := func(action string) map[string]string { return map[string]string{"action": action, "path": c.Path} }
	return json.Marshal(map[string]any{
		"logLevel": "info", "logDestinations": []string{"stdout"},
		"api": false, "metrics": false, "pprof": false, "playback": false,
		"rtmp": false, "hls": false, "webrtc": false, "srt": false, "moq": false,
		"rtsp": true, "rtspAddress": net.JoinHostPort(c.Listen, fmt.Sprint(c.Port)),
		"rtspTransports": []string{"tcp"}, "rtspAuthMethods": []string{"basic"},
		"authMethod": "internal", "authInternalUsers": []any{
			map[string]any{"user": "any", "ips": []string{"127.0.0.1"}, "permissions": []any{permission("publish")}},
			map[string]any{"user": secret.Username, "pass": secret.Password, "permissions": []any{permission("read")}},
		},
		"paths": map[string]any{c.Path: map[string]any{"source": "publisher", "overridePublisher": false, "record": false}},
	})
}

func ffmpegArgs(c Config) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-nostats", "-progress", "pipe:1",
		"-f", "avfoundation", "-framerate", fmt.Sprint(c.FPS), "-video_size", fmt.Sprintf("%dx%d", c.Width, c.Height)}
	if c.AudioEnabled {
		args = append(args, "-camstream_audio_uid", c.AudioDeviceID)
	}
	input := c.Device + ":none"
	uid, exact := cameraUID(c.Device)
	if exact {
		args = append(args, "-camstream_video_uid", uid)
		input = "none:none"
	}
	args = append(args, "-i", input)
	if c.AudioEnabled {
		args = append(args, "-map", "0:v:0", "-map", "0:a:0", "-c:a", "libopus", "-ac", "1", "-ar", "48000", "-b:a", "64k", "-frame_duration", "20")
	} else {
		args = append(args, "-an")
	}
	if exact {
		size := fmt.Sprintf("%d:%d", c.Width, c.Height)
		filter := fmt.Sprintf("scale=%s:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=%s:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d", size, size, c.FPS)
		args = append(args, "-vf", filter)
	}
	return append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast", "-tune", "zerolatency", "-g", fmt.Sprint(c.FPS*2),
		"-b:v", "2000k", "-f", "rtsp", "-rtsp_transport", "tcp", "rtsp://"+net.JoinHostPort(publisherHost(c), fmt.Sprint(c.Port))+"/"+c.Path)
}

func publisherHost(c Config) string {
	if c.Listen == "0.0.0.0" || c.Listen == "127.0.0.1" {
		return "127.0.0.1"
	}
	return c.Listen
}

func cleanEnvironment() []string {
	var env []string
	for _, v := range os.Environ() {
		if strings.HasPrefix(v, "MTX_") || strings.HasPrefix(v, "RTSP_") || strings.HasPrefix(v, "FFREPORT=") {
			continue
		}
		env = append(env, v)
	}
	return env
}
