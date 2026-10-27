package controllers

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
)

// principal is the signed-in user, whom Authenticate has put on every route
// that acts for someone.
func principal(c *gin.Context) models.Principal {
	p, _ := models.PrincipalFrom(c.Request.Context())
	return p
}
