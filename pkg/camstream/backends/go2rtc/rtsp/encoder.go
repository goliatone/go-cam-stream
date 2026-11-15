// Package rtsp encodes generic RTSP sources for the pinned go2rtc backend.
// Inputs are private credential-bearing values, never application DTOs.
package rtsp

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	strategy "github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	secrets "github.com/goliatone/go-secrets"
)

// SecretResolver transfers a fresh owned Value to Encode and must honor context.
// go-secrets providers implement this interface without an adapter.
type SecretResolver interface {
	Resolve(context.Context, secrets.Reference) (*secrets.Value, error)
}

type Encoder struct{ resolver SecretResolver }

// NewEncoder performs no I/O. A nil resolver is valid for anonymous sources only.
func NewEncoder(resolver SecretResolver) *Encoder { return &Encoder{resolver: resolver} }
func (*Encoder) Kind() camstream.SourceKind       { return strategy.SourceKind }

var _ camstream.SourceEncoder[go2rtc.Input] = (*Encoder)(nil)

func (e *Encoder) Encode(ctx context.Context, source camstream.Source) (go2rtc.Input, error) {
	if err := ctx.Err(); err != nil {
		return go2rtc.Input{}, err
	}
	s, ok := source.(strategy.Source)
	if e == nil || !ok || s.Kind() != e.Kind() {
		return go2rtc.Input{}, invalid()
	}
	cfg := s.Config()
	if cfg.Validate() != nil || len(cfg.Profiles) != 1 {
		return go2rtc.Input{}, invalid()
	}
	profile := cfg.Profiles[0]
	u := url.URL{Scheme: "rtsp", Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path: profile.Path, RawQuery: profile.Query}
	if cfg.UsernameRef.URI() != "" {
		userinfo, err := e.credentials(ctx, cfg)
		if err != nil {
			return go2rtc.Input{}, err
		}
		u.User = userinfo
	}
	// These are go2rtc source options, not camera URL fragments. Plain RTSP
	// defaults to TCP in the pinned client; operators cannot supply options.
	encoded := u.String() + "#backchannel=0"
	if !profile.Audio {
		encoded += "#media=video"
	}
	if len(encoded) > 16384 {
		return go2rtc.Input{}, invalid()
	}
	if err := ctx.Err(); err != nil {
		return go2rtc.Input{}, err
	}
	return go2rtc.Input{URL: encoded}, nil
}

func (e *Encoder) credentials(ctx context.Context, cfg strategy.Config) (*url.Userinfo, error) {
	if e.resolver == nil {
		return nil, invalid()
	}
	username, err := e.resolve(ctx, cfg.UsernameRef)
	if err != nil {
		return nil, err
	}
	defer username.Destroy()
	user := string(username.Bytes())
	// The pinned Digest implementation quotes but does not escape usernames;
	// Basic uses colon as the separator. URL escaping alone is insufficient.
	if strings.ContainsAny(user, ":\"\\") || strings.ContainsFunc(user, unicode.IsControl) {
		return nil, invalid()
	}
	password, err := e.resolve(ctx, cfg.PasswordRef)
	if err != nil {
		return nil, err
	}
	defer password.Destroy()
	return url.UserPassword(user, string(password.Bytes())), nil
}

func (e *Encoder) resolve(ctx context.Context, ref secrets.Reference) (*secrets.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := e.resolver.Resolve(ctx, ref)
	if err != nil || ctx.Err() != nil || value.Len() == 0 || value.Expired(time.Now()) {
		value.Destroy()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Resolver errors may contain secrets; omit the original error/cause.
		return nil, camstream.NewError(camstream.CodeSourceUnavailable, nil)
	}
	return value, nil
}

func invalid() error { return camstream.NewError(camstream.CodeInvalidArgument, nil) }
