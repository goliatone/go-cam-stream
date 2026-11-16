// Package data owns the reference application's embedded browser assets.
package data

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

//go:embed web/index.html web/manifest.json web/assets/*
var web embed.FS

// Web wraps the API router. Only the known browser entry and exact built assets
// are served; unknown API/admin/probe/static paths never fall through to HTML.
// The host must place this handler inside its authenticated ingress guard.
func Web(next http.Handler) (http.Handler, error) {
	root, err := fs.Sub(web, "web")
	if err != nil {
		return nil, err
	}
	return webHandler(root, next)
}
func loadAssets(root fs.FS) (map[string][]byte, error) {
	manifestBytes, err := fs.ReadFile(root, "manifest.json")
	if err != nil {
		return nil, err
	}
	manifest := map[string]string{}
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for name, sum := range manifest {
		if name != "index.html" && (!strings.HasPrefix(name, "assets/") || path.Clean(name) != name || (path.Ext(name) != ".js" && path.Ext(name) != ".css" && path.Ext(name) != ".svg")) {
			return nil, errors.New("invalid asset manifest")
		}
		content, readErr := fs.ReadFile(root, name)
		if readErr != nil {
			return nil, readErr
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != sum {
			return nil, errors.New("asset digest mismatch")
		}
		files[name] = content
	}
	if !validEntry(files) {
		return nil, errors.New("missing browser assets")
	}
	return files, nil
}

// Validate the generated entry's local script/style references as well as hashes.
func validEntry(files map[string][]byte) bool {
	refs := regexp.MustCompile(`(?:src|href)="/(assets/[A-Za-z0-9_-]+\.(?:js|css))"`).FindAllSubmatch(files["index.html"], -1)
	if len(refs) < 2 {
		return false
	}
	for _, ref := range refs {
		if len(files[string(ref[1])]) == 0 {
			return false
		}
	}
	return true
}

func webHandler(root fs.FS, next http.Handler) (http.Handler, error) {
	files, err := loadAssets(root)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if r.URL.Path == "/" {
			name = "index.html"
		}
		content, ok := files[name]
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if name != "index.html" {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(content) //nolint:errcheck // A disconnected response cannot be retried.
		}
	}), nil
}
