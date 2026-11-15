// These tests verify published dependency seams for T02. They do not construct
// the application host, production auth adapter or camera commands.
package dependencies_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goliatone/go-admin/admin"
	lifecycle "github.com/goliatone/go-admin/pkg/go-lifecycle"
	"github.com/goliatone/go-admin/quickstart"
	"github.com/goliatone/go-auth/middleware/csrf"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-command"
	"github.com/goliatone/go-config/config"
	"github.com/goliatone/go-logger/glog"
	router "github.com/goliatone/go-router"
	secrets "github.com/goliatone/go-secrets"
)

type probeConfig struct{}

func (probeConfig) Validate() error { return nil }

func TestPublishedCompositionSeams(t *testing.T) {
	cfg := quickstart.NewAdminConfig("/admin", "Contract probe", "en")
	if cfg.BasePath != "/admin" {
		t.Fatal("unexpected admin config")
	}
	_ = quickstart.WithMinimalFeatures()
	if config.New(probeConfig{}) == nil {
		t.Fatal("config constructor failed")
	}
	if glog.NewLogger() == nil {
		t.Fatal("logger constructor failed")
	}
	if _, err := lifecycle.NewRunner(lifecycle.NewRegistry()); err != nil {
		t.Fatal(err)
	}
	ref, err := secrets.ParseReference("env://CAMSTREAM_TEST_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme() != "env" || ref.Fingerprint() == "" {
		t.Fatal("secret reference contract")
	}
}

type probeMessage struct{ Value string }

func (probeMessage) Type() string { return "contract.probe.v1" }

type probeResult struct{ Value string }
type probeCommand struct{}

func (probeCommand) Execute(ctx context.Context, msg probeMessage) error {
	command.ResultFromContext[probeResult](ctx).Store(probeResult(msg))
	return nil
}
func TestOwnedNamedResultDispatch(t *testing.T) {
	bus := admin.NewCommandBus(true)
	defer bus.Close()
	set, err := bus.NewRegistrationSet("t02-dependency-probe")
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.RegisterSetCommand(set, probeCommand{}); err != nil {
		t.Fatal(err)
	}
	err = admin.RegisterSetContextMessageResultFactory[probeMessage, probeResult](set, "contract.probe.v1", func(_ context.Context, payload map[string]any, _ []string) (probeMessage, error) {
		value, ok := payload["value"].(string)
		if !ok {
			return probeMessage{}, fmt.Errorf("probe value must be a string")
		}
		return probeMessage{Value: value}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := set.Commit()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := handle.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	out, err := bus.DispatchByNameWithOutcome(context.Background(), "contract.probe.v1", map[string]any{"value": "typed-result"}, nil, command.DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := out.Result.(probeResult)
	if !ok || result.Value != "typed-result" {
		t.Fatalf("missing typed result: %#v", out)
	}
}

// This synthetic authenticator only proves the public interface and middleware
// composition. Real Serve trust/origin/allowlist enforcement belongs to T07.
type probeAuthenticator struct{}

func (probeAuthenticator) Wrap(c router.Context) error {
	if c.Header("X-Probe-Principal") == "" {
		return c.SendStatus(http.StatusForbidden)
	}
	return c.Next()
}
func (probeAuthenticator) WrapHandler(next router.HandlerFunc) router.HandlerFunc {
	return func(c router.Context) error {
		if c.Header("X-Probe-Principal") == "" {
			return c.SendStatus(http.StatusForbidden)
		}
		return next(c)
	}
}

type probeAuthorizer struct{}

func (probeAuthorizer) Can(context.Context, string, string) bool { return false }

var _ admin.Authenticator = probeAuthenticator{}
var _ admin.HandlerAuthenticator = probeAuthenticator{}
var _ admin.Authorizer = probeAuthorizer{}

func TestPackageCSRFPrincipalBoundBootstrapAndWrite(t *testing.T) {
	server := router.NewHTTPServer()
	app := server.Router()
	app.Use(probeAuthenticator{}.WrapHandler)
	app.Use(csrf.New(csrf.Config{
		TokenLookup: "header:" + api.CSRFHeader,
		SafeMethods: []string{http.MethodGet, http.MethodHead, http.MethodOptions},
		Expiration:  30 * time.Minute, DisableTemplateHelpers: true,
		SessionKeyResolver: func(c router.Context) (string, bool) {
			principal := c.Header("X-Probe-Principal")
			if principal == "" {
				return "", false
			}
			sum := sha256.Sum256([]byte("probe-installation\x00" + principal))
			return hex.EncodeToString(sum[:]), true
		},
		ErrorHandler: func(c router.Context, _ error) error { return c.SendStatus(http.StatusForbidden) },
	}))
	csrf.RegisterRoutes(app, csrf.RouteConfig{Path: api.CSRFPath})
	app.Post("/write", func(c router.Context) error { return c.SendStatus(http.StatusNoContent) })
	handler := server.WrappedRouter()
	request := func(method, path, principal, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(context.Background(), method, path, nil)
		if principal != "" {
			req.Header.Set("X-Probe-Principal", principal)
		}
		if token != "" {
			req.Header.Set(api.CSRFHeader, token)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	denied := request(http.MethodGet, api.CSRFPath, "", "")
	if denied.Code != 403 {
		t.Fatal("bootstrap bypasses auth")
	}
	response := request(http.MethodGet, api.CSRFPath, "owner@example.test", "")
	if response.Code != 200 || response.Header().Get("Cache-Control") == "" {
		t.Fatalf("bootstrap failed: %d %s", response.Code, response.Body.String())
	}
	var token api.CSRFResponse
	if err := json.Unmarshal(response.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	if token.Token == "" || token.HeaderName != api.CSRFHeader || token.FieldName != "_token" {
		t.Fatal("bootstrap contract drift")
	}
	for _, tc := range []struct {
		name, principal, token string
		want                   int
	}{
		{"valid", "owner@example.test", token.Token, 204},
		{"missing", "owner@example.test", "", 403},
		{"invalid", "owner@example.test", "invalid", 403},
		{"other principal", "other@example.test", token.Token, 403},
		{"unauthenticated", "", token.Token, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(http.MethodPost, "/write", tc.principal, tc.token).Code; got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Fatal("selected package flow unexpectedly requires a cookie")
	}
}
