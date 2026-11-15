//go:build !linux

package app

import (
	"context"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func verifyCleanContainer(context.Context) error { return cs.NewError(cs.CodeUnsupported, nil) }

func verifyCleanDeployment(ctx context.Context) error { return verifyCleanContainer(ctx) }
