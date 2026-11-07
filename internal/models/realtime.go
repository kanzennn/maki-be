package models

// RealtimeTicketResponse is POST /outlets/{oid}/realtime/ticket.
type RealtimeTicketResponse struct {
	Ticket string `json:"ticket"`
	// ExpiresIn is how many seconds the ticket is good for.
	ExpiresIn int `json:"expires_in"`
}
