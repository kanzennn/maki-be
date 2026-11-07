package validations

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// BindDashboardQuery reads a dashboard's period: ?from= and ?to= as dates,
// both required and included, at most MaxDashboardDays long.
func BindDashboardQuery(c *gin.Context) (models.DashboardQuery, error) {
	fields := map[string]string{}
	from := date(c, "from", fields)
	to := date(c, "to", fields)
	if len(fields) == 0 {
		switch days := int(to.Sub(from).Hours()/24) + 1; {
		case days < 1:
			fields["to"] = "after_from"
		case days > models.MaxDashboardDays:
			fields["to"] = "range"
		}
	}
	ids := commaList(c, "outlet_ids", fields)
	if len(fields) > 0 {
		return models.DashboardQuery{}, apperror.Invalid(fields)
	}
	return models.DashboardQuery{From: c.Query("from"), To: c.Query("to"), OutletIDs: ids}, nil
}

func date(c *gin.Context, name string, fields map[string]string) time.Time {
	v := c.Query(name)
	if v == "" {
		fields[name] = "required"
		return time.Time{}
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		fields[name] = "format"
	}
	return t
}
