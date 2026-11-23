package maccamera

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestExactCameraNormalizesVideoFormat(t *testing.T) {
	config := DefaultConfig()
	config.Device = "uid:ZGVzay12aWV3"
	config.Width, config.Height, config.FPS = 1920, 1080, 24
	args := ffmpegArgs(config)
	index := slices.Index(args, "-vf")
	if index < 0 || args[index+1] != "scale=1920:1080:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=24" {
		t.Fatal("exact video selection must preserve the configured rate and dimensions without cropping")
	}
	config.Device = "legacy camera"
	if slices.Contains(ffmpegArgs(config), "-vf") {
		t.Fatal("legacy runtimes may lack the video format filters")
	}
}

func TestWebcamPreviewLockExcludesPublisher(t *testing.T) {
	service := testService(t)
	lock, err := service.namedLock("webcam-preview.lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err := service.Supervise(context.Background(), strings.NewReader(""), nil); !errors.Is(err, errCameraBusy) {
		t.Fatalf("publisher must refuse a held webcam preview lock: %v", err)
	}
}

func TestVideoDiagnosticsClassifyAndRedact(t *testing.T) {
	for marker, code := range map[string]string{
		"Camstream video mode unavailable":     "capture_format_unsupported",
		"Selected video identity disconnected": "device_unavailable",
	} {
		diagnostics := &captureDiagnostics{}
		output := []byte(marker + " SECRET " + strings.Repeat("x", 32*1024))
		n, err := diagnostics.Write(output)
		if n != len(output) || err != nil || len(diagnostics.data) != 16*1024 {
			t.Fatal("diagnostics must drain stderr with bounded storage")
		}
		failure := CaptureFailure(diagnostics.failure())
		if failure.Code != code || strings.Contains(failure.Message, "SECRET") {
			t.Fatalf("raw FFmpeg diagnostics must not cross the worker boundary: %v", failure.Code)
		}
	}
}
