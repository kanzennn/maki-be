package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/outbox"
	"github.com/kervzent/maki-be/internal/utils/storage"
	"github.com/kervzent/maki-be/internal/validations"
)

// UploadTTL is how long a client has to start its upload.
const UploadTTL = 5 * time.Minute

// MediaService is the photos of menus and users: where a client uploads one,
// the jobs that make it a stored photo, and the row that points at it. The
// upload itself goes straight to the private bucket, and a row only ever
// points at a converted photo, so the old one shows until the worker is done.
//
// A nil MediaService, or one without buckets, is a deployment without
// storage: everything else works, and asking for an upload is
// ErrStorageUnavailable.
type MediaService struct {
	db      *config.Database
	menus   *repositories.MenuRepository
	users   *repositories.UserRepository
	audit   *AuditService
	private storage.Storage
}

// NewMediaService returns the photos' service. private is the bucket uploads
// land in; nil means no storage.
func NewMediaService(db *config.Database, menus *repositories.MenuRepository, users *repositories.UserRepository,
	audit *AuditService, private storage.Storage,
) *MediaService {
	return &MediaService{db: db, menus: menus, users: users, audit: audit, private: private}
}

func (s *MediaService) unavailable() bool {
	return s == nil || s.private == nil
}

// Presign returns a place to upload a file of the type and size to. Those
// are signed in; what the file really is, the worker finds out.
func (s *MediaService) Presign(ctx context.Context, contentType string, size int64) (models.UploadTicket, error) {
	if s.unavailable() {
		return models.UploadTicket{}, apperror.ErrStorageUnavailable
	}
	key := models.UploadPrefix + id.New()
	uploadURL, err := s.private.PresignPut(ctx, key, contentType, size, UploadTTL)
	if err != nil {
		return models.UploadTicket{}, err
	}
	return models.UploadTicket{UploadURL: uploadURL, UploadKey: key, ExpiresAt: time.Now().Add(UploadTTL)}, nil
}

// CheckUpload tells a client at once that the key it sent is no finished
// upload, rather than leaving its photo to never appear. It goes before the
// transaction, since it asks the storage over the network.
func (s *MediaService) CheckUpload(ctx context.Context, key string) error {
	if s.unavailable() {
		return apperror.ErrStorageUnavailable
	}
	if !validations.ValidUploadKey(key) {
		return apperror.Invalid(map[string]string{"upload_key": "format"})
	}
	if _, err := s.private.Head(ctx, key); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return apperror.ErrUploadNotFound
		}
		return err
	}
	return nil
}

// QueueMenuImage asks the worker, in the transaction ctx carries, to make the
// upload the menu's photo.
func (s *MediaService) QueueMenuImage(ctx context.Context, outletID, menuID, uploadKey string) error {
	return s.enqueue(ctx, models.JobProcessMenuImage, models.JobMenuImage{OutletID: outletID, MenuID: menuID, UploadKey: uploadKey})
}

// QueueUserPhoto asks the worker, in the transaction ctx carries, to make the
// upload the user's photo.
func (s *MediaService) QueueUserPhoto(ctx context.Context, userID, uploadKey string) error {
	return s.enqueue(ctx, models.JobProcessUserPhoto, models.JobUserPhoto{UserID: userID, UploadKey: uploadKey})
}

// RemoveMenuImage takes the menu's photo away, in the transaction ctx
// carries, and reports whether it had one.
func (s *MediaService) RemoveMenuImage(ctx context.Context, outletID, menuID string) (bool, error) {
	old, err := s.menus.LockImage(ctx, outletID, menuID)
	if err != nil || old == nil {
		return false, err
	}
	if err := s.menus.SetImage(ctx, menuID, nil); err != nil {
		return false, err
	}
	return true, s.deleteMenuImage(ctx, *old)
}

