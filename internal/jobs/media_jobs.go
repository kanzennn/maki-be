package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/imaging"
	"github.com/kervzent/maki-be/internal/utils/outbox"
	"github.com/kervzent/maki-be/internal/utils/storage"
)

const (
	webpType = "image/webp"
	// A public photo's name changes with every upload, so the CDN may keep
	// it for good.
	publicCache = "public, max-age=31536000, immutable"
	// A profile photo is only ever read through a short-lived address.
	privateCache = "private, max-age=3600"

	// UploadMaxAge is how long an upload nobody claimed stays.
	UploadMaxAge  = 24 * time.Hour
	sweepInterval = time.Hour
)

var (
	menuSizes = []imaging.Size{{Width: 800, Height: 800}, {Width: 200, Height: 200}}
	userSizes = []imaging.Size{{Width: 400, Height: 400}}
)

// ImageConverter makes a WebP of a file for each size, or ErrRejected.
// imaging.Converter is the real one; cmd/worker alone hands it over.
type ImageConverter interface {
	Convert(data []byte, sizes []imaging.Size) ([][]byte, error)
}

// MediaJobs turns uploads into stored photos, and removes the objects no row
// points at any more. Each job may run again after being cut off anywhere:
// the objects it writes have the same names each time, and the row it sets
// is checked first.
type MediaJobs struct {
	media           *services.MediaService
	public, private storage.Storage
	images          ImageConverter
	logger          *slog.Logger
}

// NewMediaJobs returns the photos' handlers.
func NewMediaJobs(media *services.MediaService, public, private storage.Storage, images ImageConverter, logger *slog.Logger) *MediaJobs {
	return &MediaJobs{media: media, public: public, private: private, images: images, logger: logger}
}

// Register adds the handlers to w.
func (j *MediaJobs) Register(w *outbox.Worker) {
	w.Handle(models.JobProcessMenuImage, j.processMenuImage)
	w.Handle(models.JobProcessUserPhoto, j.processUserPhoto)
	w.Handle(models.JobDeleteObject, j.deleteObject)
}

func (j *MediaJobs) processMenuImage(ctx context.Context, job outbox.Job) error {
	var p models.JobMenuImage
	if err := decodeMedia(job, &p); err != nil {
		return err
	}
	imageKey := models.MenuImageKey(p.OutletID, p.MenuID, p.UploadKey)
	current, err := j.media.MenuImageKey(ctx, p.OutletID, p.MenuID)
	if errors.Is(err, apperror.ErrMenuNotFound) {
		return j.discard(ctx, job, p.UploadKey, "the menu is gone")
	}
	if err != nil {
		return err
	}
	if current != nil && *current == imageKey {
		return nil // an earlier attempt got this far, and queued the clean-up
	}
	photos, err := j.convert(ctx, job, p.UploadKey, menuSizes)
	if err != nil || photos == nil {
		return err
	}
	if err := j.public.Put(ctx, imageKey, webpType, publicCache, photos[0]); err != nil {
		return err
	}
	if err := j.public.Put(ctx, models.MenuThumbKey(imageKey), webpType, publicCache, photos[1]); err != nil {
		return err
	}
	err = j.media.AttachMenuImage(ctx, p.OutletID, p.MenuID, imageKey, p.UploadKey)
	if errors.Is(err, apperror.ErrMenuNotFound) {
		// Deleted a moment ago: nothing points at what was just written.
		_ = j.public.Delete(ctx, imageKey)
		_ = j.public.Delete(ctx, models.MenuThumbKey(imageKey))
		return j.discard(ctx, job, p.UploadKey, "the menu is gone")
	}
	return err
}

