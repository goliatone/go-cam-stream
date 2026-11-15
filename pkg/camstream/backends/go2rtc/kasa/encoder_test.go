package kasa_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/kasa"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	secrets "github.com/goliatone/go-secrets"
)

type resolverFunc func(context.Context, secrets.Reference) (*secrets.Value, error)

func (f resolverFunc) Resolve(ctx context.Context, ref secrets.Reference) (*secrets.Value, error) {
	return f(ctx, ref)
}
func ref(t *testing.T, uri string) secrets.Reference {
	t.Helper()
	r, err := secrets.ParseReference(uri)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func spec(t *testing.T, host string, port uint16) camstream.StreamSpec {
	t.Helper()
	s, err := (ec70.Strategy{}).Resolve(context.Background(), camstream.Camera{ID: "room", PhysicalID: "device", Config: ec70.Config{
		Host: host, Port: port, UsernameRef: ref(t, "env://USER"), PasswordRef: ref(t, "env://PASS"),
	}}, ec70.DefaultStream)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func values(username, password string, owned *[]*secrets.Value) resolverFunc {
	return func(_ context.Context, ref secrets.Reference) (*secrets.Value, error) {
		data := password
		if ref.URI() == "env://USER" {
			data = username
		}
		v, err := secrets.NewValue([]byte(data), secrets.ValueOptions{})
		if owned != nil {
			*owned = append(*owned, v)
		}
		return v, err
	}
}

func TestEncodingAndValueOwnership(t *testing.T) {
	// Includes characters which need URL escaping and standard-base64 +, /, =.
	username, password := "alex+tag%#@example.test", "\xfb\xffpassword:@#%/日本語"
	var owned []*secrets.Value
	encoder := kasa.NewEncoder(values(username, password, &owned))
	input, err := encoder.Encode(context.Background(), spec(t, "2001:db8::1", 0).Source)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(input.URL)
	if err != nil {
		t.Fatal("invalid encoded URL")
	}
	pass, ok := u.User.Password()
	if !ok || u.Scheme != "kasa" || u.Host != "[2001:db8::1]:19443" || u.Path != "/https/stream/mixed" || u.RawQuery != "" || u.Fragment != "" || u.User.Username() != username || pass != base64.StdEncoding.EncodeToString([]byte(password)) {
		t.Fatal("incorrect Kasa encoding")
	}
	// The pinned adapter changes kasa -> httpx -> https before net/http adds Basic auth.
	u.Scheme = "https"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatal("invalid HTTP request")
	}
	req.SetBasicAuth(req.URL.User.Username(), pass)
	gotUser, gotPass, ok := req.BasicAuth()
	if !ok || gotUser != username || gotPass != pass {
		t.Fatal("HTTP auth mismatch")
	}
	if len(owned) != 2 || owned[0].Len() != 0 || owned[1].Len() != 0 {
		t.Fatal("secret Values not destroyed")
	}
}

type counterfeit struct{}

func (counterfeit) Kind() camstream.SourceKind { return ec70.SourceKind }

func TestRejectsTypesAndMissingResolver(t *testing.T) {
	called := false
	e := kasa.NewEncoder(resolverFunc(func(context.Context, secrets.Reference) (*secrets.Value, error) { called = true; return nil, nil }))
	for _, source := range []camstream.Source{nil, counterfeit{}, ec70.Source{}, (*ec70.Source)(nil)} {
		_, err := e.Encode(context.Background(), source)
		if camstream.DescribeError(err).Code != camstream.CodeInvalidArgument || called {
			t.Fatal("invalid source reached resolver")
		}
	}
	for _, encoder := range []*kasa.Encoder{nil, kasa.NewEncoder(nil)} {
		_, err := encoder.Encode(context.Background(), spec(t, "camera", 0).Source)
		if camstream.DescribeError(err).Code != camstream.CodeInvalidArgument {
			t.Fatal("nil resolver accepted")
		}
	}
}

func TestResolutionFailuresAreSafe(t *testing.T) {
	source := spec(t, "camera", 0).Source
	for _, mode := range []string{"nil", "expired", "error", "value-and-error"} {
		var owned *secrets.Value
		e := kasa.NewEncoder(resolverFunc(func(context.Context, secrets.Reference) (*secrets.Value, error) {
			if mode == "nil" {
				return nil, nil
			}
			v, err := secrets.NewValue([]byte("PRIVATE_PASSWORD"), secrets.ValueOptions{ExpiresAt: time.Now().Add(-time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			owned = v
			if mode == "expired" {
				return v, nil
			}
			if mode == "error" {
				v.Destroy()
				return nil, errors.New("PRIVATE_PASSWORD")
			}
			return v, errors.New("PRIVATE_PASSWORD")
		}))
		_, err := e.Encode(context.Background(), source)
		if camstream.DescribeError(err).Code != camstream.CodeSourceUnavailable || strings.Contains(fmt.Sprintf("%+v", err), "PRIVATE_PASSWORD") || errors.Unwrap(err) != nil {
			t.Fatal("unsafe resolution failure")
		}
		if owned != nil && owned.Len() != 0 {
			t.Fatal("failed resolution retained Value")
		}
	}
	for _, user := range []string{"a:b", "a\"b", "a\\b", "a\nb"} {
		var owned []*secrets.Value
		_, err := kasa.NewEncoder(values(user, "password", &owned)).Encode(context.Background(), source)
		if camstream.DescribeError(err).Code != camstream.CodeInvalidArgument || len(owned) != 1 || owned[0].Len() != 0 {
			t.Fatal("unsafe Digest username accepted")
		}
	}
}

func TestCancellationAndPasswordFailureCleanup(t *testing.T) {
	source := spec(t, "camera", 0).Source
	ctx, cancel := context.WithCancel(context.Background())
	var owned *secrets.Value
	calls := 0
	e := kasa.NewEncoder(resolverFunc(func(_ context.Context, _ secrets.Reference) (*secrets.Value, error) {
		calls++
		if calls == 2 {
			cancel()
			return nil, ctx.Err()
		}
		v, err := secrets.NewValue([]byte("username"), secrets.ValueOptions{})
		owned = v
		return v, err
	}))
	_, err := e.Encode(ctx, source)
	if !errors.Is(err, context.Canceled) || owned.Len() != 0 {
		t.Fatal("cancellation did not release username")
	}
	_, err = e.Encode(ctx, source)
	if !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatal("canceled call resolved secrets")
	}
}

func TestPasswordFailureAndOversizedInput(t *testing.T) {
	for _, password := range []string{"", strings.Repeat("x", 16384)} {
		var owned []*secrets.Value
		_, err := kasa.NewEncoder(values("alex@example.test", password, &owned)).Encode(context.Background(), spec(t, "camera", 0).Source)
		want := camstream.CodeInvalidArgument
		if password == "" {
			want = camstream.CodeSourceUnavailable
		}
		if camstream.DescribeError(err).Code != want || len(owned) != 2 {
			t.Fatal("invalid password did not fail closed")
		}
		for _, value := range owned {
			if value.Len() != 0 {
				t.Fatal("failed encoding retained credentials")
			}
		}
	}
}

func TestBackendReconciliationAndCredentialRotation(t *testing.T) {
	password := "first-password"
	encoder := kasa.NewEncoder(resolverFunc(func(ctx context.Context, ref secrets.Reference) (*secrets.Value, error) {
		return values("alex@example.test", password, nil)(ctx, ref)
	}))
	b, err := go2rtc.New(go2rtc.Config{Executable: "/unused/go2rtc", RuntimeDir: filepath.Join(t.TempDir(), "runtime"),
		PrivatePorts: go2rtc.PortRange{First: 35000, Last: 35099}, MediaPorts: go2rtc.PortRange{First: 35100, Last: 35199},
		MediaAddress: "127.0.0.1", CandidateAddress: "127.0.0.1", VerifyBoundary: go2rtc.ContainerBoundary}, encoder)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, alias := spec(t, "CAMERA.example.", 0), spec(t, "camera.example", ec70.DefaultPort)
	for _, s := range []camstream.StreamSpec{first, alias} {
		state, ensureErr := b.EnsureStream(ctx, s)
		if ensureErr != nil || state.Upstream != camstream.PresenceAbsent {
			t.Fatal(state, ensureErr)
		}
	}
	password = "rotated-password"
	_, err = b.EnsureStream(ctx, alias)
	if camstream.DescribeError(err).Code != camstream.CodeConflict {
		t.Fatal("credential rotation silently replaced registered input")
	}
	if err = b.RemoveStream(ctx, first.Key); err != nil {
		t.Fatal(err)
	}
	if _, err = b.StreamStatus(ctx, alias.Key); camstream.DescribeError(err).Code != camstream.CodeNotFound {
		t.Fatal("aliases did not share definition")
	}
	if _, err = b.EnsureStream(ctx, alias); err != nil {
		t.Fatal("re-registration after confirmed removal failed", err)
	}
}
