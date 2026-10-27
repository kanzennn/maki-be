// Package requestid carries a request's ID on its context: the RequestID
// middleware puts it there, and the logs and the error envelope read it.
package requestid

import "context"

type key struct{}

// WithRequestID returns a context carrying the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, key{}, id)
}

// RequestID returns the request ID carried by ctx, or "" if there is none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(key{}).(string)
	return id
}
