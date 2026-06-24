package proxemby

import (
	"context"
	"net/url"
)

type resourceTargetKey struct{}

func withResourceTarget(ctx context.Context, target *url.URL) context.Context {
	return context.WithValue(ctx, resourceTargetKey{}, target)
}
