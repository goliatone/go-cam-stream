package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

const usage = `camstream serve --config FILE
camstream config validate --config FILE
camstream managed-state prepare-rollback --config FILE
camstream list --origin HTTPS_ORIGIN
camstream status --origin HTTPS_ORIGIN --camera ID [--stream ID]
camstream diagnose --origin HTTPS_ORIGIN --camera ID [--stream ID]
camstream reconnect --origin HTTPS_ORIGIN --camera ID --stream ID
`

type ServeFunc func(context.Context, string) error
type PrepareRollbackFunc func(context.Context, string) (int, error)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, serve ServeFunc, rollback ...PrepareRollbackFunc) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		if _, err := io.WriteString(stdout, usage); err != nil {
			return 1
		}
		return 0
	}
	var prepare PrepareRollbackFunc
	if len(rollback) != 0 {
		prepare = rollback[0]
	}
	value, err := execute(ctx, args, serve, prepare)
	if value != nil {
		if encodeErr := json.NewEncoder(stdout).Encode(value); encodeErr != nil {
			return 1
		}
	}
	if err != nil {
		_ = json.NewEncoder(stderr).Encode(api.PublicError(err)) //nolint:errcheck,errchkjson // Already returning failure; no safe output destination remains.
		return 1
	}
	return 0
}

type options struct{ action, path, origin, camera, stream string }

func parse(args []string) (options, error) {
	o := options{action: args[0]}
	args = args[1:]
	if o.action == "config" || o.action == "managed-state" {
		expected := "validate"
		if o.action == "managed-state" {
			expected = "prepare-rollback"
		}
		if len(args) == 0 || args[0] != expected {
			return o, invalid()
		}
		o.action = expected
		args = args[1:]
	}
	flags := flag.NewFlagSet(o.action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	switch o.action {
	case "serve", "validate", "prepare-rollback":
		flags.StringVar(&o.path, "config", "", "startup configuration")
	case "list", "status", "diagnose", "reconnect":
		flags.StringVar(&o.origin, "origin", "", "Serve HTTPS origin")
		if o.action != "list" {
			flags.StringVar(&o.camera, "camera", "", "camera ID")
			flags.StringVar(&o.stream, "stream", "", "stream ID")
		}
	default:
		return o, invalid()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, err
		}
		return o, invalid()
	}
	if flags.NArg() != 0 {
		return o, invalid()
	}
	return o, nil
}
func execute(ctx context.Context, args []string, serve ServeFunc, prepare PrepareRollbackFunc) (any, error) {
	o, err := parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return map[string]string{"usage": usage}, nil
	}
	if err != nil {
		return nil, err
	}
	if o.action == "serve" {
		if o.path == "" || serve == nil {
			return nil, invalid()
		}
		return nil, serve(ctx, o.path)
	}
	if o.action == "validate" {
		return validate(ctx, o.path)
	}
	if o.action == "prepare-rollback" {
		if o.path == "" || prepare == nil {
			return nil, invalid()
		}
		removed, err := prepare(ctx, o.path)
		if err != nil {
			return nil, err
		}
		return map[string]int{"credential_receipts_removed": removed}, nil
	}
	if o.action != "list" && (!config.ValidID(o.camera) || (o.stream != "" && !config.ValidID(o.stream))) {
		return nil, invalid()
	}
	if o.action == "reconnect" && o.stream == "" {
		return nil, invalid()
	}
	client, err := NewClient(o.origin, nil)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return remote(ctx, client, o)
}
func validate(ctx context.Context, path string) (any, error) {
	if path == "" {
		return nil, invalid()
	}
	raw, err := config.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	_, err = config.Prepare(ctx, raw, config.DefaultProviders()...)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"valid": true}, nil
}
func remote(ctx context.Context, client *Client, o options) (any, error) {
	switch o.action {
	case "list":
		out, e := client.List(ctx)
		if e != nil {
			return nil, e
		}
		return out, nil
	case "status":
		out, e := client.Status(ctx, o.camera, o.stream)
		if e != nil {
			return nil, e
		}
		return out, nil
	case "diagnose":
		out, e := client.Diagnose(ctx, o.camera, o.stream)
		if e != nil && out.Streams == nil {
			return nil, e
		}
		return out, e
	default:
		out, e := client.Reconnect(ctx, o.camera, o.stream)
		if e != nil {
			return nil, e
		}
		return out, nil
	}
}
func invalid() error { return cs.NewError(cs.CodeInvalidArgument, nil) }
