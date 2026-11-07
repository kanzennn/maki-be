package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// LogController serves the Log Action.
type LogController struct {
	logs *services.LogService
}

// NewLogController returns the Log Action's controller.
func NewLogController(logs *services.LogService) *LogController {
	return &LogController{logs: logs}
}

// ListOwner handles GET /owner/logs?from=&to=&action=&outlet_ids=&self=&page=&page_size=:
// every outlet's, and the Owner's own.
func (ctl *LogController) ListOwner(c *gin.Context) {
	f, err := validations.BindLogFilter(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if f.OutletIDs, err = validations.BindOutletIDs(c); err != nil {
		_ = c.Error(err)
		return
	}
	self, err := validations.BindSelf(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if self {
		f.UserID = principal(c).UserID
	}
	ctl.list(c, f)
}

// ListOutlet handles GET /outlets/{oid}/logs?from=&to=&action=&user_id=&page=&page_size=:
// that outlet's alone.
func (ctl *LogController) ListOutlet(c *gin.Context) {
	f, err := validations.BindLogFilter(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	f.OutletIDs = []string{c.Param("oid")}
	f.UserID = c.Query("user_id")
	ctl.list(c, f)
}

func (ctl *LogController) list(c *gin.Context, f models.LogFilter) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	logs, total, err := ctl.logs.List(c.Request.Context(), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, response.NewList(models.NewActionLogResponses(logs), page, total))
}
