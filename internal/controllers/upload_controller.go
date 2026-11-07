package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/validations"
)

// UploadController serves uploads, and the signed-in user's own photo.
type UploadController struct {
	media *services.MediaService
}

// NewUploadController returns the uploads' controller.
func NewUploadController(media *services.MediaService) *UploadController {
	return &UploadController{media: media}
}

// Presign handles POST /uploads/presign.
func (ctl *UploadController) Presign(c *gin.Context) {
	var req validations.PresignUploadRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	ticket, err := ctl.media.Presign(c.Request.Context(), req.ContentType, req.Size)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewUploadTicketResponse(ticket))
}

// SetMyPhoto handles PUT /me/photo: the worker takes it from here.
func (ctl *UploadController) SetMyPhoto(c *gin.Context) {
	var req validations.SetPhotoRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.media.SetOwnPhoto(c.Request.Context(), principal(c), c.ClientIP(), req.UploadKey); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusAccepted)
}

// RemoveMyPhoto handles DELETE /me/photo.
func (ctl *UploadController) RemoveMyPhoto(c *gin.Context) {
	if err := ctl.media.RemoveOwnPhoto(c.Request.Context(), principal(c), c.ClientIP()); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
