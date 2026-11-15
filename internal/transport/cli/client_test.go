package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goliatone/go-cam-stream/internal/transport/cli"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func TestClientRejectsRedirectAndMissingServer(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Tailscale-User-Login") != "" {
			t.Error("client manufactured identity")
		}
		http.Redirect(w, r, "https://invalid.example", http.StatusFound)
	}))
	client, err := cli.NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.List(context.Background()); cs.DescribeError(err).Code != cs.CodeServerUnavailable {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("redirect/retry", calls)
	}
	server.Close()
	if _, err = client.List(context.Background()); cs.DescribeError(err).Code != cs.CodeServerUnavailable {
		t.Fatal("stopped server", err)
	}
	if _, err = cli.NewClient("http://127.0.0.1:8080", nil); cs.DescribeError(err).Code != cs.CodeInvalidArgument {
		t.Fatal("HTTP bypass", err)
	}
}
func TestOfflineValidationAndNoIdentityFlag(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if code := cli.Run(context.Background(), []string{"config", "validate", "--config", "../../../config/testdata/ec70.yaml"}, &output, &diagnostics, nil); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	if output.String() != "{\"valid\":true}\n" {
		t.Fatal(output.String())
	}
	output.Reset()
	diagnostics.Reset()
	if code := cli.Run(context.Background(), []string{"list", "--origin", "https://example.test", "--login", "owner"}, &output, &diagnostics, nil); code == 0 {
		t.Fatal("identity flag accepted")
	}
}
