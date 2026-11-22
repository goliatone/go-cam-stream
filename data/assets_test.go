package data

import (
	"io/fs"
	"maps"
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
		// Grid tiles show the page's own frames as data: URLs.
		if policy := r.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "img-src 'self' data:;") {
			t.Fatalf("%s policy does not allow data: images: %q", name, policy)
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

func TestEmbeddedDashboardIcons(t *testing.T) {
	h, err := Web(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	entry := httptest.NewRecorder()
	h.ServeHTTP(entry, httptest.NewRequestWithContext(t.Context(), "GET", "/", nil))
	for _, name := range []string{"app-icon", "favicon-idle", "favicon-live", "favicon-busy", "favicon-danger"} {
		files, err := fs.Glob(web, "web/assets/"+name+"-*.svg")
		if err != nil || len(files) != 1 {
			t.Fatalf("want one built %s SVG, got %v (%v)", name, files, err)
		}
		url := strings.TrimPrefix(files[0], "web")
		if name == "favicon-idle" && !strings.Contains(entry.Body.String(), `rel="icon" href="`+url+`"`) {
			t.Fatalf("dashboard entry does not link to %s before the app loads", url)
		}
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequestWithContext(t.Context(), "GET", url, nil))
		if r.Code != http.StatusOK || r.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(r.Body.String(), "<svg ") {
			t.Fatalf("%s did not serve as SVG: status %d, content type %q", url, r.Code, r.Header().Get("Content-Type"))
		}
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
		maps.Copy(broken, original)
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
		maps.Copy(broken, original)
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
