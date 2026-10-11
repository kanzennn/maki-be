package order

import (
	"encoding/json"
	"os"
	"testing"
)

// The cases every cashier checks its sums against, from the contract's
// folder, so the server, the web app and the Android app agree on every
// rupiah.
type vectors struct {
	Cases []struct {
		Name  string `json:"name"`
		Lines []struct {
			Price int64 `json:"price"`
			Qty   int   `json:"qty"`
		} `json:"lines"`
		Discount   int64 `json:"discount"`
		ServiceBps int   `json:"service_bps"`
		TaxBps     int   `json:"tax_bps"`
		Expected   struct {
			Subtotal int64 `json:"subtotal"`
			Discount int64 `json:"discount"`
			Service  int64 `json:"service"`
			Tax      int64 `json:"tax"`
			Total    int64 `json:"total"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestTotalsMatchTheSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("../../api/test-vectors/order-totals.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			lines := make([]Line, len(c.Lines))
			for i, l := range c.Lines {
				lines[i] = Line{Price: l.Price, Qty: l.Qty}
			}
			got := ComputeTotals(lines, c.Discount, c.ServiceBps, c.TaxBps)
			want := Totals(c.Expected)
			if got != want {
				t.Errorf("ComputeTotals = %+v, want %+v", got, want)
			}
		})
	}
}
