package user

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
)

// staffRoutes registers an outlet's user endpoints on scoped.
func (h *Handler) staffRoutes(scoped *gin.RouterGroup) {
	view, manage := middleware.RequirePermission("user.view"), middleware.RequirePermission("user.manage")
	scoped.GET("/users", view, h.ListStaff)
	scoped.POST("/users", manage, h.CreateStaff)
	scoped.GET("/users/:uid", view, h.GetStaff)
	scoped.PUT("/users/:uid", manage, h.UpdateStaff)
	scoped.PATCH("/users/:uid/status", manage, h.SetStaffStatus)
	scoped.POST("/users/:uid/reset-password", manage, h.ResetStaffPassword)
	scoped.DELETE("/users/:uid", manage, h.DeleteStaff)
}

// The caps on these only bound what the server reads; checkStaff and
// password.Check apply the real rules.
type createStaffRequest struct {
	Name      string  `json:"name" binding:"required,max=400"`
	Username  string  `json:"username" binding:"required,max=64"`
	Email     *string `json:"email" binding:"omitempty,max=254"`
	Phone     *string `json:"phone" binding:"omitempty,max=128"`
	Password  string  `json:"password" binding:"required,max=1024"`
	RoleID    *string `json:"role_id" binding:"omitempty,max=32"`
	IsManager bool    `json:"is_manager"`
}

type updateStaffRequest struct {
	Name      string  `json:"name" binding:"required,max=400"`
	Email     *string `json:"email" binding:"omitempty,max=254"`
	Phone     *string `json:"phone" binding:"omitempty,max=128"`
	RoleID    *string `json:"role_id" binding:"omitempty,max=32"`
	IsManager bool    `json:"is_manager"`
}

type statusRequest struct {
	Status string `json:"status" binding:"required,oneof=active inactive"`
}

type resetPasswordRequest struct {
	Password string `json:"password" binding:"required,max=1024"`
}

type staffRoleResponse struct {
	RoleID string `json:"role_id"`
	Name   string `json:"name"`
}

type staffResponse struct {
	UserID             string             `json:"user_id"`
	Name               string             `json:"name"`
	Username           string             `json:"username"`
	Email              *string            `json:"email"`
	Phone              *string            `json:"phone"`
	IsManager          bool               `json:"is_manager"`
	Role               *staffRoleResponse `json:"role"`
	Status             string             `json:"status"`
	MustChangePassword bool               `json:"must_change_password"`
	LastLoginAt        *time.Time         `json:"last_login_at"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

func toStaff(s Staff) staffResponse {
	out := staffResponse{
		UserID: s.UserID, Name: s.Name, Username: s.Username, Email: s.Email, Phone: s.Phone,
		IsManager: s.IsManager, Status: "inactive", MustChangePassword: s.MustChangePassword,
		CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC(),
	}
	if s.Active {
		out.Status = "active"
	}
	if s.Role != nil {
		out.Role = &staffRoleResponse{RoleID: s.Role.RoleID, Name: s.Role.Name}
	}
	if s.LastLoginAt != nil {
		t := s.LastLoginAt.UTC()
		out.LastLoginAt = &t
	}
	return out
}

// ListStaff handles GET /outlets/{oid}/users?q=&status=&role_id=.
func (h *Handler) ListStaff(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	status := c.Query("status")
	if status != "" && status != "active" && status != "inactive" {
		httpx.Fail(c, h.logger, httpx.Invalid(map[string]string{"status": "oneof"}))
		return
	}
	f := StaffFilter{Search: c.Query("q"), Status: status, RoleID: c.Query("role_id")}
	staff, total, err := h.svc.ListStaff(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]staffResponse, len(staff))
	for i, s := range staff {
		out[i] = toStaff(s)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// GetStaff handles GET /outlets/{oid}/users/{uid}.
func (h *Handler) GetStaff(c *gin.Context) {
	s, err := h.svc.Staff(c.Request.Context(), c.Param("oid"), c.Param("uid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toStaff(s))
}

// CreateStaff handles POST /outlets/{oid}/users.
func (h *Handler) CreateStaff(c *gin.Context) {
	var req createStaffRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	s, err := h.svc.CreateStaff(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), StaffInput(req))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toStaff(s))
}

// UpdateStaff handles PUT /outlets/{oid}/users/{uid}.
func (h *Handler) UpdateStaff(c *gin.Context) {
	var req updateStaffRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	in := StaffInput{Name: req.Name, Email: req.Email, Phone: req.Phone, RoleID: req.RoleID, IsManager: req.IsManager}
	s, err := h.svc.UpdateStaff(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid"), in)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toStaff(s))
}

// SetStaffStatus handles PATCH /outlets/{oid}/users/{uid}/status.
func (h *Handler) SetStaffStatus(c *gin.Context) {
	var req statusRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	s, err := h.svc.SetStaffStatus(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid"), req.Status == "active")
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toStaff(s))
}

// ResetStaffPassword handles POST /outlets/{oid}/users/{uid}/reset-password.
func (h *Handler) ResetStaffPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.ResetStaffPassword(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid"), req.Password); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteStaff handles DELETE /outlets/{oid}/users/{uid}.
func (h *Handler) DeleteStaff(c *gin.Context) {
	if err := h.svc.DeleteStaff(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}
