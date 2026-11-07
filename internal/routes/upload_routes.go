package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
)

// RegisterUploadRoutes registers uploads and the user's own photo. Anyone
// signed in may ask for an upload: it can't be read, it is gone in a day, and
// the routes that claim one check who may change what.
func RegisterUploadRoutes(protected gin.IRouter, ctl *controllers.UploadController) {
	protected.POST("/uploads/presign", ctl.Presign)
	protected.PUT("/me/photo", ctl.SetMyPhoto)
	protected.DELETE("/me/photo", ctl.RemoveMyPhoto)
}
