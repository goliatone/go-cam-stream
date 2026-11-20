package shareguest

import (
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/sharing"
	"golang.org/x/time/rate"
)

var guestAssetName = regexp.MustCompile(`^[A-Za-z0-9_-]+\.(?:js|css|svg|woff2|png|webp)$`)

// New mounts only the standalone guest artifact and scoped API.
// ExternalIngress supplies the TLS origin and host boundary.
func New(assets fs.FS, commands *sharing.Commands) (http.Handler, error) {
	if assets == nil || commands == nil {
		return nil, api.ErrInvalidSharingRequest
	}
	entry, err := fs.ReadFile(assets, api.ShareAssetEntry)
	if err != nil || len(entry) == 0 || len(entry) > 256*1024 {
		return nil, api.ErrInvalidSharingRequest
	}
	handler := guestSharing{commands: commands, limiter: rate.NewLimiter(6, 24)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.SharePagePath, func(w http.ResponseWriter, r *http.Request) {
		if !api.ValidShareID(r.PathValue("share_id")) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(entry)
	})
	mux.HandleFunc("GET "+api.ShareAssetPrefix+"{asset}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("asset")
		if !guestAssetName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		file, err := assets.Open("assets/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close() //nolint:errcheck // Read-only asset.
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		_, _ = io.Copy(w, file)
	})
	mux.HandleFunc("POST "+api.ShareRedeemPath, handler.redeem)
	mux.HandleFunc("POST "+api.ShareViewersPath, handler.admit)
	mux.HandleFunc("POST "+api.ShareOfferPath, handler.offer)
	mux.HandleFunc("DELETE "+api.ShareViewerPath, handler.close)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self' blob:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	}), nil
}

type guestSharing struct {
	commands *sharing.Commands
	limiter  *rate.Limiter
}

func shareRef(r *http.Request) api.ShareRef { return api.ShareRef{ShareID: r.PathValue("share_id")} }
func shareViewerRef(r *http.Request) sharing.ShareViewerRef {
	return sharing.ShareViewerRef{ShareRef: shareRef(r), ViewerID: r.PathValue("viewer_id")}
}
func guestAuthority(r *http.Request) *http.Request {
	csrf := r.Header.Get(api.ShareCSRFHeader)
	var value sharing.Secret
	if api.ValidShareID(csrf) {
		if cookie, err := r.Cookie(api.ShareGuestCookiePrefix + csrf); err == nil {
			value = sharing.Secret(cookie.Value)
		}
	}
	return r.WithContext(sharing.WithGuest(r.Context(), value, sharing.Secret(csrf)))
}

func readSharingJSON(w http.ResponseWriter, r *http.Request, out any, limit int64) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(out) == nil && dec.Decode(new(any)) == io.EOF
}

func (h guestSharing) redeem(w http.ResponseWriter, r *http.Request) {
	var request api.RedeemShareRequest
	if !h.limiter.Allow() {
		writeShareError(w, sharing.Fault(api.ShareRateLimited))
		return
	}
	if !readSharingJSON(w, r, &request, api.ShareInvitationMax+128) {
		writeShareError(w, api.ErrInvalidSharingRequest)
		return
	}
	result, err := sharing.Dispatch[sharing.RedeemedGuest](r.Context(), h.commands, sharing.RedeemShare{ShareRef: shareRef(r), RedeemShareRequest: request})
	if err != nil {
		writeShareError(w, err)
		return
	}
	// Each page selects its own cookie using its in-memory CSRF token, so two
	// first visits cannot overwrite each other's browser authority.
	http.SetCookie(w, &http.Cookie{Name: api.ShareGuestCookiePrefix + result.Result.CSRFToken, Value: string(result.Cookie), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Expires: result.Result.ExpiresAt.Add(api.ShareReconnectWindow)})
	writeShareJSON(w, http.StatusOK, result.Result)
}

func (h guestSharing) admit(w http.ResponseWriter, r *http.Request) {
	var request api.AdmitShareViewerRequest
	if !h.limiter.Allow() {
		writeShareError(w, sharing.Fault(api.ShareRateLimited))
		return
	}
	if !readSharingJSON(w, r, &request, 1024) {
		writeShareError(w, api.ErrInvalidSharingRequest)
		return
	}
	result, err := sharing.Dispatch[api.AdmitShareViewerResult](guestAuthority(r).Context(), h.commands, sharing.AdmitShareViewer{ShareRef: shareRef(r), AdmitShareViewerRequest: request})
	if err != nil {
		writeShareError(w, err)
		return
	}
	writeShareJSON(w, http.StatusCreated, result)
}

func (h guestSharing) offer(w http.ResponseWriter, r *http.Request) {
	var request api.OpenShareViewerRequest
	if !readSharingJSON(w, r, &request, api.ShareOfferMax*2+1024) {
		writeShareError(w, api.ErrInvalidSharingRequest)
		return
	}
	result, err := sharing.Dispatch[api.OpenShareViewerResult](guestAuthority(r).Context(), h.commands, sharing.OpenShareViewer{ShareViewerRef: shareViewerRef(r), OpenShareViewerRequest: request})
	if err != nil {
		writeShareError(w, err)
		return
	}
	writeShareJSON(w, http.StatusOK, result)
}

func (h guestSharing) close(w http.ResponseWriter, r *http.Request) {
	var request api.ShareViewerAttempt
	if !readSharingJSON(w, r, &request, 1024) {
		writeShareError(w, api.ErrInvalidSharingRequest)
		return
	}
	if _, err := sharing.Dispatch[struct{}](guestAuthority(r).Context(), h.commands, sharing.CloseShareViewer{ShareViewerRef: shareViewerRef(r), ShareViewerAttempt: request}); err != nil {
		writeShareError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeShareJSON(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func writeShareError(w http.ResponseWriter, err error) {
	body := sharing.ErrorProjection(err)
	status := http.StatusServiceUnavailable
	switch body.Code {
	case api.ShareInvalidRequest:
		status = http.StatusBadRequest
	case api.ShareInvalidInvitation, api.SharePermissionDenied:
		status = http.StatusForbidden
	case api.ShareExpiryReached, api.ShareAccessRevoked:
		status = http.StatusGone
	case api.ShareViewerAtCapacity, api.ShareStaleAttempt:
		status = http.StatusConflict
	case api.ShareUnsupportedOffer:
		status = http.StatusUnprocessableEntity
	case api.ShareRateLimited:
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", "1")
	}
	writeShareJSON(w, status, api.ShareErrorResponse{Error: body})
}
