package payment

import (
	"net/http"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
)

const path = "/api/v1/outlets/o1/payment-methods"

func (h *harness) seedMethods() {
	h.t.Helper()
	h.exec(`INSERT INTO outlet_payment_methods (outlet_id, method, is_enabled) VALUES
		('o1', 'cash', true), ('o1', 'qris', false), ('o1', 'debit', false)`)
}

func (h *harness) logged() int {
	h.t.Helper()
	var n int
	if err := h.pool.QueryRow(h.t.Context(), `SELECT count(*) FROM action_logs WHERE action = 'payment.updated'`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func TestReadMethods(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedMethods()
	var got models.PaymentMethodsResponse
	h.do(http.MethodGet, path, "manager", nil).status(http.StatusOK, "").decode(&got)
	want := []models.PaymentMethodResponse{
		{Method: "cash", IsEnabled: true, Available: true},
		{Method: "qris", IsEnabled: false, Available: false},
		{Method: "debit", IsEnabled: false, Available: false},
	}
	if len(got.Methods) != len(want) {
		t.Fatalf("methods = %+v", got.Methods)
	}
	for i := range want {
		if got.Methods[i] != want[i] {
			t.Errorf("method %d = %+v, want %+v", i, got.Methods[i], want[i])
		}
	}
}

func TestAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedMethods()
	// Payment Control is the Manager's: the Owner looks into outlets but
	// doesn't run them.
	for who, want := range map[string]int{"owner": http.StatusForbidden, "cashier": http.StatusForbidden, "manager2": http.StatusNotFound} {
		h.do(http.MethodGet, path, who, nil).status(want, "")
		h.do(http.MethodPut, path, who, map[string]any{"methods": []any{}}).status(want, "")
	}
}

func TestRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedMethods()
	on := func(method string, enabled bool) map[string]any {
		return map[string]any{"methods": []any{map[string]any{"method": method, "is_enabled": enabled}}}
	}
	h.do(http.MethodPut, path, "manager", on("cash", false)).status(http.StatusConflict, "payment_method_required")
	h.do(http.MethodPut, path, "manager", on("qris", true)).status(http.StatusConflict, "payment_method_coming_soon")
	h.do(http.MethodPut, path, "manager", on("gopay", true)).status(http.StatusUnprocessableEntity, "validation_failed")
	h.do(http.MethodPut, path, "manager", map[string]any{"methods": []any{}}).status(http.StatusUnprocessableEntity, "validation_failed")
	h.do(http.MethodPut, path, "manager", map[string]any{"methods": []any{
		map[string]any{"method": "cash", "is_enabled": true}, map[string]any{"method": "cash", "is_enabled": true},
	}}).status(http.StatusUnprocessableEntity, "validation_failed")
	h.do(http.MethodPut, path, "manager", map[string]any{"methods": []any{map[string]any{"method": "cash"}}}).
		status(http.StatusUnprocessableEntity, "validation_failed")

	// Saving what is so changes and records nothing.
	h.do(http.MethodPut, path, "manager", on("cash", true)).status(http.StatusOK, "")
	if n := h.logged(); n != 0 {
		t.Errorf("logged %d for no change", n)
	}
}

// A method on from before it was held back can still be turned off, which the
// log records, with the cashier no longer offering it.
func TestTurningOffIsLogged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedMethods()
	h.exec(`UPDATE outlet_payment_methods SET is_enabled = true WHERE outlet_id = 'o1' AND method = 'qris'`)
	var got models.PaymentMethodsResponse
	h.do(http.MethodPut, path, "manager", map[string]any{"methods": []any{
		map[string]any{"method": "qris", "is_enabled": false},
	}}).status(http.StatusOK, "").decode(&got)
	if got.Methods[1].Method != "qris" || got.Methods[1].IsEnabled {
		t.Errorf("after = %+v", got.Methods)
	}
	if n := h.logged(); n != 1 {
		t.Errorf("logged %d, want 1", n)
	}
	var enabled bool
	if err := h.pool.QueryRow(t.Context(), `SELECT is_enabled FROM outlet_payment_methods WHERE outlet_id = 'o1' AND method = 'qris'`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("qris is still on in the table")
	}
}
