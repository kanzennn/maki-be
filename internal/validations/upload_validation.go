package validations

import (
	"strings"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/id"
)

// PresignUploadRequest is the body of POST /uploads/presign. The size says
// what the signature will insist on, and the worker checks the file itself.
type PresignUploadRequest struct {
	Purpose     string `json:"purpose" binding:"required,oneof=menu_image user_photo"`
	ContentType string `json:"content_type" binding:"required,oneof=image/jpeg image/png image/webp"`
	Size        int64  `json:"size" binding:"required,gt=0,lte=2097152"`
}

// SetPhotoRequest is the body of PUT /me/photo.
type SetPhotoRequest struct {
	UploadKey string `json:"upload_key" binding:"required,max=64"`
}

// ValidUploadKey reports whether key is one this API hands out: nothing but
// uploads/ may be claimed, since a claimed object ends up deleted.
func ValidUploadKey(key string) bool {
	name, ok := strings.CutPrefix(key, models.UploadPrefix)
	return ok && id.Valid(name)
}
