//go:build camstream_dev && !darwin

package app

import (
	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
)

func newDevelopmentController(cfg config.Config, logger admin.Logger, _ DevelopmentOptions) (*Controller, error) {
	return NewDefault(cfg, logger)
}
