package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// RoleController serves the permission catalog and an outlet's roles.
type RoleController struct {
	roles *services.RoleService
}

// NewRoleController returns the roles' controller.
func NewRoleController(roles *services.RoleService) *RoleController {
	return &RoleController{roles: roles}
}

// Permissions handles GET /permissions: every permission, grouped by module.
func (ctl *RoleController) Permissions(c *gin.Context) {
	c.JSON(http.StatusOK, models.NewPermissionsResponse(ctl.roles.Catalog()))
}

// ListRoles handles GET /outlets/{oid}/roles.
func (ctl *RoleController) ListRoles(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	roles, total, err := ctl.roles.ListRoles(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.RoleResponse, len(roles))
	for i, r := range roles {
		out[i] = models.NewRoleResponse(r)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// GetRole handles GET /outlets/{oid}/roles/{rid}.
func (ctl *RoleController) GetRole(c *gin.Context) {
	r, users, err := ctl.roles.Role(c.Request.Context(), c.Param("oid"), c.Param("rid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewRoleDetailResponse(r, users))
}

// CreateRole handles POST /outlets/{oid}/roles.
func (ctl *RoleController) CreateRole(c *gin.Context) {
	var req validations.RoleRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	r, err := ctl.roles.CreateRole(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), models.RoleInput(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewRoleDetailResponse(r, nil))
}

// UpdateRole handles PUT /outlets/{oid}/roles/{rid}. It answers with the
// role as GetRole does, its users included.
func (ctl *RoleController) UpdateRole(c *gin.Context) {
	var req validations.RoleRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	oid, rid := c.Param("oid"), c.Param("rid")
	if _, err := ctl.roles.UpdateRole(c.Request.Context(), principal(c), c.ClientIP(), oid, rid, models.RoleInput(req)); err != nil {
		_ = c.Error(err)
		return
	}
	ctl.GetRole(c)
}

// DeleteRole handles DELETE /outlets/{oid}/roles/{rid}.
func (ctl *RoleController) DeleteRole(c *gin.Context) {
	if err := ctl.roles.DeleteRole(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("rid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
