package user

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

const staffPath = "/api/v1/outlets/o1/users"

func (r response) staff() staffResponse {
	r.t.Helper()
	var out staffResponse
	r.decode(&out)
	return out
}

func newStaff(username, roleID string) map[string]any {
	return map[string]any{"name": "Dewi", "username": username, "password": "kopi susu 123", "role_id": roleID}
}

func TestCreateStaff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	h.exec(`INSERT INTO roles (role_id, outlet_id, name) VALUES ('elsewhere', 'o2', 'Kasir')`)

	body := newStaff(" Dewi.K ", kasir.RoleID)
	body["email"], body["phone"] = "Dewi@Maki.Test", "0812 3456"
	u := h.do(http.MethodPost, staffPath, "manager", body).status(http.StatusCreated, "").staff()
	if u.Username != "dewi.k" || *u.Email != "dewi@maki.test" || *u.Phone != "0812 3456" || u.IsManager {
		t.Errorf("user = %+v", u)
	}
	if u.Role == nil || u.Role.Name != "Kasir" || u.Status != "active" || !u.MustChangePassword || u.LastLoginAt != nil {
		t.Errorf("user = %+v, want active in Kasir, with a password to change and no sign-in yet", u)
	}
	var hash string
	h.scalar(&hash, `SELECT password_hash FROM users WHERE user_id = $1`, u.UserID)
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash = %q", hash)
	}
	var after string
	h.scalar(&after, `SELECT after_json->>'role_id' FROM action_logs WHERE action = 'user.created'`)
	if after != kasir.RoleID {
		t.Errorf("user.created after.role_id = %q", after)
	}

	h.do(http.MethodPost, staffPath, "manager", newStaff("DEWI.K", kasir.RoleID)).status(http.StatusConflict, "username_taken")
	h.exec(`UPDATE users SET deleted_at = now() WHERE username = 'budi'`)
	h.do(http.MethodPost, staffPath, "manager", newStaff("budi", kasir.RoleID)).status(http.StatusConflict, "username_taken")

	with := func(k string, v any) map[string]any {
		b := newStaff("baru", kasir.RoleID)
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
		return b
	}
	for name, tt := range map[string]struct {
		body        map[string]any
		field, rule string
	}{
		"no name":              {with("name", nil), "name", "required"},
		"short username":       {with("username", "ab"), "username", "format"},
		"username with spaces": {with("username", "dewi k"), "username", "format"},
		"not an email":         {with("email", "dewi@"), "email", "email"},
		"long phone":           {with("phone", strings.Repeat("1", 33)), "phone", "max"},
		"no role":              {with("role_id", nil), "role_id", "required"},
		"role of another shop": {with("role_id", "elsewhere"), "role_id", "exists"},
	} {
		t.Run(name, func(t *testing.T) {
			r := h.do(http.MethodPost, staffPath, "manager", tt.body).status(http.StatusUnprocessableEntity, "")
			if r.fields()[tt.field] != tt.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tt.field, tt.rule)
			}
		})
	}
	h.do(http.MethodPost, staffPath, "manager", with("password", "pendek")).status(http.StatusUnprocessableEntity, "password_too_short")
	h.do(http.MethodPost, staffPath, "manager", with("password", "baru")).status(http.StatusUnprocessableEntity, "password_too_short")
}

func TestManagerAccountsAreTheOwners(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")

	mgr := h.do(http.MethodPost, staffPath, "owner", map[string]any{
		"name": "Joko", "username": "joko", "password": "kopi susu 123", "is_manager": true,
	}).status(http.StatusCreated, "").staff()
	if !mgr.IsManager || mgr.Role != nil || !mgr.MustChangePassword {
		t.Errorf("manager = %+v", mgr)
	}
	h.do(http.MethodPost, staffPath, "manager", map[string]any{
		"name": "Lain", "username": "lain", "password": "kopi susu 123", "is_manager": true,
	}).status(http.StatusForbidden, "manager_account")
	h.do(http.MethodPost, staffPath, "owner", newStaff("dewi", kasir.RoleID)).status(http.StatusForbidden, "owner_manages_managers")

	path := staffPath + "/" + mgr.UserID
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Joko", "is_manager": true}).status(http.StatusForbidden, "manager_account")
	h.do(http.MethodPut, path, "owner", map[string]any{"name": "Joko Susilo", "is_manager": true}).status(http.StatusOK, "")
	h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"status": "inactive"}).status(http.StatusForbidden, "manager_account")
	h.do(http.MethodPost, path+"/reset-password", "owner", map[string]any{"password": "es jeruk 789"}).status(http.StatusNoContent, "")
	h.do(http.MethodPatch, path+"/status", "owner", map[string]any{"status": "inactive"}).status(http.StatusOK, "")
	h.do(http.MethodPatch, staffPath+"/k1/status", "owner", map[string]any{"status": "inactive"}).status(http.StatusForbidden, "owner_manages_managers")
}

