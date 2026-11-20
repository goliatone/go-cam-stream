package conversations

import (
	"context"
	"sync"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-command"
	"github.com/goliatone/go-command/dispatcher"
	"github.com/goliatone/go-command/runner"
)

type Commands struct {
	runtime *dispatcher.Runtime
	subs    []dispatcher.Subscription
	close   sync.Once
}

func register[M command.Message, R any](c *Commands, run func(context.Context, M) (R, error)) {
	handler := command.CommandFunc[M](func(ctx context.Context, msg M) error {
		result := command.ResultFromContext[R](ctx)
		if result == nil {
			return api.ErrInvalidConversationRequest
		}
		value, err := run(ctx, msg)
		if err != nil {
			result.StoreError(err)
		} else {
			result.Store(value)
		}
		return err
	})
	c.subs = append(c.subs, dispatcher.SubscribeCommandTo(c.runtime, handler, runner.WithMaxRetries(0)))
}

func NewCommands(service Workflows) *Commands {
	c := &Commands{runtime: dispatcher.NewRuntime()}
	register(c, service.Create)
	register(c, service.Redeem)
	register(c, service.Join)
	register(c, service.PublishSignal)
	register(c, service.SetPublication)
	register(c, service.SetConnection)
	register(c, service.Leave)
	register(c, service.End)
	return c
}

func Dispatch[R any](ctx context.Context, commands *Commands, msg command.Message) (R, error) {
	var empty R
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
		return empty, api.ErrInvalidConversationRequest
	}
	return value, nil
}

func (c *Commands) Close() {
	c.close.Do(func() {
		for _, sub := range c.subs {
			sub.Unsubscribe()
		}
	})
}
