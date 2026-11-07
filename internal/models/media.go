package models

import (
	"strings"
	"time"
)

// What an upload may be.
const (
	MaxUploadBytes = 2 << 20 // 2 MB
	MaxImageSide   = 8000    // pixels, checked before the image is opened
	UploadPrefix   = "uploads/"
)

// The outbox jobs that turn an upload into a stored photo, and that remove
// objects no row points at any more.
const (
	JobProcessMenuImage = "media.process_menu_image"
	JobProcessUserPhoto = "media.process_user_photo"
	JobDeleteObject     = "media.delete_object"
)

// The buckets a JobObject names.
const (
	BucketPublic  = "public"
	BucketPrivate = "private"
)

// JobMenuImage is the payload of the job that gives a menu its uploaded photo.
type JobMenuImage struct {
	OutletID  string `json:"outlet_id"`
	MenuID    string `json:"menu_id"`
	UploadKey string `json:"upload_key"`
}

// JobUserPhoto is the payload of the job that gives a user their uploaded
// photo.
type JobUserPhoto struct {
	UserID    string `json:"user_id"`
	UploadKey string `json:"upload_key"`
}

// JobObject is the payload of the job that removes one object.
type JobObject struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
}

// MenuImageKey is where a menu's photo from an upload goes in the public
// bucket. The name comes from the upload, so another attempt at the same
// job writes the same object, and each new upload gets a new name the CDN
// can cache for good.
func MenuImageKey(outletID, menuID, uploadKey string) string {
	return "outlets/" + outletID + "/menus/" + menuID + "/" + strings.TrimPrefix(uploadKey, UploadPrefix) + ".webp"
}

// MenuThumbKey is where the small version of a menu's photo lies, beside it.
func MenuThumbKey(imageKey string) string {
	return strings.TrimSuffix(imageKey, ".webp") + "_thumb.webp"
}

// UserPhotoKey is where a user's photo from an upload goes in the private
// bucket.
func UserPhotoKey(userID, uploadKey string) string {
	return "users/" + userID + "/" + strings.TrimPrefix(uploadKey, UploadPrefix) + ".webp"
}

// UploadTicket is a place to upload one file to.
type UploadTicket struct {
	UploadURL string
	UploadKey string
	ExpiresAt time.Time
}

// UploadTicketResponse is POST /uploads/presign.
type UploadTicketResponse struct {
	UploadURL string    `json:"upload_url"`
	UploadKey string    `json:"upload_key"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewUploadTicketResponse returns the ticket's response, its time in UTC.
func NewUploadTicketResponse(t UploadTicket) UploadTicketResponse {
	return UploadTicketResponse{UploadURL: t.UploadURL, UploadKey: t.UploadKey, ExpiresAt: t.ExpiresAt.UTC()}
}
