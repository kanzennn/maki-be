package user

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/response"
)

const roles = "/api/v1/outlets/o1/roles"

// createRole makes a role as who and returns it.
func (h *harness) createRole(who, name string, permissions ...string) models.RoleDetailResponse {
	h.t.Helper()
	return h.do(http.MethodPost, roles, who, map[string]any{"name": name, "permissions": permissions}).
		status(http.StatusCreated, "").role()
}

func TestPermissionCatalog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var got models.PermissionsResponse
	h.do(http.MethodGet, "/api/v1/permissions", "cashier", nil).status(http.StatusOK, "").decode(&got)

	var modules []string
	codes := 0
	for _, g := range got.Groups {
		modules = append(modules, g.Module)
		codes += len(g.Permissions)
	}
	if !slices.Equal(modules, []string{"dashboard", "role", "user", "menu", "table", "checkout", "payment", "log"}) || codes != 14 {
		t.Errorf("catalog = %+v, want the 8 groups and 14 permissions", got)
	}
}

func TestCreateRole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.do(http.MethodPost, roles, "manager", map[string]any{
		"name": "  Pelayan ", "description": "Antar pesanan", "permissions": []string{"table.view", "menu.view", "table.view"},
	}).status(http.StatusCreated, "").role()
	if r.Name != "Pelayan" || *r.Description != "Antar pesanan" || r.UserCount != 0 || len(r.Users) != 0 {
		t.Errorf("role = %+v", r)
	}
	if !slices.Equal(r.Permissions, []string{"menu.view", "table.view"}) {
		t.Errorf("permissions = %v, want them sorted, each once", r.Permissions)
	}
	if got := h.actions(r.RoleID); !slices.Equal(got, []string{"role.created"}) {
		t.Errorf("Log Action = %v", got)
	}

	h.do(http.MethodPost, roles, "manager", map[string]any{"name": "PELAYAN"}).status(http.StatusConflict, "role_name_taken")
	// The same name in another outlet is fine.
	h.do(http.MethodPost, "/api/v1/outlets/o1/roles", "manager", map[string]any{"name": "Kosong"}).status(http.StatusCreated, "")

	for name, tt := range map[string]struct {
		body        map[string]any
		field, rule string
	}{
		"no name":            {map[string]any{"permissions": []string{}}, "name", "required"},
		"blank name":         {map[string]any{"name": "  "}, "name", "required"},
		"long name":          {map[string]any{"name": strings.Repeat("n", 51)}, "name", "max"},
		"long description":   {map[string]any{"name": "Dapur", "description": strings.Repeat("d", 201)}, "description", "max"},
		"unknown permission": {map[string]any{"name": "Dapur", "permissions": []string{"menu.cook"}}, "permissions", "oneof"},
	} {
		t.Run(name, func(t *testing.T) {
			r := h.do(http.MethodPost, roles, "manager", tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
			if r.fields()[tt.field] != tt.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tt.field, tt.rule)
			}
		})
	}
}

func TestRolesGiveNoMoreThanTheCallerHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.do(http.MethodPost, roles, "supervisor", map[string]any{
		"name": "Kasir senior", "permissions": []string{"checkout.create", "menu.manage", "order.void"},
	}).status(http.StatusForbidden, "permission_not_held")
	if got := r.envelope().Error.Details["permissions"]; len(got.([]any)) != 2 {
		t.Errorf("details.permissions = %v, want menu.manage and order.void", got)
	}
	h.createRole("supervisor", "Kasir senior", "checkout.create")

	// A role the Manager made with permissions the supervisor lacks: they
	// may rename it or take those away, but not add more.
	menu := h.createRole("manager", "Menu", "menu.manage", "menu.view")
	path := roles + "/" + menu.RoleID
	h.do(http.MethodPut, path, "supervisor", map[string]any{"name": "Menu & harga", "permissions": []string{"menu.manage", "menu.view"}}).
		status(http.StatusOK, "")
	h.do(http.MethodPut, path, "supervisor", map[string]any{"name": "Menu & harga", "permissions": []string{"menu.manage", "menu.view", "stock.adjust"}}).
		status(http.StatusForbidden, "permission_not_held")
	if got := h.do(http.MethodPut, path, "supervisor", map[string]any{"name": "Menu", "permissions": []string{"menu.view"}}).
		status(http.StatusOK, "").role().Permissions; !slices.Equal(got, []string{"menu.view"}) {
		t.Errorf("permissions = %v, want menu.manage dropped", got)
	}
}

