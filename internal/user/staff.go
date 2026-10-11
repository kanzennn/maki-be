package user

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/platform/password"
	"github.com/kervzent/maki-be/internal/user/store"
)

// Who manages whom, from the spec's access matrix:
//
//   - the Owner manages Manager accounts, and promotes staff to Manager;
//   - Managers, and staff whose role has user.manage, manage staff;
//   - nobody manages their own account here: their settings go through /me;
//   - and nobody hands out more than they hold: they may only manage staff
//     whose permissions they all hold, and give roles they all hold.
//     Otherwise a supervisor could reset a stronger user's password and sign
//     in as them.

// Staff is one of an outlet's Manager or staff accounts.
type Staff struct {
	UserID             string
	Name               string
	Username           string
	Email              *string
	Phone              *string
	IsManager          bool
	Active             bool
	MustChangePassword bool
	// Role is a staff member's custom role; nil for a Manager, or for staff
	// whose role was deleted.
	Role        *StaffRole
	LastLoginAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// StaffRole is the role a staff member holds.
type StaffRole struct {
	RoleID, Name string
}

// StaffFilter narrows the list of an outlet's users.
type StaffFilter struct {
	Search string // part of the name or the username
	Status string // active, inactive, or empty for both
	RoleID string
}

// StaffInput is an account as filled in. Username and Password only count
// when creating it; RoleID is for staff, IsManager for a Manager.
type StaffInput struct {
	Name      string
	Username  string
	Email     *string
	Phone     *string
	Password  string
	RoleID    *string
	IsManager bool
}

// ListStaff returns a page of the outlet's users, by name, and how many
// match in all.
func (s *Service) ListStaff(ctx context.Context, outletID string, f StaffFilter, page httpx.Page) ([]Staff, int, error) {
	status, roleID, search := optional(f.Status), optional(f.RoleID), optional(strings.TrimSpace(f.Search))
	if search != nil {
		*search = likeEscaper.Replace(*search)
	}
	q := store.New(s.pool)
	total, err := q.CountOutletUsers(ctx, store.CountOutletUsersParams{OutletID: outletID, Status: status, RoleID: roleID, Search: search})
	if err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	rows, err := q.ListOutletUsers(ctx, store.ListOutletUsersParams{
		OutletID: outletID, Status: status, RoleID: roleID, Search: search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	out := make([]Staff, len(rows))
	for i, r := range rows {
		out[i] = staffFrom(store.GetOutletUserRow(r))
	}
	return out, int(total), nil
}

// likeEscaper keeps LIKE's wildcards in a search literal.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Staff returns one of the outlet's users.
func (s *Service) Staff(ctx context.Context, outletID, userID string) (Staff, error) {
	r, err := store.New(s.pool).GetOutletUser(ctx, store.GetOutletUserParams{UserID: userID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Staff{}, ErrUserNotFound
	}
	if err != nil {
		return Staff{}, fmt.Errorf("load user %s: %w", userID, err)
	}
	return staffFrom(r), nil
}

// CreateStaff adds an account to the outlet: a Manager, by the Owner, or a
// staff member with a role, by everyone else who may. The password is
// temporary: the user must change it at the first sign-in.
func (s *Service) CreateStaff(ctx context.Context, p authn.Principal, ip, outletID string, in StaffInput) (Staff, error) {
	profile, err := checkStaff(in, true)
	if err != nil {
		return Staff{}, err
	}
	switch {
	case in.IsManager && !p.IsOwner:
		return Staff{}, ErrManagerAccount
	case !in.IsManager && p.IsOwner:
		return Staff{}, ErrOwnerManagesManagers
	}
	if err := password.Check(in.Password, profile.Username); err != nil {
		return Staff{}, err
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return Staff{}, err
	}
	userID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if !in.IsManager {
			if err := s.giveable(ctx, q, p, outletID, *in.RoleID); err != nil {
				return err
			}
		}
		if err := usernameTaken(q.InsertStaff(ctx, store.InsertStaffParams{
			UserID: userID, OutletID: &outletID, Name: profile.Name, Username: profile.Username,
			Email: profile.Email, Phone: profile.Phone, PasswordHash: hash, IsManager: in.IsManager,
		})); err != nil {
			return err
		}
		after := profile.snapshot()
		after["is_manager"] = in.IsManager
		if !in.IsManager {
			if err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: *in.RoleID, OutletID: outletID}); err != nil {
				return err
			}
			after["role_id"] = *in.RoleID
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "user.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "user", TargetID: userID, After: after, IP: ip,
		})
	})
	if err != nil {
		return Staff{}, err
	}
	return s.Staff(ctx, outletID, userID)
}

