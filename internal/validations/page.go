package validations

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// BindPage reads ?page= and ?page_size=. A value that isn't a whole number
// in range is a validation_failed error naming the parameter.
func BindPage(c *gin.Context) (pagination.Page, error) {
	p := pagination.Page{Number: 1, Size: pagination.DefaultPageSize}
	fields := map[string]string{}
	if v := c.Query("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fields["page"] = "min"
		}
		p.Number = n
	}
	if v := c.Query("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > pagination.MaxPageSize {
			fields["page_size"] = "range"
		}
		p.Size = n
	}
	if len(fields) > 0 {
		return pagination.Page{}, apperror.Invalid(fields)
	}
	return p, nil
}
