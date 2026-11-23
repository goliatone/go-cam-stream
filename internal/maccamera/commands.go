package maccamera

import (
	"context"
	"io"
)

// These messages and Execute handlers follow the application's typed command
// contract. The companion dispatches directly without importing the web host.
type Configure struct{ Config Config }

func (Configure) Type() string      { return "camera.configure.v1" }
func (m Configure) Validate() error { return m.Config.Validate() }

type ConfigureHandler struct{ Service *Service }

func (h ConfigureHandler) Execute(ctx context.Context, m Configure) error {
	if err := m.Validate(); err != nil {
		return err
	}
	return h.Service.Configure(ctx, m.Config)
}

type Start struct{}

func (Start) Type() string    { return "camera.start.v1" }
func (Start) Validate() error { return nil }

type StartHandler struct {
	Service  *Service
	Out, Err io.Writer
}

func (h StartHandler) Execute(ctx context.Context, _ Start) error {
	return h.Service.Start(ctx, h.Out, h.Err)
}

type Stop struct{}

func (Stop) Type() string    { return "camera.stop.v1" }
func (Stop) Validate() error { return nil }

type StopHandler struct{ Service *Service }

func (h StopHandler) Execute(ctx context.Context, _ Stop) error { return h.Service.Stop(ctx) }

type ExportLegacy struct{}

func (ExportLegacy) Type() string { return "camera.export-legacy.v1" }

type ExportLegacyHandler struct{ Service *Service }

func (h ExportLegacyHandler) Run(ctx context.Context, _ ExportLegacy) (string, error) {
	return h.Service.ExportLegacy(ctx)
}
func (h ExportLegacyHandler) Execute(ctx context.Context, m ExportLegacy) error {
	_, err := h.Run(ctx, m)
	return err
}