// UpdateStaff changes an account's name, email and phone, and its role or
// rank. Only the Owner changes a Manager account, or makes a staff member a
// Manager; that, and making a Manager staff again, ends the user's sessions.
func (s *Service) UpdateStaff(ctx context.Context, p authn.Principal, ip, outletID, userID string, in StaffInput) (Staff, error) {
	next, err := checkStaff(in, false)
	if err != nil {
		return Staff{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		target, err := lockStaff(ctx, q, outletID, userID)
		if err != nil {
			return err
		}
		switch {
		case target.UserID == p.UserID:
			return ErrOwnAccount
		case p.IsOwner && !target.IsManager && !in.IsManager:
			// Promoting is the Owner's one move on a staff account.
			return ErrOwnerManagesManagers
		case !p.IsOwner && (target.IsManager || in.IsManager):
			return ErrManagerAccount
		case !p.IsOwner:
			if err := s.holdsAllOf(ctx, q, p, target.UserID); err != nil {
				return err
			}
		}

		if err := s.updateProfile(ctx, q, tx, p, ip, outletID, target, next); err != nil {
			return err
		}
		prevRole, err := q.GetUserRoleID(ctx, target.UserID)
		if err != nil {
			return err
		}
		switch {
		case in.IsManager && !target.IsManager:
			return s.changeRank(ctx, q, tx, p, ip, outletID, target.UserID, true, prevRole, "")
		case !in.IsManager && target.IsManager:
			// A Manager held every permission, so any role takes some away.
			if err := s.roleOfOutlet(ctx, q, outletID, *in.RoleID); err != nil {
				return err
			}
			return s.changeRank(ctx, q, tx, p, ip, outletID, target.UserID, false, "", *in.RoleID)
		case !in.IsManager && *in.RoleID != prevRole:
			if err := s.giveable(ctx, q, p, outletID, *in.RoleID); err != nil {
				return err
			}
			if err := q.ClearUserRole(ctx, target.UserID); err != nil {
				return err
			}
			if err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: target.UserID, RoleID: *in.RoleID, OutletID: outletID}); err != nil {
				return err
			}
			return s.audit.Record(ctx, tx, audit.Entry{
				Action: "user.role_changed", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: target.UserID,
				Before: map[string]any{"role_id": nullable(prevRole)}, After: map[string]any{"role_id": *in.RoleID}, IP: ip,
			})
		}
		return nil
	})
	if err != nil {
		return Staff{}, err
	}
	return s.Staff(ctx, outletID, userID)
}

// updateProfile stores changed name, email and phone, and records them.
func (s *Service) updateProfile(ctx context.Context, q *store.Queries, tx pgx.Tx, p authn.Principal, ip, outletID string, target store.LockOutletUserRow, next staffProfile) error {
	prev := staffProfile{Name: target.Name, Username: target.Username, Email: target.Email, Phone: target.Phone}
	before, after := changedFields(prev.snapshot(), next.snapshot())
	if len(after) == 0 {
		return nil
	}
	if err := q.UpdateStaffProfile(ctx, store.UpdateStaffProfileParams{
		UserID: target.UserID, Name: next.Name, Email: next.Email, Phone: next.Phone,
	}); err != nil {
		return err
	}
	return s.audit.Record(ctx, tx, audit.Entry{
		Action: "user.updated", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: target.UserID,
		Before: before, After: after, IP: ip,
	})
}

