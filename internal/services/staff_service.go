package services

import (
	"context"
	"errors"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/utils/password"
	"github.com/kervzent/maki-be/internal/validations"
)

// StaffService is each outlet's Manager and staff accounts, as the Owner and
// the outlet's people manage them. Who manages whom, from the spec's access
// matrix:
//
//   - the Owner manages Manager accounts, and promotes staff to Manager;
//   - Managers, and staff whose role has user.manage, manage staff;
//   - nobody manages their own account here: their settings go through /me;
//   - and nobody hands out more than they hold: they may only manage staff
//     whose permissions they all hold, and give roles they all hold.
//     Otherwise a supervisor could reset a stronger user's password and sign
//     in as them.
type StaffService struct {
	db       *config.Database
	users    *repositories.UserRepository
	roles    *repositories.RoleRepository
	hasher   *password.Hasher
	audit    *AuditService
	sessions *repositories.SessionRepository
}

// NewStaffService returns the staff accounts' service. hasher hashes the
// passwords it sets; sessions are the ones it ends when an account changes
// wholesale.
func NewStaffService(db *config.Database, users *repositories.UserRepository, roles *repositories.RoleRepository,
	hasher *password.Hasher, audit *AuditService, sessions *repositories.SessionRepository,
) *StaffService {
	return &StaffService{db: db, users: users, roles: roles, hasher: hasher, audit: audit, sessions: sessions}
}

// ListStaff returns a page of the outlet's users, by name, and how many
// match in all.
func (s *StaffService) ListStaff(ctx context.Context, outletID string, f models.StaffFilter, page pagination.Page) ([]models.Staff, int, error) {
	return s.users.ListStaff(ctx, outletID, f, page)
}

// Staff returns one of the outlet's users.
func (s *StaffService) Staff(ctx context.Context, outletID, userID string) (models.Staff, error) {
	return s.users.Staff(ctx, outletID, userID)
}

// CreateStaff adds an account to the outlet: a Manager, by the Owner, or a
// staff member with a role, by everyone else who may. The password is
// temporary: the user must change it at the first sign-in.
func (s *StaffService) CreateStaff(ctx context.Context, p models.Principal, ip, outletID string, in models.StaffInput) (models.Staff, error) {
	profile, err := validations.CheckStaff(in, true)
	if err != nil {
		return models.Staff{}, err
	}
	switch {
	case in.IsManager && !p.IsOwner:
		return models.Staff{}, apperror.ErrManagerAccount
	case !in.IsManager && p.IsOwner:
		return models.Staff{}, apperror.ErrOwnerManagesManagers
	}
	if err := password.Check(in.Password, profile.Username); err != nil {
		return models.Staff{}, err
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return models.Staff{}, err
	}
	userID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if !in.IsManager {
			if err := s.giveable(ctx, p, outletID, *in.RoleID); err != nil {
				return err
			}
		}
		if err := s.users.InsertStaff(ctx, models.NewStaff{
			UserID: userID, OutletID: outletID, Name: profile.Name, Username: profile.Username,
			Email: profile.Email, Phone: profile.Phone, PasswordHash: hash, IsManager: in.IsManager,
		}); err != nil {
			return err
		}
		after := profile.Snapshot()
		after["is_manager"] = in.IsManager
		if !in.IsManager {
			if err := s.users.AssignRole(ctx, userID, *in.RoleID, outletID); err != nil {
				return err
			}
			after["role_id"] = *in.RoleID
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "user.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "user", TargetID: userID, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Staff{}, err
	}
	return s.Staff(ctx, outletID, userID)
}

