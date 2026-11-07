package realtime

import (
	"testing"
	"time"
)

func TestPublishReachesTheOutletAlone(t *testing.T) {
	h := NewHub(time.Now)
	a, b := h.Subscribe("o1"), h.Subscribe("o2")
	defer a.Close()
	defer b.Close()
	h.Publish("o1", TopicCatalog, TopicOrders)
	got := <-a.C
	if len(got.Topics) != 2 || got.Topics[0] != TopicCatalog {
		t.Errorf("o1 got %+v", got)
	}
	select {
	case m := <-b.C:
		t.Errorf("o2 got %+v", m)
	default:
	}
}

func TestAClientTooFarBehindIsDropped(t *testing.T) {
	h := NewHub(time.Now)
	slow, keeping := h.Subscribe("o1"), h.Subscribe("o1")
	defer keeping.Close()
	for range buffer + 1 {
		h.Publish("o1", TopicOrders)
		<-keeping.C
	}
	for range buffer {
		<-slow.C
	}
	if _, open := <-slow.C; open {
		t.Error("the slow client is still subscribed")
	}
	slow.Close() // closing what was dropped is fine
	h.Publish("o1", TopicOrders)
	if _, open := <-keeping.C; !open {
		t.Error("the client keeping up was dropped")
	}
}

func TestTickets(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	h := NewHub(func() time.Time { return now })

	once := h.IssueTicket("o1")
	if h.RedeemTicket(once, "o2") {
		t.Error("o1's ticket let a connection into o2")
	}
	if !h.RedeemTicket(once, "o1") {
		t.Error("a good ticket was refused, after a try at another outlet")
	}
	if h.RedeemTicket(once, "o1") {
		t.Error("a ticket worked twice")
	}
	if h.RedeemTicket("no such ticket", "o1") {
		t.Error("an unknown ticket worked")
	}

	late := h.IssueTicket("o1")
	now = now.Add(TicketTTL)
	if h.RedeemTicket(late, "o1") {
		t.Error("an expired ticket worked")
	}

	old := h.IssueTicket("o1")
	now = now.Add(TicketTTL)
	h.IssueTicket("o1")
	if _, kept := h.tickets[old]; kept {
		t.Error("an expired ticket was kept")
	}
}
