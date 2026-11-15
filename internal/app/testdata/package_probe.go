// Standalone, standard-library-only artifact smoke. It never acquires media.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	manifestBytes, err := os.ReadFile("/release/web-manifest.json")
	if err != nil {
		return err
	}
	manifest := map[string]string{}
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		return err
	}
	client := &http.Client{Timeout: time.Second}
	request := func(path, actor string, want int) ([]byte, error) {
		req, err := http.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:8080"+path, nil)
		if err != nil {
			return nil, err
		}
		req.Host = "camera-pi.example.ts.net"
		if actor != "" {
			req.Header.Set("Tailscale-User-Login", actor)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != want {
			return nil, fmt.Errorf("%s: got %d, want %d", path, resp.StatusCode, want)
		}
		return body, nil
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = request("/healthz", "", 200); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	for path, sum := range manifest {
		body, err := request("/"+path, "operator@example.test", 200)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != sum {
			return fmt.Errorf("asset mismatch: %s", path)
		}
	}
	for _, check := range []struct {
		path, actor string
		status      int
	}{{"/", "", 403}, {"/", "unknown", 403}, {"/api/cameras", "operator@example.test", 200}, {"/api/missing", "operator@example.test", 404}, {"/assets/missing.js", "operator@example.test", 404}, {"/preview.html", "operator@example.test", 404}, {"/readyz", "", 503}} {
		if _, err = request(check.path, check.actor, check.status); err != nil {
			return err
		}
	}
	fmt.Println("PASS: installed executable serves exact embedded assets and authorized catalog without Node/source; denied/unknown routes and unverified-boundary readiness fail closed")
	return nil
}