// changeRank makes the user a Manager (toManager) or staff with roleID,
// and ends their sessions: what they may do changes wholesale.
func (s *Service) changeRank(ctx context.Context, q *store.Queries, tx pgx.Tx, p authn.Principal, ip, outletID, userID string, toManager bool, prevRole, roleID string) error {
	if err := q.ClearUserRole(ctx, userID); err != nil {
		return err
	}
	if !toManager {
		if err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: roleID, OutletID: outletID}); err != nil {
			return err
		}
	}
	if err := q.SetManager(ctx, store.SetManagerParams{UserID: userID, IsManager: toManager}); err != nil {
		return err
	}
	if err := s.sessions.EndAll(ctx, tx, userID); err != nil {
		return err
	}
	action, before, after := "user.promoted", map[string]any{"role_id": nullable(prevRole)}, map[string]any{"is_manager": true}
	if !toManager {
		action, before, after = "user.demoted", map[string]any{"is_manager": true}, map[string]any{"role_id": roleID}
	}
	return s.audit.Record(ctx, tx, audit.Entry{
		Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID,
		Before: before, After: after, IP: ip,
	})
}

// SetStaffStatus activates or deactivates an account. Deactivating ends
// its sessions at once.
func (s *Service) SetStaffStatus(ctx context.Context, p authn.Principal, ip, outletID, userID string, active bool) (Staff, error) {
	status, action := "inactive", "user.deactivated"
	if active {
		status, action = "active", "user.activated"
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		target, err := s.lockManaged(ctx, q, p, outletID, userID)
		if err != nil || target.Status == status {
			return err
		}
		if err := q.SetUserStatus(ctx, store.SetUserStatusParams{UserID: userID, Status: status}); err != nil {
			return err
		}
		if !active {
			if err := s.sessions.EndAll(ctx, tx, userID); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID,
			Before: map[string]any{"status": target.Status}, After: map[string]any{"status": status}, IP: ip,
		})
	})
	if err != nil {
		return Staff{}, err
	}
	return s.Staff(ctx, outletID, userID)
}

// ResetStaffPassword gives an account a temporary password, which its user
// must change at their next sign-in. Their sessions end at once.
func (s *Service) ResetStaffPassword(ctx context.Context, p authn.Principal, ip, outletID, userID, pw string) error {
	target, err := s.Staff(ctx, outletID, userID)
	if err != nil {
		return err
	}
	if err := password.Check(pw, target.Username); err != nil {
		return err
	}
	// Hash before the transaction: it takes a tenth of a second.
	hash, err := s.hasher.Hash(ctx, pw)
	if err != nil {
		return err
	}
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := s.lockManaged(ctx, q, p, outletID, userID); err != nil {
			return err
		}
		if err := q.SetTemporaryPassword(ctx, store.SetTemporaryPasswordParams{UserID: userID, PasswordHash: hash}); err != nil {
			return err
		}
		if err := s.sessions.EndAll(ctx, tx, userID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "user.password_reset", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID, IP: ip,
		})
	})
}

// DeleteStaff soft-deletes an account and ends its sessions. Its username
// stays taken, and its data stays in the name it had.
func (s *Service) DeleteStaff(ctx context.Context, p authn.Principal, ip, outletID, userID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		target, err := s.lockManaged(ctx, q, p, outletID, userID)
		if err != nil {
			return err
		}
		if err := q.DeleteUser(ctx, userID); err != nil {
			return err
		}
		if err := s.sessions.EndAll(ctx, tx, userID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "user.deleted", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID,
			Before: map[string]any{"name": target.Name, "username": target.Username, "is_manager": target.IsManager}, IP: ip,
		})
	})
}

// lockManaged locks an account p may manage: not their own, a Manager's
// only for the Owner, a staff member's only for everyone else, and then
// only one whose permissions p all holds.
func (s *Service) lockManaged(ctx context.Context, q *store.Queries, p authn.Principal, outletID, userID string) (store.LockOutletUserRow, error) {
	target, err := lockStaff(ctx, q, outletID, userID)
	if err != nil {
		return target, err
	}
	switch {
	case target.UserID == p.UserID:
		return target, ErrOwnAccount
	case p.IsOwner && !target.IsManager:
		return target, ErrOwnerManagesManagers
	case !p.IsOwner && target.IsManager:
		return target, ErrManagerAccount
	case !p.IsOwner:
		return target, s.holdsAllOf(ctx, q, p, userID)
	}
	return target, nil
}

