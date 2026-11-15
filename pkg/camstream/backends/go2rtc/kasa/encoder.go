// Package kasa connects the EC70 source description to the stock go2rtc Kasa
// input syntax. Import it only in a composition root using this particular pair.
package kasa

import (
	"context"
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	secrets "github.com/goliatone/go-secrets"
)

// SecretResolver is satisfied by go-secrets providers. Resolve transfers a fresh
// Value to the caller; implementations must honor context cancellation. Hosts
// using several providers can inject a scheme-routing resolver here.
type SecretResolver interface {
	Resolve(context.Context, secrets.Reference) (*secrets.Value, error)
}

type Encoder struct{ resolver SecretResolver }

// NewEncoder performs no I/O. A nil resolver fails closed when Encode is called.
func NewEncoder(resolver SecretResolver) *Encoder { return &Encoder{resolver: resolver} }
func (*Encoder) Kind() camstream.SourceKind       { return ec70.SourceKind }

var _ camstream.SourceEncoder[go2rtc.Input] = (*Encoder)(nil)

func (e *Encoder) Encode(ctx context.Context, source camstream.Source) (go2rtc.Input, error) {
	if err := ctx.Err(); err != nil {
		return go2rtc.Input{}, err
	}
	s, ok := source.(ec70.Source)
	if !ok || s.Kind() != e.Kind() || s.Config().Validate() != nil || e == nil || e.resolver == nil {
		return go2rtc.Input{}, camstream.NewError(camstream.CodeInvalidArgument, nil)
	}
	cfg := s.Config()
	username, err := e.resolve(ctx, cfg.UsernameRef)
	if err != nil {
		return go2rtc.Input{}, err
	}
	defer username.Destroy()
	// The pinned Digest path interpolates the username into a quoted header;
	// reject delimiters it cannot escape. Basic authentication also forbids colon.
	user := string(username.Bytes())
	if strings.ContainsAny(user, ":\"\\") || strings.ContainsFunc(user, unicode.IsControl) {
		return go2rtc.Input{}, camstream.NewError(camstream.CodeInvalidArgument, nil)
	}
	password, err := e.resolve(ctx, cfg.PasswordRef)
	if err != nil {
		return go2rtc.Input{}, err
	}
	defer password.Destroy()
	encoded := &url.URL{Scheme: "kasa", Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path: "/https/stream/mixed", User: url.UserPassword(user, base64.StdEncoding.EncodeToString(password.Bytes()))}
	input := go2rtc.Input{URL: encoded.String()}
	if len(input.URL) > 16384 {
		return go2rtc.Input{}, camstream.NewError(camstream.CodeInvalidArgument, nil)
	}
	return input, nil
}

func (e *Encoder) resolve(ctx context.Context, ref secrets.Reference) (*secrets.Value, error) {
	value, err := e.resolver.Resolve(ctx, ref)
	if err != nil || ctx.Err() != nil || value.Len() == 0 || value.Expired(time.Now()) {
		value.Destroy()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A provider error may contain secret material. Do not retain it as cause.
		// Failure to resolve a binding is not evidence of camera auth rejection.
		return nil, camstream.NewError(camstream.CodeSourceUnavailable, nil)
	}
	return value, nil
}
