package rtsp_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	encoderpkg "github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/rtsp"
	strategy "github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
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
	s, err := (strategy.Strategy{}).Resolve(context.Background(), camstream.Camera{ID: "room", PhysicalID: "device", Config: strategy.Config{
		Host: host, Port: port, Profiles: []strategy.Profile{{ID: "main", Path: "/cam//../日本語 space%", Query: "x=1&x=2&q=%23%2f+", Audio: true}}, UsernameRef: ref(t, "env://USER"), PasswordRef: ref(t, "env://PASS"),
	}}, "main")
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
	username, password := "alex+tag%#@example.test", "\xfb\xffpassword:@#%/日本語"
	var owned []*secrets.Value
	encoder := encoderpkg.NewEncoder(values(username, password, &owned))
	input, err := encoder.Encode(context.Background(), spec(t, "2001:db8::1", 0).Source)
	if err != nil {
		t.Fatal(err)
	}
	raw, options, _ := strings.Cut(input.URL, "#")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal("invalid encoded URL")
	}
	pass, ok := u.User.Password()
	if !ok || u.Scheme != "rtsp" || u.Host != "[2001:db8::1]:554" || u.Path != "/cam//../日本語 space%" || u.RawQuery != "x=1&x=2&q=%23%2f+" || u.User.Username() != username || pass != password || options != "backchannel=0" {
		t.Fatal("incorrect RTSP encoding")
	}
	if len(owned) != 2 || owned[0].Len() != 0 || owned[1].Len() != 0 {
		t.Fatal("secret Values not destroyed")
	}
}

func TestAnonymousVideoOnly(t *testing.T) {
	cfg := strategy.Config{Host: "camera.invalid", Profiles: []strategy.Profile{{ID: "main", Path: "/camera"}}}
	s, err := (strategy.Strategy{}).Resolve(context.Background(), camstream.Camera{ID: "pi", PhysicalID: "pi-1", Config: cfg}, "main")
	if err != nil {
		t.Fatal(err)
	}
	panicResolver := resolverFunc(func(context.Context, secrets.Reference) (*secrets.Value, error) {
		t.Fatal("anonymous source resolved credentials")
		return nil, nil
	})
	for _, e := range []*encoderpkg.Encoder{encoderpkg.NewEncoder(nil), encoderpkg.NewEncoder(panicResolver)} {
		input, encodeErr := e.Encode(context.Background(), s.Source)
		if encodeErr != nil || input.URL != "rtsp://camera.invalid:554/camera#backchannel=0#media=video" {
			t.Fatal("anonymous video encoding failed", encodeErr)
		}
	}
}

type counterfeit struct{}

func (counterfeit) Kind() camstream.SourceKind { return strategy.SourceKind }

func TestRejectsTypesAndMissingResolver(t *testing.T) {
	called := false
	e := encoderpkg.NewEncoder(resolverFunc(func(context.Context, secrets.Reference) (*secrets.Value, error) { called = true; return nil, nil }))
	for _, source := range []camstream.Source{nil, counterfeit{}, strategy.Source{}, (*strategy.Source)(nil)} {
		_, err := e.Encode(context.Background(), source)
		if camstream.DescribeError(err).Code != camstream.CodeInvalidArgument || called {
			t.Fatal("invalid source reached resolver")
		}
	}
	for _, encoder := range []*encoderpkg.Encoder{nil, encoderpkg.NewEncoder(nil)} {
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
		e := encoderpkg.NewEncoder(resolverFunc(func(context.Context, secrets.Reference) (*secrets.Value, error) {
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
		_, err := encoderpkg.NewEncoder(values(user, "password", &owned)).Encode(context.Background(), source)
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
	e := encoderpkg.NewEncoder(resolverFunc(func(_ context.Context, _ secrets.Reference) (*secrets.Value, error) {
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
	for _, password := range []string{"", strings.Repeat("x", 16384), strings.Repeat("@", 6000)} {
		var owned []*secrets.Value
		_, err := encoderpkg.NewEncoder(values("alex@example.test", password, &owned)).Encode(context.Background(), spec(t, "camera", 0).Source)
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

func TestFinalURLBoundaryAndCanceledValue(t *testing.T) {
	source := spec(t, "camera", 0).Source
	short, err := encoderpkg.NewEncoder(values("user", "x", nil)).Encode(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	for _, length := range []int{16384, 16385} {
		var owned []*secrets.Value
		password := strings.Repeat("x", length-len(short.URL)+1)
		input, encodeErr := encoderpkg.NewEncoder(values("user", password, &owned)).Encode(context.Background(), source)
		if length == 16384 {
			if encodeErr != nil || len(input.URL) != length {
				t.Fatal("exact input boundary rejected")
			}
		} else if camstream.DescribeError(encodeErr).Code != camstream.CodeInvalidArgument || input.URL != "" {
			t.Fatal("oversized input did not fail closed")
		}
		for _, value := range owned {
			if value.Len() != 0 {
				t.Fatal("boundary test retained secret")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var owned *secrets.Value
	e := encoderpkg.NewEncoder(resolverFunc(func(context.Context, secrets.Reference) (*secrets.Value, error) {
		value, valueErr := secrets.NewValue([]byte("private-user"), secrets.ValueOptions{})
		owned = value
		cancel()
		return value, valueErr
	}))
	input, err := e.Encode(ctx, source)
	if !errors.Is(err, context.Canceled) || input.URL != "" || owned.Len() != 0 {
		t.Fatal("canceled resolution retained secret/input")
	}
}

func TestBackendReconciliationAndCredentialRotation(t *testing.T) {
	password := "first-password"
	encoder := encoderpkg.NewEncoder(resolverFunc(func(ctx context.Context, ref secrets.Reference) (*secrets.Value, error) {
		return values("alex@example.test", password, nil)(ctx, ref)
	}))
	b, err := go2rtc.New(go2rtc.Config{Executable: "/unused/go2rtc", RuntimeDir: filepath.Join(t.TempDir(), "runtime"),
		PrivatePorts: go2rtc.PortRange{First: 35000, Last: 35099}, MediaPorts: go2rtc.PortRange{First: 35100, Last: 35199},
		MediaAddress: "127.0.0.1", CandidateAddress: "127.0.0.1", VerifyBoundary: go2rtc.ContainerBoundary}, encoder)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, alias := spec(t, "CAMERA.example.", 0), spec(t, "camera.example", strategy.DefaultPort)
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