func (j *MediaJobs) processUserPhoto(ctx context.Context, job outbox.Job) error {
	var p models.JobUserPhoto
	if err := decodeMedia(job, &p); err != nil {
		return err
	}
	photoKey := models.UserPhotoKey(p.UserID, p.UploadKey)
	current, err := j.media.UserPhotoKey(ctx, p.UserID)
	if errors.Is(err, apperror.ErrUserNotFound) {
		return j.discard(ctx, job, p.UploadKey, "the user is gone")
	}
	if err != nil {
		return err
	}
	if current != nil && *current == photoKey {
		return nil
	}
	photos, err := j.convert(ctx, job, p.UploadKey, userSizes)
	if err != nil || photos == nil {
		return err
	}
	if err := j.private.Put(ctx, photoKey, webpType, privateCache, photos[0]); err != nil {
		return err
	}
	err = j.media.AttachUserPhoto(ctx, p.UserID, photoKey, p.UploadKey)
	if errors.Is(err, apperror.ErrUserNotFound) {
		_ = j.private.Delete(ctx, photoKey)
		return j.discard(ctx, job, p.UploadKey, "the user is gone")
	}
	return err
}

func (j *MediaJobs) deleteObject(ctx context.Context, job outbox.Job) error {
	var p models.JobObject
	if err := decodeMedia(job, &p); err != nil {
		return err
	}
	bucket := j.private
	if p.Bucket == models.BucketPublic {
		bucket = j.public
	}
	return bucket.Delete(ctx, p.Key)
}

// convert reads the upload and converts it. A file that can't become a photo
// is logged and deleted, and returns nothing and no error, as another attempt
// wouldn't change it: the row keeps the photo it has. Errors are for the
// storage or the converter failing, which another attempt might get past.
func (j *MediaJobs) convert(ctx context.Context, job outbox.Job, uploadKey string, sizes []imaging.Size) ([][]byte, error) {
	data, err := j.private.Get(ctx, uploadKey, models.MaxUploadBytes)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		j.logger.WarnContext(ctx, "upload is gone", slog.String("job", job.ID), slog.String("key", uploadKey))
		return nil, nil
	case errors.Is(err, storage.ErrTooLarge):
		return nil, j.discard(ctx, job, uploadKey, "over the size limit")
	case err != nil:
		return nil, err
	}
	if _, err := imaging.Inspect(data, models.MaxImageSide); err != nil {
		return nil, j.discard(ctx, job, uploadKey, err.Error())
	}
	photos, err := j.images.Convert(data, sizes)
	if errors.Is(err, imaging.ErrRejected) {
		return nil, j.discard(ctx, job, uploadKey, err.Error())
	}
	return photos, err
}

// discard deletes an upload that won't be used. If that fails too, the sweep
// gets it.
func (j *MediaJobs) discard(ctx context.Context, job outbox.Job, uploadKey, reason string) error {
	j.logger.WarnContext(ctx, "upload rejected", slog.String("job", job.ID), slog.String("key", uploadKey), slog.String("reason", reason))
	if err := j.private.Delete(ctx, uploadKey); err != nil {
		j.logger.WarnContext(ctx, "delete rejected upload", slog.String("key", uploadKey), slog.Any("error", err))
	}
	return nil
}

// Sweep deletes the uploads nobody claimed within UploadMaxAge, and returns
// how many.
func (j *MediaJobs) Sweep(ctx context.Context) (int, error) {
	keys, err := j.private.ListOlder(ctx, models.UploadPrefix, time.Now().Add(-UploadMaxAge))
	if err != nil {
		return 0, err
	}
	for i, key := range keys {
		if err := j.private.Delete(ctx, key); err != nil {
			return i, err
		}
	}
	return len(keys), nil
}

// RunSweeper sweeps at the start and every hour until ctx ends.
func (j *MediaJobs) RunSweeper(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		switch n, err := j.Sweep(ctx); {
		case err != nil && ctx.Err() == nil:
			j.logger.ErrorContext(ctx, "sweep uploads", slog.Any("error", err))
		case n > 0:
			j.logger.InfoContext(ctx, "swept unclaimed uploads", slog.Int("count", n))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func decodeMedia(job outbox.Job, dst any) error {
	if err := json.Unmarshal(job.Payload, dst); err != nil {
		return fmt.Errorf("decode %s payload: %w", job.Kind, err)
	}
	return nil
}
