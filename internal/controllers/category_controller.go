package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// CategoryController serves an outlet's menu categories.
type CategoryController struct {
	categories *services.CategoryService
}

// NewCategoryController returns the categories' controller.
func NewCategoryController(categories *services.CategoryService) *CategoryController {
	return &CategoryController{categories: categories}
}

// ListCategories handles GET /outlets/{oid}/categories?page=&page_size=.
func (ctl *CategoryController) ListCategories(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.categories.ListCategories(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.CategoryResponse, len(items))
	for i, item := range items {
		out[i] = models.NewCategoryResponse(item)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// GetCategory handles GET /outlets/{oid}/categories/{cid}.
func (ctl *CategoryController) GetCategory(c *gin.Context) {
	cat, err := ctl.categories.Category(c.Request.Context(), c.Param("oid"), c.Param("cid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewCategoryResponse(cat))
}

// CreateCategory handles POST /outlets/{oid}/categories.
func (ctl *CategoryController) CreateCategory(c *gin.Context) {
	var req validations.CategoryRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	cat, err := ctl.categories.CreateCategory(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewCategoryResponse(cat))
}

// UpdateCategory handles PUT /outlets/{oid}/categories/{cid}.
func (ctl *CategoryController) UpdateCategory(c *gin.Context) {
	var req validations.CategoryRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	cat, err := ctl.categories.UpdateCategory(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("cid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewCategoryResponse(cat))
}

// SetCategoryStatus handles PATCH /outlets/{oid}/categories/{cid}/status.
func (ctl *CategoryController) SetCategoryStatus(c *gin.Context) {
	var req validations.ActiveRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	cat, err := ctl.categories.SetCategoryActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("cid"), *req.IsActive)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewCategoryResponse(cat))
}

// ReorderCategories handles PUT /outlets/{oid}/categories/order.
func (ctl *CategoryController) ReorderCategories(c *gin.Context) {
	var req validations.ReorderCategoriesRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.categories.ReorderCategories(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.CategoryIDs); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteCategory handles DELETE /outlets/{oid}/categories/{cid}.
func (ctl *CategoryController) DeleteCategory(c *gin.Context) {
	if err := ctl.categories.DeleteCategory(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("cid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
