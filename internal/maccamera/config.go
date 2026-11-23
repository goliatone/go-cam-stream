// Package maccamera manages a standalone macOS webcam RTSP producer.
// It does not import or change the Linux camstream controller.
package maccamera

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MediaMTXVersion = "v1.21.3"

type Config struct {
	AudioEnabled  bool   `json:"audio_enabled"`
	AudioDeviceID string `json:"audio_device_id"`
	Device        string `json:"device"`
	Listen        string `json:"listen"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Path          string `json:"path"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPS           int    `json:"fps"`
}

func DefaultConfig() Config {
	return Config{Device: "auto", Listen: "127.0.0.1", Host: "localhost", Port: 8554, Path: "macbook", Width: 1280, Height: 720, FPS: 30}
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var hostName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)

func (c Config) Validate() error {
	if strings.HasPrefix(c.Device, cameraUIDPrefix) {
		if _, ok := cameraUID(c.Device); !ok {
			return errors.New("invalid webcam ID")
		}
	} else {
		if c.Device == "" || len(c.Device) > 200 || strings.ContainsAny(c.Device, ":\r\n\x00") || strings.Contains(strings.ToLower(c.Device), "capture screen") {
			return errors.New("choose a named webcam; screen capture is not supported")
		}
	}
	// IPv4 binding keeps the local publisher endpoint unambiguous.
	if c.Listen != "127.0.0.1" && c.Listen != "0.0.0.0" {
		return errors.New("listen must be 127.0.0.1 or 0.0.0.0")
	}
	if !hostName.MatchString(c.Host) || strings.Contains(c.Host, "..") {
		return errors.New("host must be a bare IPv4 address or DNS hostname reachable by camstream")
	}
	if c.Port < 1024 || c.Port > 65535 || !safeID.MatchString(c.Path) {
		return errors.New("use port 1024–65535 and a simple alphanumeric stream path")
	}
	if c.AudioEnabled || c.AudioDeviceID != "" {
		if !validAudioID(c.AudioDeviceID) {
			return audioFailure("audio_selection_invalid")
		}
	}
	return c.validateVideo()
}

func (c Config) validateVideo() error {
	if c.Width < 160 || c.Width > 3840 || c.Height < 120 || c.Height > 2160 || c.Width%2 != 0 || c.Height%2 != 0 || c.FPS < 1 || c.FPS > 60 {
		return errors.New("use even dimensions within 160x120–3840x2160 and 1–60 fps")
	}
	return nil
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func newCredentials() (credentials, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return credentials{}, err
	}
	return credentials{Username: "camera", Password: hex.EncodeToString(b)}, nil
}

func (c credentials) validate() error {
	if c.Username != "camera" || len(c.Password) != 48 {
		return errors.New("invalid credentials file")
	}
	if _, err := hex.DecodeString(c.Password); err != nil {
		return errors.New("invalid credentials file")
	}
	return nil
}

func readJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("configuration must be a private regular file (mode 0600)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) > 16384 {
		return errors.New("configuration is too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(value); err != nil {
		return errors.New("invalid configuration JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("configuration must contain one JSON object")
	}
	return nil
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, append(b, '\n'))
}

func writePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".camera-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name) //nolint:errcheck // Temporary file may already have been renamed.
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func load(dir string) (Config, credentials, error) {
	var c Config
	var secret credentials
	if err := readJSON(filepath.Join(dir, "camera.json"), &c); err != nil {
		return c, secret, err
	}
	if err := c.Validate(); err != nil {
		return c, secret, err
	}
	if err := readJSON(filepath.Join(dir, "credentials.json"), &secret); err != nil {
		return c, secret, err
	}
	return c, secret, secret.validate()
}

func (c Config) URL() string {
	return "rtsp://" + net.JoinHostPort(c.Host, fmt.Sprint(c.Port)) + "/" + c.Path
}

func snippet(c Config) []byte {
	return []byte(fmt.Sprintf(`# Merge this camera and grant into the Linux camstream configuration.
cameras:
  - id: %s
    name: MacBook Camera
    physical_id: %s
    strategy: rtsp
    config:
      host: %q
      port: %d
      username_ref: env://MAC_CAMERA_USERNAME
      password_ref: env://MAC_CAMERA_PASSWORD
      profiles:
        - {id: default, path: /%s, audio: %t}
access:
  - login: REPLACE_WITH_YOUR_SERVE_LOGIN
    grants:
      - {camera_id: %s, streams: [default], view: true, diagnose: true, reconnect: true}
`, c.Path, c.Path, c.Host, c.Port, c.Path, c.AudioEnabled, c.Path))
}
