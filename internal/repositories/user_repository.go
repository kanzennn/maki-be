package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/user"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// UserRepository owns users, the accounts of everyone who signs in, and
// user_roles, the custom role each staff member holds.
type UserRepository struct {
	db *config.Database
}

// NewUserRepository returns the accounts' repository.
func NewUserRepository(db *config.Database) *UserRepository {
	return &UserRepository{db: db}
}

// ByUsername returns the account signing in with username: ErrNoAccount if
// there is none.
func (r *UserRepository) ByUsername(ctx context.Context, username string) (models.User, error) {
	return oneUser(store.New(r.db.Conn(ctx)).GetUserByUsername(ctx, username))
}

// ByID returns the account: ErrNoAccount if there is none.
func (r *UserRepository) ByID(ctx context.Context, userID string) (models.User, error) {
	return oneUser(store.New(r.db.Conn(ctx)).GetUserByID(ctx, userID))
}

// OwnerByLogin returns the Owner, if login is their username or their
// verified email: ErrNoAccount if it is neither.
func (r *UserRepository) OwnerByLogin(ctx context.Context, login string) (models.User, error) {
	return oneUser(store.New(r.db.Conn(ctx)).GetOwnerByLogin(ctx, login))
}

// OwnerExists reports whether the Owner account exists.
func (r *UserRepository) OwnerExists(ctx context.Context) (bool, error) {
	return store.New(r.db.Conn(ctx)).OwnerExists(ctx)
}

// Names returns the names of the accounts, by user ID, deleted ones
// included. An unknown ID is left out.
func (r *UserRepository) Names(ctx context.Context, userIDs []string) (map[string]string, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListUserNames(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("list user names: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.UserID] = row.Name
	}
	return out, nil
}

// ManagersOf returns the Managers of each of the outlets, by outlet ID.
func (r *UserRepository) ManagersOf(ctx context.Context, outletIDs []string) (map[string][]models.Manager, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListManagersOf(ctx, outletIDs)
	if err != nil {
		return nil, fmt.Errorf("list managers: %w", err)
	}
	out := make(map[string][]models.Manager, len(outletIDs))
	for _, row := range rows {
		outletID := deref(row.OutletID)
		out[outletID] = append(out[outletID], models.Manager{UserID: row.UserID, Name: row.Name})
	}
	return out, nil
}

// Permissions returns the permission codes of the staff member's role, as
// stored: codes the catalog no longer has included.
func (r *UserRepository) Permissions(ctx context.Context, userID string) ([]string, error) {
	codes, err := store.New(r.db.Conn(ctx)).GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load permissions of %s: %w", userID, err)
	}
	return codes, nil
}

// RecordLogin notes a successful sign-in.
func (r *UserRepository) RecordLogin(ctx context.Context, userID string) error {
	return store.New(r.db.Conn(ctx)).RecordLogin(ctx, userID)
}

// SetPasswordHash replaces the hash of an unchanged password.
func (r *UserRepository) SetPasswordHash(ctx context.Context, userID, hash string) error {
	return store.New(r.db.Conn(ctx)).SetPasswordHash(ctx, store.SetPasswordHashParams{UserID: userID, PasswordHash: hash})
}

// ChangePassword stores a password the user chose, which clears
// must_change_password.
func (r *UserRepository) ChangePassword(ctx context.Context, userID, hash string) error {
	return store.New(r.db.Conn(ctx)).ChangePassword(ctx, store.ChangePasswordParams{UserID: userID, PasswordHash: hash})
}

// SetTemporaryPassword stores a password someone else set, which the user
// must change at their next sign-in.
func (r *UserRepository) SetTemporaryPassword(ctx context.Context, userID, hash string) error {
	return store.New(r.db.Conn(ctx)).SetTemporaryPassword(ctx, store.SetTemporaryPasswordParams{UserID: userID, PasswordHash: hash})
}

// SetLocale sets the user's own language; nil follows their outlet's.
func (r *UserRepository) SetLocale(ctx context.Context, userID string, locale *string) error {
	return store.New(r.db.Conn(ctx)).SetLocale(ctx, store.SetLocaleParams{UserID: userID, Locale: locale})
}

// SetVerifiedEmail makes a confirmed address the account's email.
func (r *UserRepository) SetVerifiedEmail(ctx context.Context, userID, email string) error {
	return store.New(r.db.Conn(ctx)).SetVerifiedEmail(ctx, store.SetVerifiedEmailParams{UserID: userID, Email: &email})
}

