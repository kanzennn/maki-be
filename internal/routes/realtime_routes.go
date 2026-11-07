package routes

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/utils/realtime"
)

// RegisterRealtimeRoutes registers an outlet's live messages: the ticket on
// scoped, for whoever may act in the outlet, and the connection on public,
// since a browser can't send its access token there; the ticket is what lets
// it in.
func RegisterRealtimeRoutes(public, scoped gin.IRouter, ctl *controllers.RealtimeController) {
	scoped.POST("/realtime/ticket", ctl.Ticket)
	public.GET("/outlets/:oid/realtime", ctl.Connect)
}

// changeTopics are what writes under an outlet change, by the path after
// /outlets/:oid, its first part. A sale takes stock and can seat a table.
var changeTopics = map[string][]string{
	"":                {realtime.TopicCatalog},
	"status":          {realtime.TopicCatalog},
	"categories":      {realtime.TopicCatalog},
	"menus":           {realtime.TopicCatalog},
	"payment-methods": {realtime.TopicCatalog},
	"orders":          {realtime.TopicCatalog, realtime.TopicTables, realtime.TopicOrders},
	"tables":          {realtime.TopicTables},
	"areas":           {realtime.TopicTables},
	"table-schedules": {realtime.TopicTables},
	"dine-in-mode":    {realtime.TopicTables, realtime.TopicCatalog},
}

// ChangeTopics returns what a write to the route changes. The path is the
// pattern Gin matched, as in /api/v1/outlets/:oid/menus/:mid.
func ChangeTopics(route string) []string {
	_, rest, ok := strings.Cut(route, "/outlets/:oid")
	if !ok {
		return nil
	}
	part, _, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	return changeTopics[part]
}
