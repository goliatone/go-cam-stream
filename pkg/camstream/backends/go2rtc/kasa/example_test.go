package kasa_test

import (
	"context"
	"fmt"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/kasa"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	secrets "github.com/goliatone/go-secrets"
	"github.com/goliatone/go-secrets/providers/environment"
)

func ExampleNewEncoder() {
	username, err := secrets.ParseReference("env://KASA_USERNAME")
	if err != nil {
		panic(err)
	}
	password, err := secrets.ParseReference("env://KASA_PASSWORD")
	if err != nil {
		panic(err)
	}
	camera := camstream.Camera{ID: "room1", PhysicalID: "ec70-001", Config: ec70.Config{
		Host: "camera.example", UsernameRef: username, PasswordRef: password,
	}}
	spec, err := (ec70.Strategy{}).Resolve(context.Background(), camera, ec70.DefaultStream)
	if err != nil {
		panic(err)
	}
	// In a host, environment.Config{} reads environment variables. This example
	// injects synthetic values and performs no environment or camera access.
	provider := environment.New(environment.Config{Lookup: func(name string) (string, bool) {
		switch name {
		case "KASA_USERNAME":
			return "example@example.test", true
		case "KASA_PASSWORD":
			return "synthetic-password", true
		default:
			return "", false
		}
	}})
	encoder := kasa.NewEncoder(provider)
	// Pass encoder to go2rtc.New(backendConfig, encoder). EnsureStream invokes
	// Encode at the trusted backend boundary; never send its Input to clients.
	_, err = encoder.Encode(context.Background(), spec.Source)
	fmt.Println(encoder.Kind() == spec.Source.Kind(), err == nil)
	// Output: true true
}