// RemoveUserPhoto takes the user's photo away, in the transaction ctx
// carries, and reports whether they had one.
func (s *MediaService) RemoveUserPhoto(ctx context.Context, userID string) (bool, error) {
	old, err := s.users.LockPhoto(ctx, userID)
	if err != nil || old == nil {
		return false, err
	}
	if err := s.users.SetPhoto(ctx, userID, nil); err != nil {
		return false, err
	}
	return true, s.deleteObject(ctx, models.BucketPrivate, *old)
}

// SetOwnPhoto is PUT /me/photo: anyone may change their own photo.
func (s *MediaService) SetOwnPhoto(ctx context.Context, p models.Principal, ip, uploadKey string) error {
	if err := s.CheckUpload(ctx, uploadKey); err != nil {
		return err
	}
	return s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.QueueUserPhoto(ctx, p.UserID, uploadKey); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "user.photo_changed", UserID: p.UserID, OutletID: p.OutletID,
			TargetType: "user", TargetID: p.UserID, IP: ip,
		})
	})
}

// RemoveOwnPhoto is DELETE /me/photo.
func (s *MediaService) RemoveOwnPhoto(ctx context.Context, p models.Principal, ip string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		removed, err := s.RemoveUserPhoto(ctx, p.UserID)
		if err != nil || !removed {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "user.photo_removed", UserID: p.UserID, OutletID: p.OutletID,
			TargetType: "user", TargetID: p.UserID, IP: ip,
		})
	})
}

// MenuImageKey is the object the menu's photo is, nil without one, for the
// worker to see whether a job already finished: ErrMenuNotFound if the menu
// is gone.
func (s *MediaService) MenuImageKey(ctx context.Context, outletID, menuID string) (*string, error) {
	return s.menus.ImageKey(ctx, outletID, menuID)
}

// UserPhotoKey is MenuImageKey for a user: ErrUserNotFound if they're gone.
func (s *MediaService) UserPhotoKey(ctx context.Context, userID string) (*string, error) {
	return s.users.PhotoKey(ctx, userID)
}

// AttachMenuImage points the menu at its converted photo and, in the same
// transaction, asks for the upload and the photo it replaces to be deleted.
func (s *MediaService) AttachMenuImage(ctx context.Context, outletID, menuID, imageKey, uploadKey string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		old, err := s.menus.LockImage(ctx, outletID, menuID)
		if err != nil {
			return err
		}
		if err := s.menus.SetImage(ctx, menuID, &imageKey); err != nil {
			return err
		}
		if old != nil && *old != imageKey {
			if err := s.deleteMenuImage(ctx, *old); err != nil {
				return err
			}
		}
		return s.deleteObject(ctx, models.BucketPrivate, uploadKey)
	})
}

// AttachUserPhoto is AttachMenuImage for a user.
func (s *MediaService) AttachUserPhoto(ctx context.Context, userID, photoKey, uploadKey string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		old, err := s.users.LockPhoto(ctx, userID)
		if err != nil {
			return err
		}
		if err := s.users.SetPhoto(ctx, userID, &photoKey); err != nil {
			return err
		}
		if old != nil && *old != photoKey {
			if err := s.deleteObject(ctx, models.BucketPrivate, *old); err != nil {
				return err
			}
		}
		return s.deleteObject(ctx, models.BucketPrivate, uploadKey)
	})
}

func (s *MediaService) deleteMenuImage(ctx context.Context, imageKey string) error {
	if err := s.deleteObject(ctx, models.BucketPublic, imageKey); err != nil {
		return err
	}
	return s.deleteObject(ctx, models.BucketPublic, models.MenuThumbKey(imageKey))
}

func (s *MediaService) deleteObject(ctx context.Context, bucket, key string) error {
	return s.enqueue(ctx, models.JobDeleteObject, models.JobObject{Bucket: bucket, Key: key})
}

func (s *MediaService) enqueue(ctx context.Context, kind string, payload any) error {
	if _, err := outbox.Enqueue(ctx, s.db.Conn(ctx), kind, payload); err != nil {
		return fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return nil
}
