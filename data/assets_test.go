package data

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedWeb(t *testing.T) {
	h, err := Web(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/", "/index.html"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequestWithContext(t.Context(), "GET", name, nil))
		if r.Code != 200 || !strings.Contains(r.Body.String(), "/assets/") || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(name, r.Code)
		}
	}
	for _, name := range []string{"/api/missing", "/api/cameras/missing", "/admin/missing", "/healthz/missing", "/readyz/missing", "/assets/missing.js", "/web/manifest.json", "/preview.html", "/cameras/unknown"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequestWithContext(t.Context(), "GET", name, nil))
		if r.Code != 404 {
			t.Fatal(name, r.Code)
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequestWithContext(t.Context(), "HEAD", "/", nil))
	if r.Code != 200 || r.Body.Len() != 0 || r.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD")
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequestWithContext(t.Context(), "POST", "/", nil))
	if r.Code != 405 {
		t.Fatal("unsafe asset request")
	}
}
func TestMissingOrCorruptAssetsFailConstruction(t *testing.T) {
	root, err := fs.Sub(web, "web")
	if err != nil {
		t.Fatal(err)
	}
	original := fstest.MapFS{}
	if err = fs.WalkDir(root, ".", func(name string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		original[name] = &fstest.MapFile{Data: b}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "manifest.json"} {
		broken := fstest.MapFS{}
		for k, v := range original {
			broken[k] = v
		}
		delete(broken, name)
		if _, err = webHandler(broken, http.NotFoundHandler()); err == nil {
			t.Fatal("accepted missing", name)
		}
	}
	for name := range original {
		if !strings.HasPrefix(name, "assets/") {
			continue
		}
		broken := fstest.MapFS{}
		for k, v := range original {
			broken[k] = v
		}
		delete(broken, name)
		if _, err = webHandler(broken, http.NotFoundHandler()); err == nil {
			t.Fatal("accepted missing", name)
		}
		broken[name] = &fstest.MapFile{Data: []byte("changed")}
		if _, err = webHandler(broken, http.NotFoundHandler()); err == nil {
			t.Fatal("accepted corruption", name)
		}
	}
}
