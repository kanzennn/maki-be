package services

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
)

// The cases every cashier checks a voucher's discount against, from the
// contract's folder, so the server, the web app and the Android app agree
// on every rupiah.
type vectors struct {
	Cases []struct {
		Name    string `json:"name"`
		Voucher struct {
			Type        string `json:"type"`
			Value       int64  `json:"value"`
			MaxDiscount *int64 `json:"max_discount"`
			MinSpend    *int64 `json:"min_spend"`
		} `json:"voucher"`
		Subtotal int64 `json:"subtotal"`
		Expected struct {
			Applies  bool  `json:"applies"`
			Discount int64 `json:"discount"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestDiscountsMatchTheSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("../../api/test-vectors/voucher-discounts.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) < 10 {
		t.Fatalf("%d cases, want at least 10", len(v.Cases))
	}
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			discount, applies := voucherDiscount(models.VoucherTerms(c.Voucher), c.Subtotal)
			if discount != c.Expected.Discount || applies != c.Expected.Applies {
				t.Errorf("Discount(%d) = %d, %v; want %d, %v",
					c.Subtotal, discount, applies, c.Expected.Discount, c.Expected.Applies)
			}
		})
	}
}