// UpdateStaff changes an account's name, email and phone, and its role or
// rank. Only the Owner changes a Manager account, or makes a staff member a
// Manager; that, and making a Manager staff again, ends the user's sessions.
func (s *StaffService) UpdateStaff(ctx context.Context, p models.Principal, ip, outletID, userID string, in models.StaffInput) (models.Staff, error) {
	next, err := validations.CheckStaff(in, false)
	if err != nil {
		return models.Staff{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		target, err := s.users.LockStaff(ctx, outletID, userID)
		if err != nil {
			return err
		}
		switch {
		case target.UserID == p.UserID:
			return apperror.ErrOwnAccount
		case p.IsOwner && !target.IsManager && !in.IsManager:
			// Promoting is the Owner's one move on a staff account.
			return apperror.ErrOwnerManagesManagers
		case !p.IsOwner && (target.IsManager || in.IsManager):
			return apperror.ErrManagerAccount
		case !p.IsOwner:
			if err := s.holdsAllOf(ctx, p, target.UserID); err != nil {
				return err
			}
		}

		if err := s.updateProfile(ctx, p, ip, outletID, target, next); err != nil {
			return err
		}
		prevRole, err := s.users.RoleOf(ctx, target.UserID)
		if err != nil {
			return err
		}
		switch {
		case in.IsManager && !target.IsManager:
			return s.changeRank(ctx, p, ip, outletID, target.UserID, true, prevRole, "")
		case !in.IsManager && target.IsManager:
			// A Manager held every permission, so any role takes some away.
			if err := s.roleOfOutlet(ctx, outletID, *in.RoleID); err != nil {
				return err
			}
			return s.changeRank(ctx, p, ip, outletID, target.UserID, false, "", *in.RoleID)
		case !in.IsManager && *in.RoleID != prevRole:
			if err := s.giveable(ctx, p, outletID, *in.RoleID); err != nil {
				return err
			}
			if err := s.users.ClearRole(ctx, target.UserID); err != nil {
				return err
			}
			if err := s.users.AssignRole(ctx, target.UserID, *in.RoleID, outletID); err != nil {
				return err
			}
			return s.audit.Record(ctx, models.AuditEntry{
				Action: "user.role_changed", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: target.UserID,
				Before: map[string]any{"role_id": nullable(prevRole)}, After: map[string]any{"role_id": *in.RoleID}, IP: ip,
			})
		}
		return nil
	})
	if err != nil {
		return models.Staff{}, err
	}
	return s.Staff(ctx, outletID, userID)
}

// updateProfile stores changed name, email and phone, and records them.
func (s *StaffService) updateProfile(ctx context.Context, p models.Principal, ip, outletID string, target models.StaffLock, next models.StaffProfile) error {
	prev := models.StaffProfile{Name: target.Name, Username: target.Username, Email: target.Email, Phone: target.Phone}
	before, after := changedFields(prev.Snapshot(), next.Snapshot())
	if len(after) == 0 {
		return nil
	}
	if err := s.users.UpdateProfile(ctx, target.UserID, next); err != nil {
		return err
	}
	return s.audit.Record(ctx, models.AuditEntry{
		Action: "user.updated", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: target.UserID,
		Before: before, After: after, IP: ip,
	})
}

// changeRank makes the user a Manager (toManager) or staff with roleID,
// and ends their sessions: what they may do changes wholesale.
func (s *StaffService) changeRank(ctx context.Context, p models.Principal, ip, outletID, userID string, toManager bool, prevRole, roleID string) error {
	if err := s.users.ClearRole(ctx, userID); err != nil {
		return err
	}
	if !toManager {
		if err := s.users.AssignRole(ctx, userID, roleID, outletID); err != nil {
			return err
		}
	}
	if err := s.users.SetManager(ctx, userID, toManager); err != nil {
		return err
	}
	if err := s.sessions.RevokeUserSessions(ctx, userID, ""); err != nil {
		return err
	}
	action, before, after := "user.promoted", map[string]any{"role_id": nullable(prevRole)}, map[string]any{"is_manager": true}
	if !toManager {
		action, before, after = "user.demoted", map[string]any{"is_manager": true}, map[string]any{"role_id": roleID}
	}
	return s.audit.Record(ctx, models.AuditEntry{
		Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID,
		Before: before, After: after, IP: ip,
	})
}

