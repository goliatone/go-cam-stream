package sharing

import (
	"context"
	"sync"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-command"
	"github.com/goliatone/go-command/dispatcher"
	"github.com/goliatone/go-command/runner"
)

type sharingMessage interface {
	command.Message
	Validate() error
}

type commandHandler[M sharingMessage, R any] struct {
	run func(context.Context, M) (R, error)
}

func (h commandHandler[M, R]) Execute(ctx context.Context, msg M) error {
	result := command.ResultFromContext[R](ctx)
	if result == nil {
		return fault(api.ShareInvalidRequest)
	}
	value, err := h.run(ctx, msg)
	if err != nil {
		result.StoreError(err)
	} else {
		result.Store(value)
	}
	return err
}

// Commands owns an isolated go-command runtime for one native sharing service.
type Commands struct {
	runtime *dispatcher.Runtime
	subs    []dispatcher.Subscription
	close   sync.Once
}

func registerCommand[M sharingMessage, R any](commands *Commands, run func(context.Context, M) (R, error)) {
	sub := dispatcher.SubscribeCommandTo(commands.runtime, commandHandler[M, R]{run: run}, runner.WithMaxRetries(0))
	commands.subs = append(commands.subs, sub)
}

// Dispatch uses the typed local message directly. No transport registers
// these commands globally or converts their private fields to a generic map.
func Dispatch[R any](ctx context.Context, commands *Commands, msg sharingMessage) (R, error) {
	var empty R
	if commands == nil {
		return empty, fault(api.ShareInvalidRequest)
	}
	result := command.NewResult[R]()
	err := dispatcher.DispatchTo(command.ContextWithResult(ctx, result), commands.runtime, msg)
	if failure := result.Error(); failure != nil {
		return empty, failure
	}
	if err != nil {
		return empty, err
	}
	value, stored := result.Load()
	if !stored {
		return empty, fault(api.ShareInvalidRequest)
	}
	return value, nil
}

func NewCommands(service Workflows) *Commands {
	commands := &Commands{runtime: dispatcher.NewRuntime()}
	registerCommand(commands, service.Create)
	registerCommand(commands, service.CopyLink)
	registerCommand(commands, service.Revoke)
	registerCommand(commands, service.Redeem)
	registerCommand(commands, service.AdmitViewer)
	registerCommand(commands, service.OpenViewer)
	registerCommand(commands, func(ctx context.Context, msg CloseShareViewer) (struct{}, error) {
		return struct{}{}, service.CloseViewer(ctx, msg)
	})
	return commands
}

func (c *Commands) Close() {
	c.close.Do(func() {
		for _, sub := range c.subs {
			sub.Unsubscribe()
		}
	})
}