func TestPromoteAndDemote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', $1, 'o1')`, kasir.RoleID)
	path := staffPath + "/k1"

	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Budi", "is_manager": true}).status(http.StatusForbidden, "manager_account")
	h.do(http.MethodPut, path, "owner", map[string]any{"name": "Budi", "role_id": kasir.RoleID}).status(http.StatusForbidden, "owner_manages_managers")

	h.signIn("k1")
	u := h.do(http.MethodPut, path, "owner", map[string]any{"name": "Budi", "is_manager": true}).status(http.StatusOK, "").staff()
	if !u.IsManager || u.Role != nil {
		t.Errorf("promoted = %+v, want a Manager without a role", u)
	}
	if h.signedIn("k1") {
		t.Error("promotion kept the session")
	}

	h.exec(`DELETE FROM refresh_tokens WHERE user_id = 'k1'`)
	h.signIn("k1")
	h.do(http.MethodPut, path, "owner", map[string]any{"name": "Budi", "is_manager": false}).status(http.StatusUnprocessableEntity, "validation_failed")
	u = h.do(http.MethodPut, path, "owner", map[string]any{"name": "Budi", "is_manager": false, "role_id": kasir.RoleID}).
		status(http.StatusOK, "").staff()
	if u.IsManager || u.Role == nil || u.Role.RoleID != kasir.RoleID {
		t.Errorf("demoted = %+v, want staff in Kasir", u)
	}
	if h.signedIn("k1") {
		t.Error("demotion kept the session")
	}
	if got := h.actions("k1"); !slices.Equal(got, []string{"user.promoted", "user.demoted"}) {
		t.Errorf("Log Action = %v", got)
	}
}

func TestNobodyManagesUpwards(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	dapur := h.createRole("manager", "Dapur", "menu.manage", "menu.view")
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', $1, 'o1'), ('s1', $2, 'o1')`, kasir.RoleID, dapur.RoleID)

	// hr holds user.manage and checkout.create, but no menu permission.
	h.do(http.MethodPost, staffPath, "hr", newStaff("dewi", kasir.RoleID)).status(http.StatusCreated, "")
	r := h.do(http.MethodPost, staffPath, "hr", newStaff("rudi", dapur.RoleID)).status(http.StatusForbidden, "permission_not_held")
	if got := r.envelope().Error.Details["permissions"]; len(got.([]any)) != 2 {
		t.Errorf("details.permissions = %v", got)
	}
	// Sari holds Dapur's menu permissions, so hr may not take over her account.
	h.do(http.MethodPost, staffPath+"/s1/reset-password", "hr", map[string]any{"password": "es jeruk 789"}).
		status(http.StatusForbidden, "permission_not_held")
	h.do(http.MethodPatch, staffPath+"/s1/status", "hr", map[string]any{"status": "inactive"}).status(http.StatusForbidden, "permission_not_held")
	h.do(http.MethodDelete, staffPath+"/s1", "hr", nil).status(http.StatusForbidden, "permission_not_held")
	h.do(http.MethodPut, staffPath+"/k1", "hr", map[string]any{"name": "Budi", "role_id": dapur.RoleID}).
		status(http.StatusForbidden, "permission_not_held")

	h.do(http.MethodPatch, staffPath+"/k1/status", "hr", map[string]any{"status": "inactive"}).status(http.StatusOK, "")
	h.do(http.MethodPut, staffPath+"/k1", "hr", map[string]any{"name": "Budi Santoso", "role_id": kasir.RoleID}).status(http.StatusOK, "")
}

