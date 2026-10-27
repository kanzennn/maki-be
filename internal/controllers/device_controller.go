package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/cookie"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// DeviceController serves an outlet's cashier devices.
type DeviceController struct {
	devices *services.DeviceService
	// secureCookies marks the device cookie Secure. Only development over
	// plain HTTP turns it off.
	secureCookies bool
}

// NewDeviceController returns the devices' controller.
func NewDeviceController(devices *services.DeviceService, secureCookies bool) *DeviceController {
	return &DeviceController{devices: devices, secureCookies: secureCookies}
}

// Activate handles POST /outlets/{oid}/devices, sent from the device itself.
// A browser gets its token as a cookie; the Android app, in the body.
func (ctl *DeviceController) Activate(c *gin.Context) {
	var req validations.ActivateDeviceRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	d, token, err := ctl.devices.Activate(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"),
		cookie.DeviceToken(c.Request), req.Name, req.Platform)
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := models.ActivateDeviceResponse{Device: models.NewDeviceResponse(d)}
	if d.Platform == models.PlatformWeb {
		http.SetCookie(c.Writer, cookie.NewDeviceCookie(token, ctl.secureCookies))
	} else {
		resp.Token = &token
	}
	c.JSON(http.StatusCreated, resp)
}

// List handles GET /outlets/{oid}/devices?page=&page_size=.
func (ctl *DeviceController) List(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.devices.List(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.DeviceResponse, len(items))
	for i, item := range items {
		out[i] = models.NewDeviceResponse(item)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// Revoke handles DELETE /outlets/{oid}/devices/{did}.
func (ctl *DeviceController) Revoke(c *gin.Context) {
	if err := ctl.devices.Revoke(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("did")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
