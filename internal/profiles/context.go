package profiles

import "context"

type ctxKey struct{}

// EchoContextKey is the key under which the middleware also stores the profile in echo.Context.
const EchoContextKey = "profiles.current"

// WithProfile returns a context carrying the profile.
func WithProfile(ctx context.Context, p *Profile) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the profile of the request, or nil.
func FromContext(ctx context.Context) *Profile {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(ctxKey{}).(*Profile)
	return p
}

// IDFromContext returns the profile id of the request, or 0.
func IDFromContext(ctx context.Context) int64 {
	if p := FromContext(ctx); p != nil {
		return p.ID
	}
	return 0
}