func TestNobodyManagesTheirOwnAccountHere(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('h1', $1, 'o1')`, kasir.RoleID)

	h.do(http.MethodPut, staffPath+"/m1", "manager", map[string]any{"name": "Rina", "is_manager": true}).status(http.StatusForbidden, "own_account")
	h.do(http.MethodDelete, staffPath+"/h1", "hr", nil).status(http.StatusForbidden, "own_account")
	h.do(http.MethodPost, staffPath+"/h1/reset-password", "hr", map[string]any{"password": "es jeruk 789"}).status(http.StatusForbidden, "own_account")
	h.do(http.MethodPatch, staffPath+"/h1/status", "hr", map[string]any{"status": "inactive"}).status(http.StatusForbidden, "own_account")
}

func TestListStaff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', $1, 'o1'), ('s1', $1, 'o1')`, kasir.RoleID)
	h.exec(`UPDATE users SET status = 'inactive' WHERE user_id = 's1'`)
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, password_hash, deleted_at)
		VALUES ('gone', 'o1', 'Lama', 'lama', 'x', now()), ('far', 'o2', 'Jauh', 'jauh', 'x', NULL)`)

	names := func(query string) []string {
		var l httpx.List[staffResponse]
		h.do(http.MethodGet, staffPath+query, "manager", nil).status(http.StatusOK, "").decode(&l)
		var out []string
		for _, u := range l.Data {
			out = append(out, u.Name)
		}
		return out
	}
	if got := names(""); !slices.Equal(got, []string{"Budi", "Hana", "Rina", "Sari"}) {
		t.Errorf("users = %v, want o1's four, by name", got)
	}
	for query, want := range map[string][]string{
		"?status=inactive":         {"Sari"},
		"?role_id=" + kasir.RoleID: {"Budi", "Sari"},
		"?q=RIN":                   {"Rina"},
		"?q=budi":                  {"Budi"},
		"?q=_":                     nil,
		"?page=2&page_size=3":      {"Sari"},
	} {
		if got := names(query); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", query, got, want)
		}
	}
	h.do(http.MethodGet, staffPath+"?status=gone", "manager", nil).status(http.StatusUnprocessableEntity, "validation_failed")

	u := h.do(http.MethodGet, staffPath+"/k1", "manager", nil).status(http.StatusOK, "").staff()
	if u.Role == nil || u.Role.Name != "Kasir" {
		t.Errorf("k1 = %+v", u)
	}
	h.do(http.MethodGet, staffPath+"/gone", "manager", nil).status(http.StatusNotFound, "user_not_found")
	h.do(http.MethodGet, staffPath+"/far", "manager", nil).status(http.StatusNotFound, "user_not_found")
}

func TestStatusPasswordAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', $1, 'o1')`, kasir.RoleID)
	path := staffPath + "/k1"
	reset := func() { h.exec(`DELETE FROM refresh_tokens WHERE user_id = 'k1'`); h.signIn("k1") }

	h.signIn("k1")
	if got := h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"status": "inactive"}).status(http.StatusOK, "").staff().Status; got != "inactive" {
		t.Errorf("status = %q", got)
	}
	if h.signedIn("k1") {
		t.Error("deactivating kept the session")
	}
	h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"status": "inactive"}).status(http.StatusOK, "")
	h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"status": "active"}).status(http.StatusOK, "")

	reset()
	h.do(http.MethodPost, path+"/reset-password", "manager", map[string]any{"password": "pendek"}).status(http.StatusUnprocessableEntity, "password_too_short")
	h.do(http.MethodPost, path+"/reset-password", "manager", map[string]any{"password": "es jeruk 789"}).status(http.StatusNoContent, "")
	var mustChange bool
	h.scalar(&mustChange, `SELECT must_change_password FROM users WHERE user_id = 'k1'`)
	if !mustChange || h.signedIn("k1") {
		t.Errorf("after the reset: must_change_password %v, signed in %v; want true, false", mustChange, h.signedIn("k1"))
	}

	reset()
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, path, "manager", nil).status(http.StatusNotFound, "user_not_found")
	if h.signedIn("k1") {
		t.Error("deleting kept the session")
	}
	h.do(http.MethodPost, staffPath, "manager", newStaff("budi", kasir.RoleID)).status(http.StatusConflict, "username_taken")
	// Nobody holds Kasir any more, so it can go.
	h.do(http.MethodDelete, roles+"/"+kasir.RoleID, "manager", nil).status(http.StatusNoContent, "")

	want := []string{"user.deactivated", "user.activated", "user.password_reset", "user.deleted"}
	if got := h.actions("k1"); !slices.Equal(got, want) {
		t.Errorf("Log Action = %v, want %v", got, want)
	}
}

func TestUpdateStaffProfile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kasir := h.createRole("manager", "Kasir", "checkout.create")
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', $1, 'o1')`, kasir.RoleID)
	h.exec(`UPDATE users SET email = 'budi@maki.test', email_verified_at = now() WHERE user_id = 'k1'`)
	path := staffPath + "/k1"
	verified := func() bool {
		var v bool
		h.scalar(&v, `SELECT email_verified_at IS NOT NULL FROM users WHERE user_id = 'k1'`)
		return v
	}

	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Budi S", "email": "budi@maki.test", "role_id": kasir.RoleID}).status(http.StatusOK, "")
	if !verified() {
		t.Error("the same email lost its verification")
	}
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Budi S", "email": "budi.s@maki.test", "role_id": kasir.RoleID}).status(http.StatusOK, "")
	if verified() {
		t.Error("an email someone else set counts as verified")
	}
	var before string
	h.scalar(&before, `SELECT before_json::text FROM action_logs WHERE action = 'user.updated' ORDER BY occurred_at LIMIT 1`)
	if before != `{"name": "Budi"}` {
		t.Errorf("first user.updated before = %s, want only the name", before)
	}
}

func TestStaffAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.do(http.MethodGet, staffPath, "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodGet, "/api/v1/outlets/o2/users", "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodGet, staffPath, "cashier", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodPost, staffPath, "supervisor", newStaff("dewi", "x")).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o2/users", "manager", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, staffPath, "", nil).status(http.StatusUnauthorized, "unauthorized")
}
