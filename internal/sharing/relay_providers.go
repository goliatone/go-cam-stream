package sharing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

const cloudflareRelayBase = "https://rtc.live.cloudflare.com/v1/turn/keys/"

type CoturnRelay struct {
	urls   []string
	secret Secret
	clock  func() time.Time
}

func NewCoturnRelay(urls []string, secret Secret) (*CoturnRelay, error) {
	cfg := config.Sharing{Relay: config.SharingRelay{Mode: config.SharingRelayCoturn, CredentialRef: "private", URLs: urls}}
	if cfg.Validate() != nil || secret == "" {
		return nil, fault(api.ShareRelayUnavailable)
	}
	return &CoturnRelay{urls: append([]string(nil), urls...), secret: secret, clock: time.Now}, nil
}

func (r *CoturnRelay) Issue(ctx context.Context, grant RelayGrant) (RelayCredentials, error) {
	now, expires := r.clock(), grant.NotAfter.Truncate(time.Second)
	if ctx.Err() != nil || !grant.Attempt.valid() || !expires.After(now) {
		return RelayCredentials{}, fault(api.ShareRelayUnavailable)
	}
	makeICE := func(endpoint string) []api.ShareICECredentials {
		username := strconv.FormatInt(expires.Unix(), 10) + ":" + grant.Attempt.AttemptID + "-" + endpoint
		// Coturn's shared-secret authentication requires HMAC-SHA1.
		mac := hmac.New(sha1.New, []byte(r.secret))
		_, _ = mac.Write([]byte(username))
		return []api.ShareICECredentials{{URLs: r.urls, Username: username, Credential: base64.StdEncoding.EncodeToString(mac.Sum(nil)), ExpiresAt: expires}}
	}
	return NewRelayCredentials(grant, now, makeICE("host"), makeICE("guest"))
}

type CloudflareRelay struct {
	key    string
	token  Secret
	client *http.Client
	base   string
	clock  func() time.Time
}

func NewCloudflareRelay(key string, token Secret, client *http.Client) (*CloudflareRelay, error) {
	if !config.ValidID(key) || token == "" || strings.ContainsAny(string(token), "\r\n") {
		return nil, fault(api.ShareRelayUnavailable)
	}
	if client == nil {
		client = http.DefaultClient
	}
	privateClient := *client
	privateClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CloudflareRelay{key: key, token: token, client: &privateClient, base: cloudflareRelayBase, clock: time.Now}, nil
}

func (r *CloudflareRelay) Issue(ctx context.Context, grant RelayGrant) (RelayCredentials, error) {
	if !grant.Attempt.valid() {
		return RelayCredentials{}, fault(api.ShareRelayUnavailable)
	}
	host, err := r.generate(ctx, grant)
	if err != nil {
		return RelayCredentials{}, err
	}
	guest, err := r.generate(ctx, grant)
	if err != nil {
		return RelayCredentials{}, err
	}
	return NewRelayCredentials(grant, r.clock(), host, guest)
}

func (r *CloudflareRelay) generate(ctx context.Context, grant RelayGrant) ([]api.ShareICECredentials, error) {
	start := r.clock()
	ttl := int64((grant.NotAfter.Sub(start) - 4*time.Second) / time.Second)
	if ttl < 1 {
		return nil, fault(api.ShareRelayUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body := []byte("{\"ttl\":" + strconv.FormatInt(ttl, 10) + "}")
	endpoint := r.base + r.key + "/credentials/generate-ice-servers"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fault(api.ShareRelayUnavailable)
	}
	req.Header.Set("Authorization", "Bearer "+string(r.token))
	req.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(req)
	if err != nil {
		return nil, fault(api.ShareRelayUnavailable)
	}
	defer response.Body.Close() //nolint:errcheck // The response is bounded and read only.
	if response.StatusCode != http.StatusCreated {
		return nil, fault(api.ShareRelayUnavailable)
	}
	var reply struct {
		ICEServers []api.ShareICECredentials `json:"iceServers"`
	}
	dec := json.NewDecoder(io.LimitReader(response.Body, 16*1024))
	if dec.Decode(&reply) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, fault(api.ShareRelayUnavailable)
	}
	var results []api.ShareICECredentials
	for _, server := range reply.ICEServers {
		if server.Username == "" && server.Credential == "" {
			continue
		} // The TURN port excludes unauthenticated STUN entries.
		cfg := config.Sharing{Relay: config.SharingRelay{Mode: config.SharingRelayCoturn, CredentialRef: "private", URLs: server.URLs}}
		if cfg.Validate() != nil {
			return nil, fault(api.ShareRelayUnavailable)
		}
		server.ExpiresAt = start.Add(time.Duration(ttl) * time.Second)
		results = append(results, server)
	}
	if len(results) == 0 || len(results) > 4 {
		return nil, fault(api.ShareRelayUnavailable)
	}
	return results, nil
}
