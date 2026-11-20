package conversationhost

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

const routeProofHeader = "X-Camstream-Route-Proof"

type routeVerification struct {
	origin, path, key string
	client            http.Client
}

func newRouteVerification(origin string) *routeVerification {
	return &routeVerification{
		origin: origin,
		path:   "/conversation-api/v1/route-check/" + rand.Text(),
		key:    rand.Text(),
		client: http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (v *routeVerification) wrap(guest http.Handler) http.Handler {
	host := strings.TrimPrefix(v.origin, "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.Host == host && r.URL.Path == v.path && r.URL.RawPath == "" {
			if nonce := r.URL.Query().Get("check"); nonce != "" && len(nonce) <= 64 {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set(routeProofHeader, v.proof(nonce))
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		guest.ServeHTTP(w, r)
	})
}

func (v *routeVerification) proof(nonce string) string {
	mac := hmac.New(sha256.New, []byte(v.key))
	_, _ = mac.Write([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (v *routeVerification) reachable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// A distinct nonce rejects previously cached responses. A different listener
	// can see the nonce but cannot produce the expected proof.
	nonce := rand.Text()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.origin+v.path+"?check="+nonce, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Cache-Control", "no-cache")
	res, err := v.client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close() //nolint:errcheck // No response body is consumed.
	return res.StatusCode == http.StatusNoContent && hmac.Equal([]byte(res.Header.Get(routeProofHeader)), []byte(v.proof(nonce)))
}
