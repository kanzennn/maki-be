package middlewares

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ChangePublisher tells an outlet's clients what changed; realtime.Hub is the
// real one.
type ChangePublisher interface {
	Publish(outletID string, topics ...string)
}

// PublishChanges tells the outlet's connected clients what a request changed,
// once it has succeeded: its transaction has committed by then, so a client
// that loads the data again finds the change. topics says what each route
// changes, by its path pattern; a route it doesn't name changes nothing they
// follow.
func PublishChanges(hub ChangePublisher, topics func(route string) []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if c.Request.Method == http.MethodGet || c.Writer.Status() >= http.StatusBadRequest || len(c.Errors) > 0 {
			return
		}
		if changed := topics(c.FullPath()); len(changed) > 0 {
			hub.Publish(c.Param("oid"), changed...)
		}
	}
}
