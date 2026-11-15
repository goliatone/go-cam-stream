package cameracli

import (
	"bytes"
	"context"
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
