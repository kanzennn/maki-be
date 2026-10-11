package authn

import (
	"context"
	"errors"
	"maps"
	"slices"
)

// ErrNoSession is what a PrincipalLoader returns when the session a valid
// token names can't be used anymore: signed out, revoked, or its user
// deactivated or deleted.
var ErrNoSession = errors.New("authn: the session no longer exists")

// PrincipalLoader loads the principal of a verified session; the auth module
// implements it. It returns ErrNoSession for a session that can't be used,
// and any other error for a failure to find out, such as the database being
// down.
type PrincipalLoader interface {
	LoadPrincipal(ctx context.Context, s Session) (Principal, error)
}

// Principal is the signed-in user a request acts for, as the Authenticate
// middleware loaded it for this request.
type Principal struct {
	UserID    string
	SessionID string
	Username  string
	Name      string
	IsOwner   bool
	IsManager bool
	// OutletID is the outlet the user belongs to; empty for the Owner.
	OutletID string
	// Locale is the user's language after the fallbacks: their own choice,
	// else their outlet's, else Indonesian.
	Locale string
	// MustChangePassword is set until the user replaces a password someone
	// else gave them; every endpoint but changing it is refused until then.
	MustChangePassword bool
	// Permissions are what the user may do: in their own outlet, or for the
	// Owner in any outlet. Loaded with the rest on every request, so a change
	// to a role applies from the next one.
	Permissions Permissions
}

// Permissions is a set of permission codes such as "menu.manage". The user
// module defines the codes; modules name the one each route needs.
type Permissions map[string]struct{}

// NewPermissions returns the set of codes.
func NewPermissions(codes ...string) Permissions {
	p := make(Permissions, len(codes))
	for _, c := range codes {
		p[c] = struct{}{}
	}
	return p
}

// Has reports whether code is in the set.
func (p Permissions) Has(code string) bool {
	_, ok := p[code]
	return ok
}

// List returns the codes, sorted; empty but never nil, so JSON shows [].
func (p Permissions) List() []string {
	list := slices.AppendSeq(make([]string, 0, len(p)), maps.Keys(p))
	slices.Sort(list)
	return list
}

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal carried by ctx. ok is false on routes
// without the Authenticate middleware.
func PrincipalFrom(ctx context.Context) (p Principal, ok bool) {
	p, ok = ctx.Value(principalKey{}).(Principal)
	return p, ok
}
