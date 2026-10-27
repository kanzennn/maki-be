package models

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

func TestPrincipalOnContext(t *testing.T) {
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Error("PrincipalFrom found a principal on an empty context")
	}
	p := Principal{UserID: "u1", IsOwner: true, Permissions: NewPermissions("menu.view")}
	got, ok := PrincipalFrom(WithPrincipal(context.Background(), p))
	if !ok || !reflect.DeepEqual(got, p) {
		t.Errorf("PrincipalFrom = %+v, %v; want %+v", got, ok, p)
	}
}

func TestPermissions(t *testing.T) {
	p := NewPermissions("menu.view", "checkout.create", "menu.view")
	if !p.Has("menu.view") || !p.Has("checkout.create") || p.Has("menu.manage") {
		t.Errorf("Has on %v", p)
	}
	if got := p.List(); !slices.Equal(got, []string{"checkout.create", "menu.view"}) {
		t.Errorf("List = %v, want sorted and without duplicates", got)
	}
	var none Permissions
	if none.Has("menu.view") || none.List() == nil || len(none.List()) != 0 {
		t.Error("the zero Permissions should hold nothing, and list as empty rather than nil")
	}
}
