package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-auth/middleware/csrf"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	router "github.com/goliatone/go-router"
)

type principalKey struct{}
type authenticator struct{}

func (authenticator) Wrap(c router.Context) error {
	return authenticator{}.WrapHandler(func(c router.Context) error { return c.Next() })(c)
}
func (authenticator) WrapHandler(next router.HandlerFunc) router.HandlerFunc {
	return func(c router.Context) error {
		if login(c.Context()) == "" {
			return replyError(c, cs.NewError(cs.CodeAccessDenied, nil))
		}
		return next(c)
	}
}
func (authenticator) Can(context.Context, string, string) bool { return false } // no generic admin permissions
var _ admin.HandlerAuthenticator = authenticator{}

func login(ctx context.Context) string {
	v, ok := ctx.Value(principalKey{}).(string)
	if !ok {
		return ""
	}
	return v
}
func csrfProtection(installation string) router.MiddlewareFunc {
	return csrf.New(csrf.Config{TokenLookup: "header:" + api.CSRFHeader, SafeMethods: []string{http.MethodGet, http.MethodHead, http.MethodOptions}, Expiration: 30 * time.Minute, DisableTemplateHelpers: true,
		SessionKeyResolver: func(c router.Context) (string, bool) {
			p := login(c.Context())
			if p == "" {
				return "", false
			}
			sum := sha256.Sum256([]byte(installation + "\x00" + p))
			return hex.EncodeToString(sum[:]), true
		},
		ErrorHandler: func(c router.Context, _ error) error { return replyError(c, cs.NewError(cs.CodeCSRFInvalid, nil)) },
	})
}

// Trust only the raw loopback peer, never forwarding headers. The host must bind
// loopback and keep local processes trusted; this is not public-header auth.
func guard(next http.Handler, origin string, access []config.Access, ready func() bool) http.Handler {
	allow := map[string]bool{}
	for _, a := range access {
		allow[a.Login] = true
	}
	return guardWithAdmission(next, origin, func() map[string]bool { return allow }, ready)
}

func guardWithAdmission(next http.Handler, origin string, principals func() map[string]bool, ready func() bool) http.Handler {
	u, parseErr := url.Parse(origin)
	if parseErr != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeError(w, cs.CodeAccessDenied) })
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			writeError(w, cs.CodeAccessDenied)
			return
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path == "/readyz" && !ready() {
				writeError(w, cs.CodeBackendUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"ok\":true}\n")) //nolint:errcheck // The response is complete; a disconnected caller needs no second write.
			return
		}
		ctx, err := authenticatedContext(r, u.Host, origin, principals())
		if err != nil {
			writeError(w, cs.DescribeError(err).Code)
			return
		}

		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
		next.ServeHTTP(w, r)
	})
}
func writeError(w http.ResponseWriter, code cs.ErrorCode) {
	body := api.PublicError(cs.NewError(code, nil))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(api.ErrorStatus(body.Error.Code))
	_ = json.NewEncoder(w).Encode(body) //nolint:errcheck,errchkjson // A failed response write cannot be retried safely.
}
func replyError(c router.Context, err error) error {
	body := api.PublicError(err)
	return c.JSON(api.ErrorStatus(body.Error.Code), body)
}

func authenticatedContext(r *http.Request, host, origin string, allow map[string]bool) (context.Context, error) {
	deny := func() (context.Context, error) { return nil, cs.NewError(cs.CodeAccessDenied, nil) }
	if r.Host != host {
		return deny()
	}
	values := r.Header.Values(api.ServeLoginHeader)
	if len(values) != 1 {
		return deny()
	}
	principal, err := new(mime.WordDecoder).DecodeHeader(values[0])
	if err != nil || strings.Contains(principal, "=?") || !config.ValidLogin(principal) || !allow[principal] {
		return deny()
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && origins[0] != origin) {
		return deny()
	}
	if len(r.Header.Values(api.CSRFHeader)) > 1 {
		return nil, cs.NewError(cs.CodeCSRFInvalid, nil)
	}
	if r.Method == http.MethodTrace || r.Method == http.MethodConnect {
		return deny()
	}
	ctx := context.WithValue(r.Context(), principalKey{}, principal)
	return viewer.WithPrincipal(ctx, principal), nil
}
