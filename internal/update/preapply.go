package update

import "context"

type beforeSwapCallbackKey struct{}

// WithBeforeSwap attaches an optional callback to run before update files are
// installed.
func WithBeforeSwap(ctx context.Context, callback func(context.Context) error) context.Context {
	return context.WithValue(ctx, beforeSwapCallbackKey{}, callback)
}

func beforeSwapCallback(ctx context.Context) func(context.Context) error {
	callback, _ := ctx.Value(beforeSwapCallbackKey{}).(func(context.Context) error)
	return callback
}