// SetStaffStatus activates or deactivates an account. Deactivating ends
// its sessions at once.
func (s *StaffService) SetStaffStatus(ctx context.Context, p models.Principal, ip, outletID, userID string, active bool) (models.Staff, error) {
	status, action := "inactive", "user.deactivated"
	if active {
		status, action = "active", "user.activated"
	}
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		target, err := s.lockManaged(ctx, p, outletID, userID)
		if err != nil || target.Status == status {
			return err
		}
		if err := s.users.SetStatus(ctx, userID, status); err != nil {
			return err
		}
		if !active {
			if err := s.sessions.RevokeUserSessions(ctx, userID, ""); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID,
			Before: map[string]any{"status": target.Status}, After: map[string]any{"status": status}, IP: ip,
		})
	})
	if err != nil {
		return models.Staff{}, err
	}
	return s.Staff(ctx, outletID, userID)
}

// ResetStaffPassword gives an account a temporary password, which its user
// must change at their next sign-in. Their sessions end at once.
func (s *StaffService) ResetStaffPassword(ctx context.Context, p models.Principal, ip, outletID, userID, pw string) error {
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
	return s.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := s.lockManaged(ctx, p, outletID, userID); err != nil {
			return err
		}
		if err := s.users.SetTemporaryPassword(ctx, userID, hash); err != nil {
			return err
		}
		if err := s.sessions.RevokeUserSessions(ctx, userID, ""); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "user.password_reset", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID, IP: ip,
		})
	})
}

// DeleteStaff soft-deletes an account and ends its sessions. Its username
// stays taken, and its data stays in the name it had.
func (s *StaffService) DeleteStaff(ctx context.Context, p models.Principal, ip, outletID, userID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		target, err := s.lockManaged(ctx, p, outletID, userID)
		if err != nil {
			return err
		}
		if err := s.users.Delete(ctx, userID); err != nil {
			return err
		}
		if err := s.sessions.RevokeUserSessions(ctx, userID, ""); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "user.deleted", UserID: p.UserID, OutletID: outletID, TargetType: "user", TargetID: userID,
			Before: map[string]any{"name": target.Name, "username": target.Username, "is_manager": target.IsManager}, IP: ip,
		})
	})
}

// lockManaged locks an account p may manage: not their own, a Manager's
// only for the Owner, a staff member's only for everyone else, and then
// only one whose permissions p all holds.
func (s *StaffService) lockManaged(ctx context.Context, p models.Principal, outletID, userID string) (models.StaffLock, error) {
	target, err := s.users.LockStaff(ctx, outletID, userID)
	if err != nil {
		return target, err
	}
	switch {
	case target.UserID == p.UserID:
		return target, apperror.ErrOwnAccount
	case p.IsOwner && !target.IsManager:
		return target, apperror.ErrOwnerManagesManagers
	case !p.IsOwner && target.IsManager:
		return target, apperror.ErrManagerAccount
	case !p.IsOwner:
		return target, s.holdsAllOf(ctx, p, userID)
	}
	return target, nil
}

// holdsAllOf refuses when the staff member holds a permission p lacks.
func (s *StaffService) holdsAllOf(ctx context.Context, p models.Principal, userID string) error {
	codes, err := s.users.Permissions(ctx, userID)
	if err != nil {
		return err
	}
	return grantable(p, codes)
}

// giveable checks that the role is the outlet's and holds nothing p lacks.
// It locks the role, so it can't be deleted while it is being given.
func (s *StaffService) giveable(ctx context.Context, p models.Principal, outletID, roleID string) error {
	role, err := s.roles.Lock(ctx, outletID, roleID)
	if errors.Is(err, apperror.ErrRoleNotFound) {
		return apperror.Invalid(map[string]string{"role_id": "exists"})
	}
	if err != nil {
		return err
	}
	return grantable(p, role.Permissions)
}

// roleOfOutlet checks that the role is the outlet's, and locks it.
func (s *StaffService) roleOfOutlet(ctx context.Context, outletID, roleID string) error {
	_, err := s.roles.Lock(ctx, outletID, roleID)
	if errors.Is(err, apperror.ErrRoleNotFound) {
		return apperror.Invalid(map[string]string{"role_id": "exists"})
	}
	return err
}

// nullable is the Log Action's null for an empty role ID.
func nullable(roleID string) any {
	if roleID == "" {
		return nil
	}
	return roleID
}
