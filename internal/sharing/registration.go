package sharing

import (
	"context"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-command"
)

type commandInput struct{}

type sharingMessage interface {
	command.Message
	Validate() error
}

type commandHandler[M sharingMessage, R any] struct {
	run func(context.Context, M) (R, error)
}

func (h commandHandler[M, R]) Execute(ctx context.Context, msg M) error {
	value, err := h.run(ctx, msg)
	if result := command.ResultFromContext[R](ctx); result != nil {
		if err != nil {
			result.StoreError(err)
		} else {
			result.Store(value)
		}
	}
	return err
}

func registerCommand[M sharingMessage, R any](set *admin.CommandRegistrationSet, msg M, run func(context.Context, M) (R, error)) error {
	if err := admin.RegisterSetCommand(set, commandHandler[M, R]{run: run}); err != nil {
		return err
	}
	return admin.RegisterSetContextMessageResultFactory[M, R](set, msg.Type(), func(ctx context.Context, payload map[string]any, ids []string) (M, error) {
		input, ok := ctx.Value(commandInput{}).(M)
		if !ok || len(payload) != 0 || len(ids) != 0 {
			var zero M
			return zero, fault(api.ShareInvalidRequest)
		}
		return input, input.Validate()
	})
}

// Dispatch keeps secrets and request authority out of the named bus payload.
func Dispatch[R any](ctx context.Context, bus *admin.CommandBus, msg sharingMessage) (R, error) {
	var empty R
	if bus == nil {
		return empty, fault(api.ShareInvalidRequest)
	}
	outcome, err := bus.DispatchByNameWithOutcome(context.WithValue(ctx, commandInput{}, msg), msg.Type(), nil, nil, command.DispatchOptions{Mode: command.ExecutionModeInline})
	if err != nil {
		return empty, err
	}
	if err := admin.CommandResultFailure(outcome.Result); err != nil {
		return empty, err
	}
	result, ok := outcome.Result.(R)
	if !ok {
		return empty, fault(api.ShareInvalidRequest)
	}
	return result, nil
}

// Register publishes sharing commands owned by the host's local bus.
func Register(bus *admin.CommandBus, service Workflows) (admin.CommandRegistrationHandle, error) {
	if bus == nil || service == nil {
		return nil, fault(api.ShareInvalidRequest)
	}
	set, err := bus.NewRegistrationSet("camstream.sharing")
	if err != nil {
		return nil, err
	}
	if err := registerCommand(set, CreateShare{}, service.Create); err != nil {
		return nil, err
	}
	if err := registerCommand(set, CopyShareLink{}, service.CopyLink); err != nil {
		return nil, err
	}
	if err := registerCommand(set, RevokeShare{}, service.Revoke); err != nil {
		return nil, err
	}
	if err := registerCommand(set, RedeemShare{}, service.Redeem); err != nil {
		return nil, err
	}
	if err := registerCommand(set, AdmitShareViewer{}, service.AdmitViewer); err != nil {
		return nil, err
	}
	if err := registerCommand(set, OpenShareViewer{}, service.OpenViewer); err != nil {
		return nil, err
	}
	if err := registerCommand(set, CloseShareViewer{}, func(ctx context.Context, msg CloseShareViewer) (struct{}, error) {
		return struct{}{}, service.CloseViewer(ctx, msg)
	}); err != nil {
		return nil, err
	}
	return set.Commit()
}