func TestListAndGetRoles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	h.createRole("manager", "dapur", "menu.view")
	h.createRole("manager", "Pelayan", "table.view")
	h.exec(`INSERT INTO roles (role_id, outlet_id, name) VALUES ('elsewhere', 'o2', 'Kasir')`)
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', $1, 'o1'), ('s1', $1, 'o1')`, kasir.RoleID)
	// A deleted user no longer holds anything.
	h.exec(`UPDATE users SET deleted_at = now() WHERE user_id = 's1'`)

	var list response.List[models.RoleResponse]
	h.do(http.MethodGet, roles, "manager", nil).status(http.StatusOK, "").decode(&list)
	var names []string
	for _, r := range list.Data {
		names = append(names, r.Name)
	}
	if !slices.Equal(names, []string{"dapur", "Kasir", "Pelayan"}) || list.Meta.Total != 3 {
		t.Errorf("roles = %v (total %d), want o1's three, by name whatever its case", names, list.Meta.Total)
	}
	if list.Data[1].UserCount != 1 {
		t.Errorf("user_count of Kasir = %d, want 1", list.Data[1].UserCount)
	}
	h.do(http.MethodGet, roles+"?page=2&page_size=2", "manager", nil).status(http.StatusOK, "").decode(&list)
	if len(list.Data) != 1 || list.Meta.Total != 3 {
		t.Errorf("page 2 of 2 = %+v", list)
	}

	got := h.do(http.MethodGet, roles+"/"+kasir.RoleID, "manager", nil).status(http.StatusOK, "").role()
	if len(got.Users) != 1 || got.Users[0].Username != "budi" {
		t.Errorf("users of Kasir = %+v, want budi", got.Users)
	}
	h.do(http.MethodGet, roles+"/nothing", "manager", nil).status(http.StatusNotFound, "role_not_found")
	h.do(http.MethodGet, roles+"/elsewhere", "manager", nil).status(http.StatusNotFound, "role_not_found")
}

func TestUpdateRole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := h.createRole("manager", "Kasir", "checkout.create")
	h.createRole("manager", "Dapur", "menu.view")
	path := roles + "/" + r.RoleID

	got := h.do(http.MethodPut, path, "manager", map[string]any{"name": "Kasir", "permissions": []string{"checkout.create", "order.void"}}).
		status(http.StatusOK, "").role()
	if !slices.Equal(got.Permissions, []string{"checkout.create", "order.void"}) {
		t.Errorf("permissions = %v", got.Permissions)
	}
	var before, after string
	h.scalar(&before, `SELECT before_json::text FROM action_logs WHERE action = 'role.updated'`)
	h.scalar(&after, `SELECT after_json::text FROM action_logs WHERE action = 'role.updated'`)
	if before != `{"permissions": ["checkout.create"]}` || after != `{"permissions": ["checkout.create", "order.void"]}` {
		t.Errorf("role.updated before %s after %s, want only the permissions", before, after)
	}

	// Saving it unchanged records nothing.
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Kasir", "permissions": []string{"order.void", "checkout.create"}}).
		status(http.StatusOK, "")
	if got := h.actions(r.RoleID); !slices.Equal(got, []string{"role.created", "role.updated"}) {
		t.Errorf("Log Action = %v", got)
	}
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "dapur"}).status(http.StatusConflict, "role_name_taken")
	h.do(http.MethodPut, roles+"/nothing", "manager", map[string]any{"name": "X"}).status(http.StatusNotFound, "role_not_found")
}

func TestDeleteRole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := h.createRole("manager", "Kasir", "checkout.create")
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', $1, 'o1')`, r.RoleID)
	path := roles + "/" + r.RoleID

	resp := h.do(http.MethodDelete, path, "manager", nil).status(http.StatusConflict, "role_in_use")
	users, _ := resp.envelope().Error.Details["users"].([]any)
	if len(users) != 1 || users[0].(map[string]any)["name"] != "Budi" {
		t.Errorf("details.users = %v, want Budi", resp.envelope().Error.Details)
	}

	h.exec(`DELETE FROM user_roles WHERE user_id = 'k1'`)
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, path, "manager", nil).status(http.StatusNotFound, "role_not_found")
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNotFound, "role_not_found")
	h.createRole("manager", "Kasir") // the name is free again
	if got := h.actions(r.RoleID); !slices.Equal(got, []string{"role.created", "role.deleted"}) {
		t.Errorf("Log Action = %v", got)
	}
}

func TestRoleAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := h.createRole("manager", "Kasir", "checkout.create")

	// The Owner looks into every outlet's roles, but changes none.
	h.do(http.MethodGet, roles, "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodGet, "/api/v1/outlets/o2/roles", "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodPost, roles, "owner", map[string]any{"name": "X"}).status(http.StatusForbidden, "forbidden")

	h.do(http.MethodGet, roles, "cashier", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodDelete, roles+"/"+r.RoleID, "cashier", nil).status(http.StatusForbidden, "forbidden")

	// Whoever sees users lists the roles too, to filter by one or give one,
	// but opens no role and changes none.
	h.do(http.MethodGet, roles, "hr", nil).status(http.StatusOK, "")
	h.do(http.MethodGet, roles+"/"+r.RoleID, "hr", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodPut, roles+"/"+r.RoleID, "hr", map[string]any{"name": "X"}).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o2/roles", "manager", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, roles, "", nil).status(http.StatusUnauthorized, "unauthorized")
}
