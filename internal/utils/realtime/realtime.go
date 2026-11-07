// Package realtime tells an outlet's connected clients that something of
// theirs changed. A message says only what changed, as topics; a client loads
// the data again, through the same endpoints and permissions as ever, so
// nothing but a topic ever goes over the socket.
//
// Everything lives in memory: the API runs as a single process. A second
// instance would need the messages passed between them, such as with
// PostgreSQL's LISTEN/NOTIFY, and the tickets kept somewhere shared.
package realtime

import (
	"crypto/rand"
	"encoding/base64"
	"slices"
	"sync"
	"time"
)

// The topics a message can name.
const (
	TopicCatalog = "catalog" // menus, prices, stock, rates and methods
	TopicTables  = "tables"  // areas, tables, schedules and the dine-in mode
	TopicOrders  = "orders"  // sales and voids
)

const (
	// TicketTTL is how long a client has to connect with a ticket.
	TicketTTL = 30 * time.Second
	// buffer is how many messages a client may fall behind by. One that falls
	// further is dropped, and loads everything again when it reconnects.
	buffer = 16
)

// Message is what a client receives.
type Message struct {
	Topics []string `json:"topics"`
}

// Subscription is one client's messages.
type Subscription struct {
	// C closes when the client falls too far behind or Close is called.
	C        chan Message
	hub      *Hub
	outletID string
}

// Hub holds each outlet's connected clients and its tickets to connect. It is
// safe for concurrent use.
type Hub struct {
	mu      sync.Mutex
	clients map[string]map[*Subscription]struct{}
	tickets map[string]ticket
	now     func() time.Time
}

type ticket struct {
	outletID string
	expires  time.Time
}

// NewHub returns an empty hub. now is the clock tickets expire by.
func NewHub(now func() time.Time) *Hub {
	return &Hub{clients: map[string]map[*Subscription]struct{}{}, tickets: map[string]ticket{}, now: now}
}

// Subscribe adds a client of the outlet. Its caller closes the subscription.
func (h *Hub) Subscribe(outletID string) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Subscription{C: make(chan Message, buffer), hub: h, outletID: outletID}
	if h.clients[outletID] == nil {
		h.clients[outletID] = map[*Subscription]struct{}{}
	}
	h.clients[outletID][s] = struct{}{}
	return s
}

// Close removes the client. Closing it twice is fine.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.hub.remove(s)
}

// remove takes the client out and closes its channel; the hub's lock is held.
func (h *Hub) remove(s *Subscription) {
	clients := h.clients[s.outletID]
	if _, ok := clients[s]; !ok {
		return
	}
	delete(clients, s)
	if len(clients) == 0 {
		delete(h.clients, s.outletID)
	}
	close(s.C)
}

// Publish tells the outlet's clients the topics changed. It never waits: a
// client too far behind is dropped instead of holding the others up.
func (h *Hub) Publish(outletID string, topics ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.clients[outletID] {
		select {
		case s.C <- Message{Topics: slices.Clone(topics)}:
		default:
			h.remove(s)
		}
	}
}

// IssueTicket returns a ticket that lets one connection into the outlet's
// messages within TicketTTL. A browser can't send an Authorization header on a
// WebSocket, so the signed-in client asks for this first, and it goes in the
// connection's URL: it is short-lived and good for one use.
func (h *Hub) IssueTicket(outletID string) string {
	b := make([]byte, 32)
	// rand.Read never fails, as of Go 1.24.
	_, _ = rand.Read(b)
	value := base64.RawURLEncoding.EncodeToString(b)
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	for k, t := range h.tickets {
		if !now.Before(t.expires) {
			delete(h.tickets, k)
		}
	}
	h.tickets[value] = ticket{outletID: outletID, expires: now.Add(TicketTTL)}
	return value
}

// RedeemTicket reports whether the ticket lets a connection into the outlet,
// and uses it up. Another outlet's ticket isn't used up by trying.
func (h *Hub) RedeemTicket(value, outletID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tickets[value]
	if !ok || t.outletID != outletID {
		return false
	}
	delete(h.tickets, value)
	return h.now().Before(t.expires)
}
