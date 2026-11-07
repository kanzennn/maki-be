package validations

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// maxListFilter caps a comma-separated filter's values.
const maxListFilter = 100

// BindLogFilter reads a Log Action list's query: ?from= and ?to= as RFC 3339
// instants, to after from, and ?action= as a comma-separated list.
func BindLogFilter(c *gin.Context) (models.LogFilter, error) {
	fields := map[string]string{}
	var f models.LogFilter
	f.From = instant(c, "from", fields)
	f.To = instant(c, "to", fields)
	if !f.From.IsZero() && !f.To.IsZero() && !f.To.After(f.From) {
		fields["to"] = "after_from"
	}
	f.Actions = commaList(c, "action", fields)
	if len(fields) > 0 {
		return models.LogFilter{}, apperror.Invalid(fields)
	}
	return f, nil
}

func instant(c *gin.Context, name string, fields map[string]string) time.Time {
	v := c.Query(name)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		fields[name] = "format"
	}
	return t
}

// commaList reads a comma-separated query parameter.
func commaList(c *gin.Context, name string, fields map[string]string) []string {
	v := strings.TrimSpace(c.Query(name))
	if v == "" {
		return nil
	}
	values := strings.Split(v, ",")
	if len(values) > maxListFilter {
		fields[name] = "max"
		return nil
	}
	return values
}

// BindOutletIDs reads ?outlet_ids=, a comma-separated list.
func BindOutletIDs(c *gin.Context) ([]string, error) {
	fields := map[string]string{}
	ids := commaList(c, "outlet_ids", fields)
	if len(fields) > 0 {
		return nil, apperror.Invalid(fields)
	}
	return ids, nil
}

// BindSelf reads ?self=: true narrows the Owner's log to their own actions.
func BindSelf(c *gin.Context) (bool, error) {
	v := c.Query("self")
	if v == "" {
		return false, nil
	}
	self, err := strconv.ParseBool(v)
	if err != nil {
		return false, apperror.Invalid(map[string]string{"self": "format"})
	}
	return self, nil
}
