package maccamera

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Tools struct{ FFmpeg, MediaMTX string }

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
	return Tools{FFmpeg: f, MediaMTX: m}, nil
}

type Device struct {
	Index string `json:"index"`
	Name  string `json:"name"`
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
			devices = append(devices, Device{match[1], match[2]})
		}
	}
	return devices
}

func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Tools.FFmpeg, "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	// AVFoundation exits nonzero after successful enumeration.
	b, runErr := cmd.CombinedOutput()
	if runErr != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	devices := parseDevices(b)
	if len(devices) == 0 {
		return nil, errors.New("no webcams visible; allow camera access for the launching Terminal/app in macOS System Settings, then retry")
	}
	return devices, nil
}

func selectDevice(wanted string, devices []Device) (string, error) {
	for _, d := range devices {
		if wanted == d.Name || wanted == d.Index {
			return d.Name, nil
		}
	}
	if wanted == "auto" {
		for _, d := range devices {
			name := strings.ToLower(d.Name)
			if (strings.Contains(name, "macbook") || strings.Contains(name, "facetime")) && !strings.Contains(name, "desk view") {
				return d.Name, nil
			}
		}
	}
	return "", errors.New("selected webcam unavailable; run devices and configure --device with its exact name")
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
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-nostats", "-progress", "pipe:1",
		"-f", "avfoundation", "-framerate", fmt.Sprint(c.FPS), "-video_size", fmt.Sprintf("%dx%d", c.Width, c.Height), "-i", c.Device + ":none",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast", "-tune", "zerolatency", "-g", fmt.Sprint(c.FPS * 2),
		"-b:v", "2000k", "-f", "rtsp", "-rtsp_transport", "tcp", "rtsp://" + net.JoinHostPort(publisherHost(c), fmt.Sprint(c.Port)) + "/" + c.Path}
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
