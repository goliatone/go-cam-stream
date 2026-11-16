package cameracli

import (
	"bytes"
	"context"
	"flag"
	"github.com/goliatone/go-cam-stream/internal/maccamera"
	"strings"
	"testing"
)

func TestHelpDoesNotResolveToolsOrActivateCamera(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"help"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("help failed: %s", stderr.String())
	}
	if !strings.Contains(out.String(), "configure") || !strings.Contains(out.String(), "stop") {
		t.Fatal("missing setup/lifecycle help")
	}
}

func TestAudioFlagsKeepExplicitOptIn(t *testing.T) {
	c := maccamera.DefaultConfig()
	f := flag.NewFlagSet("configure", flag.ContinueOnError)
	configFlags(f, &c)
	if err := f.Parse([]string{"--audio-device-id", "opaque: microphone"}); err != nil {
		t.Fatal(err)
	}
	if c.AudioEnabled || c.AudioDeviceID != "opaque: microphone" {
		t.Fatal(c)
	}
	if err := f.Parse([]string{"--audio-enabled"}); err != nil {
		t.Fatal(err)
	}
	if !c.AudioEnabled {
		t.Fatal("explicit opt-in ignored")
	}
}
