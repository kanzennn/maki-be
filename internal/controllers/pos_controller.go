package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/cookie"
)

// PosController serves what the cashier starts from: its catalog, and the
// device it runs on.
type PosController struct {
	pos *services.PosService
	// secureCookies marks the device cookie Secure. Only development over
	// plain HTTP turns it off.
	secureCookies bool
}

// NewPosController returns the cashier's controller.
func NewPosController(pos *services.PosService, secureCookies bool) *PosController {
	return &PosController{pos: pos, secureCookies: secureCookies}
}

// Catalog handles GET /outlets/{oid}/pos/catalog.
func (ctl *PosController) Catalog(c *gin.Context) {
	cat, err := ctl.pos.Catalog(c.Request.Context(), c.Param("oid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewCatalogResponse(cat))
}

// Device handles GET /outlets/{oid}/pos/device, which the cashier sends as
// it starts. A browser's device cookie starts its 400 days again each time,
// so a device in use keeps it.
func (ctl *PosController) Device(c *gin.Context) {
	token := cookie.DeviceToken(c.Request)
	d, err := ctl.pos.Device(c.Request.Context(), c.Param("oid"), token)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if d.Device.Platform == models.PlatformWeb {
		http.SetCookie(c.Writer, cookie.NewDeviceCookie(token, ctl.secureCookies))
	}
	c.JSON(http.StatusOK, models.NewPosDeviceResponse(d))
}
