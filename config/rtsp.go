package config

import (
	"bytes"
	"encoding/json"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	secrets "github.com/goliatone/go-secrets"
	"github.com/goliatone/go-secrets/providers/environment"
)

// DefaultProviders returns the startup providers shared by the server and CLI.
// Explicit composition through Prepare or app.New retains caller-owned providers.
func DefaultProviders() []Provider { return []Provider{EC70Provider(), RTSPProvider()} }

// RTSPProvider decodes explicit profiles without resolving credentials or dialing.
// The host enables environment references only; the reusable strategy is agnostic.
func RTSPProvider() Provider { return Provider{Strategy: rtsp.Strategy{}, Decode: decodeRTSP} }

// rtspOption distinguishes omission from explicit zero/empty values. Null is not
// a valid port or credential reference and must never silently enable anonymous use.
type rtspOption[T any] struct {
	value   T
	present bool
}

func (o *rtspOption[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return invalid()
	}
	o.present = true
	return json.Unmarshal(data, &o.value)
}

func decodeRTSP(input map[string]any) (Decoded, error) {
	var wire struct {
		Host        string             `json:"host"`
		Port        rtspOption[uint16] `json:"port"`
		UsernameRef rtspOption[string] `json:"username_ref"`
		PasswordRef rtspOption[string] `json:"password_ref"`
		Profiles    []struct {
			ID    cs.StreamID `json:"id"`
			Name  string      `json:"name"`
			Path  string      `json:"path"`
			Query string      `json:"query"`
			Audio bool        `json:"audio"`
		} `json:"profiles"`
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Decoded{}, invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&wire); err != nil || (wire.Port.present && wire.Port.value == 0) {
		return Decoded{}, invalid()
	}
	config := rtsp.Config{Host: wire.Host, Port: wire.Port.value}
	if wire.UsernameRef.present != wire.PasswordRef.present {
		return Decoded{}, invalid()
	}
	if wire.UsernameRef.present {
		config.UsernameRef, err = rtspReference(wire.UsernameRef.value)
		if err != nil {
			return Decoded{}, err
		}
		config.PasswordRef, err = rtspReference(wire.PasswordRef.value)
		if err != nil {
			return Decoded{}, err
		}
	}
	for _, profile := range wire.Profiles {
		config.Profiles = append(config.Profiles, rtsp.Profile{ID: profile.ID, Name: profile.Name, Path: profile.Path, Query: profile.Query, Audio: profile.Audio})
	}
	config, err = config.Normalize()
	if err != nil {
		return Decoded{}, invalid()
	}
	endpoint, err := config.Endpoint()
	if err != nil {
		return Decoded{}, invalid()
	}
	return Decoded{Config: config, Endpoint: endpoint}, nil
}

func rtspReference(raw string) (secrets.Reference, error) {
	ref, err := secrets.ParseReference(raw)
	if err != nil || environment.New(environment.Config{}).ValidateReference(ref) != nil {
		return secrets.Reference{}, invalid()
	}
	return ref, nil
}
