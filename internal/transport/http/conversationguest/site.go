package conversationguest

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/conversations"
)

const assetPrefix = "/conversation-assets/"

var assetName = regexp.MustCompile(`^app-[A-Za-z0-9_-]+\.(js|css)$`)
var assetReference = regexp.MustCompile(`/conversation-assets/(app-[A-Za-z0-9_-]+\.(?:js|css))`)

// NewSite combines the room API with the standalone call frontend. The
// listener exposes only guest routes, even when the host uses the same page.
func NewSite(origin string, loopback bool, files fs.FS, state *conversations.Service, bus *conversations.Commands) (http.Handler, error) {
	guest, err := newAPI(origin, loopback, state, bus)
	if err != nil || files == nil {
		return nil, api.ErrInvalidConversationRequest
	}
	bundle, err := fs.ReadFile(files, "index.html")
	if err != nil || len(bundle) == 0 {
		return nil, api.ErrInvalidConversationRequest
	}
	for _, match := range assetReference.FindAllSubmatch(bundle, -1) {
		asset, err := fs.Stat(files, string(match[1]))
		if err != nil || !asset.Mode().IsRegular() || asset.Size() == 0 {
			return nil, api.ErrInvalidConversationRequest
		}
	}
	canonical, _ := config.ConversationOrigin(origin, loopback)
	authority, _ := url.Parse(canonical)
	mux := http.NewServeMux()
	ended := terminalPage(bundle, "ended")
	expired := terminalPage(bundle, "expired")
	mux.Handle("/conversation-api/v1/rooms/", guest)
	mux.HandleFunc("GET "+api.ConversationPage, func(w http.ResponseWriter, r *http.Request) {
		if !api.ValidShareID(r.PathValue("room_id")) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := bundle
		switch state.TerminalState(roomRef(r)) {
		case api.RoomEnded:
			page = ended
		case api.RoomExpired:
			page = expired
		}
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
	})
	mux.HandleFunc("GET "+assetPrefix, func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, assetPrefix)
		if !assetName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		asset, err := fs.ReadFile(files, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(asset))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; media-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; form-action 'none'; base-uri 'none'")
		w.Header().Set("Permissions-Policy", "camera=(self), microphone=(self), autoplay=(self)")
		if r.Host != authority.Host || r.URL.RawPath != "" || path.Clean(r.URL.Path) != r.URL.Path {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func terminalPage(bundle []byte, state string) []byte {
	return bytes.Replace(bundle, []byte("</head>"), []byte(`<meta name="camstream-room-state" content="`+state+`"></head>`), 1)
}