// InsertOwner stores the Owner account. The database refuses a second
// Owner, even from a concurrent run: ErrOwnerExists.
func (r *UserRepository) InsertOwner(ctx context.Context, o models.NewOwner) error {
	err := store.New(r.db.Conn(ctx)).InsertOwner(ctx, store.InsertOwnerParams{
		UserID:       o.UserID,
		Name:         o.Name,
		Username:     o.Username,
		Email:        &o.Email,
		PasswordHash: o.PasswordHash,
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.ConstraintName {
		case "users_single_owner_key":
			return apperror.ErrOwnerExists
		case "users_username_key":
			return fmt.Errorf("the username %q is taken", o.Username)
		}
	}
	return err
}

// ListStaff returns a page of the outlet's users, by name, and how many
// match in all.
func (r *UserRepository) ListStaff(ctx context.Context, outletID string, f models.StaffFilter, page pagination.Page) ([]models.Staff, int, error) {
	status, roleID, search := optional(f.Status), optional(f.RoleID), optional(f.Search)
	if search != nil {
		*search = likeEscaper.Replace(*search)
	}
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountOutletUsers(ctx, store.CountOutletUsersParams{OutletID: outletID, Status: status, RoleID: roleID, Search: search})
	if err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	rows, err := q.ListOutletUsers(ctx, store.ListOutletUsersParams{
		OutletID: outletID, Status: status, RoleID: roleID, Search: search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	out := make([]models.Staff, len(rows))
	for i, row := range rows {
		out[i] = staffFrom(store.GetOutletUserRow(row))
	}
	return out, int(total), nil
}

// likeEscaper keeps LIKE's wildcards in a search literal.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Staff returns one of the outlet's users: ErrUserNotFound if it has no such
// user.
func (r *UserRepository) Staff(ctx context.Context, outletID, userID string) (models.Staff, error) {
	row, err := store.New(r.db.Conn(ctx)).GetOutletUser(ctx, store.GetOutletUserParams{UserID: userID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Staff{}, apperror.ErrUserNotFound
	}
	if err != nil {
		return models.Staff{}, fmt.Errorf("load user %s: %w", userID, err)
	}
	return staffFrom(row), nil
}

// LockStaff locks one of the outlet's users for a change: ErrUserNotFound if
// it has no such user.
func (r *UserRepository) LockStaff(ctx context.Context, outletID, userID string) (models.StaffLock, error) {
	row, err := store.New(r.db.Conn(ctx)).LockOutletUser(ctx, store.LockOutletUserParams{UserID: userID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StaffLock{}, apperror.ErrUserNotFound
	}
	if err != nil {
		return models.StaffLock{}, fmt.Errorf("lock user %s: %w", userID, err)
	}
	return models.StaffLock(row), nil
}

// InsertStaff stores a new account of the outlet: ErrUsernameTaken if
// someone has, or had, the username.
func (r *UserRepository) InsertStaff(ctx context.Context, s models.NewStaff) error {
	err := store.New(r.db.Conn(ctx)).InsertStaff(ctx, store.InsertStaffParams{
		UserID: s.UserID, OutletID: &s.OutletID, Name: s.Name, Username: s.Username,
		Email: s.Email, Phone: s.Phone, PasswordHash: s.PasswordHash, IsManager: s.IsManager,
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "users_username_key" {
		return apperror.ErrUsernameTaken
	}
	return err
}

// UpdateProfile stores an account's name, email and phone.
func (r *UserRepository) UpdateProfile(ctx context.Context, userID string, p models.StaffProfile) error {
	return store.New(r.db.Conn(ctx)).UpdateStaffProfile(ctx, store.UpdateStaffProfileParams{
		UserID: userID, Name: p.Name, Email: p.Email, Phone: p.Phone,
	})
}

// SetManager makes the user a Manager, or makes a Manager staff again.
func (r *UserRepository) SetManager(ctx context.Context, userID string, isManager bool) error {
	return store.New(r.db.Conn(ctx)).SetManager(ctx, store.SetManagerParams{UserID: userID, IsManager: isManager})
}

// SetStatus activates or deactivates the account: status is active or
// inactive.
func (r *UserRepository) SetStatus(ctx context.Context, userID, status string) error {
	return store.New(r.db.Conn(ctx)).SetUserStatus(ctx, store.SetUserStatusParams{UserID: userID, Status: status})
}

// Delete soft-deletes the account. Its username stays taken.
func (r *UserRepository) Delete(ctx context.Context, userID string) error {
	return store.New(r.db.Conn(ctx)).DeleteUser(ctx, userID)
}

// RoleOf returns the ID of the staff member's custom role, or "" when they
// have none.
func (r *UserRepository) RoleOf(ctx context.Context, userID string) (string, error) {
	return store.New(r.db.Conn(ctx)).GetUserRoleID(ctx, userID)
}

// AssignRole gives the staff member one of their outlet's roles.
func (r *UserRepository) AssignRole(ctx context.Context, userID, roleID, outletID string) error {
	return store.New(r.db.Conn(ctx)).AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: roleID, OutletID: outletID})
}

// ClearRole takes the staff member's role away.
func (r *UserRepository) ClearRole(ctx context.Context, userID string) error {
	return store.New(r.db.Conn(ctx)).ClearUserRole(ctx, userID)
}

// oneUser maps a single account's row.
func oneUser(row store.User, err error) (models.User, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, apperror.ErrNoAccount
	}
	if err != nil {
		return models.User{}, fmt.Errorf("load user: %w", err)
	}
	return models.User{
		UserID:             row.UserID,
		OutletID:           row.OutletID,
		Name:               row.Name,
		Username:           row.Username,
		Email:              row.Email,
		EmailVerifiedAt:    row.EmailVerifiedAt,
		Locale:             row.Locale,
		PasswordHash:       row.PasswordHash,
		MustChangePassword: row.MustChangePassword,
		IsOwner:            row.IsOwner,
		IsManager:          row.IsManager,
		Active:             row.Status == "active",
		LastLoginAt:        row.LastLoginAt,
	}, nil
}

func staffFrom(row store.GetOutletUserRow) models.Staff {
	s := models.Staff{
		UserID: row.UserID, Name: row.Name, Username: row.Username, Email: row.Email, Phone: row.Phone,
		IsManager: row.IsManager, Active: row.Status == "active", MustChangePassword: row.MustChangePassword,
		LastLoginAt: row.LastLoginAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.RoleID != nil {
		s.Role = &models.StaffRole{RoleID: *row.RoleID, Name: deref(row.RoleName)}
	}
	return s
}

// deref is the value of a nullable column, "" for NULL.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
