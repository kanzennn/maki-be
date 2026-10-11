package user

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
)

// Handler serves the permission catalog and an outlet's roles and users.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler returns the user module's handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Routes registers the endpoints: the catalog on protected, and the
// outlet's roles and users on scoped, the group under /outlets/:oid that
// OutletScope guards. Each route names the permission it needs.
func (h *Handler) Routes(protected, scoped *gin.RouterGroup) {
	protected.GET("/permissions", h.Permissions)

	view, manage := middleware.RequirePermission("role.view"), middleware.RequirePermission("role.manage")
	// Whoever sees the outlet's users sees their roles' names, and whoever
	// manages them picks one: the list opens with user.view too.
	scoped.GET("/roles", middleware.RequireAnyPermission("role.view", "user.view"), h.ListRoles)
	scoped.POST("/roles", manage, h.CreateRole)
	scoped.GET("/roles/:rid", view, h.GetRole)
	scoped.PUT("/roles/:rid", manage, h.UpdateRole)
	scoped.DELETE("/roles/:rid", manage, h.DeleteRole)

	h.staffRoutes(scoped)
}

type permissionGroupResponse struct {
	Module      string   `json:"module"`
	Permissions []string `json:"permissions"`
}

type permissionsResponse struct {
	Groups []permissionGroupResponse `json:"groups"`
}

// Permissions handles GET /permissions: every permission, grouped by module.
func (h *Handler) Permissions(c *gin.Context) {
	groups := Catalog()
	out := permissionsResponse{Groups: make([]permissionGroupResponse, len(groups))}
	for i, g := range groups {
		out.Groups[i] = permissionGroupResponse(g)
	}
	c.JSON(http.StatusOK, out)
}

// roleRequest is the body of POST and PUT. The caps only bound what the
// server reads; checkRole applies the real rules.
type roleRequest struct {
	Name        string   `json:"name" binding:"required,max=200"`
	Description *string  `json:"description" binding:"omitempty,max=800"`
	Permissions []string `json:"permissions" binding:"max=100"`
}

type roleResponse struct {
	RoleID      string    `json:"role_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Permissions []string  `json:"permissions"`
	UserCount   int       `json:"user_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type roleUserResponse struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

type roleDetailResponse struct {
	roleResponse
	Users []roleUserResponse `json:"users"`
}

func toRole(r Role) roleResponse {
	return roleResponse{
		RoleID: r.RoleID, Name: r.Name, Description: r.Description, Permissions: r.Permissions,
		UserCount: r.UserCount, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

func toRoleDetail(r Role, users []RoleUser) roleDetailResponse {
	out := roleDetailResponse{roleResponse: toRole(r), Users: make([]roleUserResponse, len(users))}
	for i, u := range users {
		out.Users[i] = roleUserResponse(u)
	}
	return out
}

// ListRoles handles GET /outlets/{oid}/roles.
func (h *Handler) ListRoles(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	roles, total, err := h.svc.ListRoles(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]roleResponse, len(roles))
	for i, r := range roles {
		out[i] = toRole(r)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// GetRole handles GET /outlets/{oid}/roles/{rid}.
func (h *Handler) GetRole(c *gin.Context) {
	r, users, err := h.svc.Role(c.Request.Context(), c.Param("oid"), c.Param("rid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toRoleDetail(r, users))
}

// CreateRole handles POST /outlets/{oid}/roles.
func (h *Handler) CreateRole(c *gin.Context) {
	var req roleRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	r, err := h.svc.CreateRole(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), RoleInput(req))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toRoleDetail(r, nil))
}

// UpdateRole handles PUT /outlets/{oid}/roles/{rid}.
func (h *Handler) UpdateRole(c *gin.Context) {
	var req roleRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	oid, rid := c.Param("oid"), c.Param("rid")
	if _, err := h.svc.UpdateRole(c.Request.Context(), principal(c), c.ClientIP(), oid, rid, RoleInput(req)); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	h.GetRole(c)
}

// DeleteRole handles DELETE /outlets/{oid}/roles/{rid}.
func (h *Handler) DeleteRole(c *gin.Context) {
	if err := h.svc.DeleteRole(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("rid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// principal is the signed-in user, whom Authenticate has put on every route
// here.
func principal(c *gin.Context) authn.Principal {
	p, _ := authn.PrincipalFrom(c.Request.Context())
	return p
}