// holdsAllOf refuses when the staff member holds a permission p lacks.
func (s *Service) holdsAllOf(ctx context.Context, q *store.Queries, p authn.Principal, userID string) error {
	codes, err := q.GetUserPermissions(ctx, userID)
	if err != nil {
		return fmt.Errorf("load permissions of %s: %w", userID, err)
	}
	return grantable(p, codes)
}

// giveable checks that the role is the outlet's and holds nothing p lacks.
// It locks the role, so it can't be deleted while it is being given.
func (s *Service) giveable(ctx context.Context, q *store.Queries, p authn.Principal, outletID, roleID string) error {
	r, err := q.LockRole(ctx, store.LockRoleParams{RoleID: roleID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Invalid(map[string]string{"role_id": "exists"})
	}
	if err != nil {
		return fmt.Errorf("lock role %s: %w", roleID, err)
	}
	return grantable(p, r.Permissions)
}

// roleOfOutlet checks that the role is the outlet's, and locks it.
func (s *Service) roleOfOutlet(ctx context.Context, q *store.Queries, outletID, roleID string) error {
	_, err := q.LockRole(ctx, store.LockRoleParams{RoleID: roleID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Invalid(map[string]string{"role_id": "exists"})
	}
	return err
}

func lockStaff(ctx context.Context, q *store.Queries, outletID, userID string) (store.LockOutletUserRow, error) {
	r, err := q.LockOutletUser(ctx, store.LockOutletUserParams{UserID: userID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrUserNotFound
	}
	if err != nil {
		return r, fmt.Errorf("lock user %s: %w", userID, err)
	}
	return r, nil
}

// staffProfile is an account's own fields, checked, as the Log Action
// stores them.
type staffProfile struct {
	Name     string
	Username string
	Email    *string
	Phone    *string
}

func (p staffProfile) snapshot() map[string]any {
	return map[string]any{"name": p.Name, "email": p.Email, "phone": p.Phone}
}

// checkStaff validates an account as filled in, every problem at once as
// validation_failed. creating also checks the username; the password has
// rules of its own, which password.Check applies.
func checkStaff(in StaffInput, creating bool) (staffProfile, error) {
	fields := map[string]string{}
	p := staffProfile{
		Name:     strings.TrimSpace(in.Name),
		Username: strings.ToLower(strings.TrimSpace(in.Username)),
		Email:    trimmed(in.Email),
		Phone:    trimmed(in.Phone),
	}
	switch n := utf8.RuneCountInString(p.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 100:
		fields["name"] = "max"
	}
	if creating && !usernamePattern.MatchString(p.Username) {
		fields["username"] = "format"
	}
	if p.Email != nil {
		lower := strings.ToLower(*p.Email)
		p.Email = &lower
		if !ValidEmail(lower) {
			fields["email"] = "email"
		}
	}
	if p.Phone != nil && utf8.RuneCountInString(*p.Phone) > 32 {
		fields["phone"] = "max"
	}
	if !in.IsManager && (in.RoleID == nil || *in.RoleID == "") {
		fields["role_id"] = "required"
	}
	if len(fields) > 0 {
		return staffProfile{}, httpx.Invalid(fields)
	}
	return p, nil
}

// usernameTaken turns the username's unique violation into ErrUsernameTaken.
func usernameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "users_username_key" {
		return ErrUsernameTaken
	}
	return err
}

func staffFrom(r store.GetOutletUserRow) Staff {
	st := Staff{
		UserID: r.UserID, Name: r.Name, Username: r.Username, Email: r.Email, Phone: r.Phone,
		IsManager: r.IsManager, Active: r.Status == "active", MustChangePassword: r.MustChangePassword,
		LastLoginAt: r.LastLoginAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.RoleID != nil {
		st.Role = &StaffRole{RoleID: *r.RoleID, Name: deref(r.RoleName)}
	}
	return st
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullable is the Log Action's null for an empty role ID.
func nullable(roleID string) any {
	if roleID == "" {
		return nil
	}
	return roleID
}
