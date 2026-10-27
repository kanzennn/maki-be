package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
)

// The cases every cashier checks its sums against, from the contract's
// folder, so the server, the web app and the Android app agree on every
// rupiah.
type totalsVectors struct {
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
	var v totalsVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			lines := make([]models.OrderLine, len(c.Lines))
			for i, l := range c.Lines {
				lines[i] = models.OrderLine{Price: l.Price, Qty: l.Qty}
			}
			got := ComputeTotals(lines, c.Discount, c.ServiceBps, c.TaxBps)
			want := models.OrderTotals(c.Expected)
			if got != want {
				t.Errorf("ComputeTotals = %+v, want %+v", got, want)
			}
		})
	}
}

func TestStandInTheHash(t *testing.T) {
	t.Parallel()
	c := models.CheckedOrder{
		OrderID: "o0", OrderNo: "BDG01-K01-20261001-0001", Type: models.OrderDineIn,
		CreatedAt: time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC),
		Lines:     []models.CheckedLine{{MenuID: "m1", Qty: 2, Price: 18000}},
		Total:     41_580, Method: "cash", Received: ptr[int64](50_000),
	}
	// One without a stand hashes as before stands: see
	// TestOrdersWithoutVoucherKeepTheirHash.
	without, err := orderHash(c)
	if err != nil {
		t.Fatal(err)
	}
	c.StandNumber = "12"
	with, err := orderHash(c)
	if err != nil {
		t.Fatal(err)
	}
	if with == without {
		t.Error("the stand isn't in the hash")
	}
	c.StandNumber = "13"
	if other, _ := orderHash(c); other == with {
		t.Error("two stands hash alike")
	}
}

func TestTableInTheHash(t *testing.T) {
	t.Parallel()
	c := models.CheckedOrder{
		OrderID: "o0", OrderNo: "BDG01-K01-20261001-0001", Type: models.OrderDineIn,
		CreatedAt: time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC),
		Lines:     []models.CheckedLine{{MenuID: "m1", Qty: 2, Price: 18000}},
		Total:     41_580, Method: "cash", Received: ptr[int64](50_000),
	}
	without, err := orderHash(c)
	if err != nil {
		t.Fatal(err)
	}
	c.TableID = "t1"
	if with, _ := orderHash(c); with == without {
		t.Error("the table isn't in the hash")
	}
}

func TestOrdersWithoutVoucherKeepTheirHash(t *testing.T) {
	t.Parallel()
	c := models.CheckedOrder{
		OrderID: "o0", OrderNo: "BDG01-K01-20261001-0001", Type: models.OrderTakeaway,
		CreatedAt: time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC),
		Lines:     []models.CheckedLine{{MenuID: "m1", Qty: 2, Price: 18000, Note: ptr("less sugar")}},
		Total:     41_580, Method: "cash", Received: ptr[int64](50_000),
	}
	got, err := orderHash(c)
	if err != nil {
		t.Fatal(err)
	}
	// The orders taken before vouchers were hashed as this.
	type hashedLine struct {
		MenuID string  `json:"menu_id"`
		Qty    int     `json:"qty"`
		Price  int64   `json:"price"`
		Note   *string `json:"note"`
	}
	old, err := json.Marshal(struct {
		OrderID         string       `json:"order_id"`
		OrderNo         string       `json:"order_no"`
		Type            string       `json:"type"`
		ClientCreatedAt time.Time    `json:"client_created_at"`
		Items           []hashedLine `json:"items"`
		Total           int64        `json:"total"`
		Method          string       `json:"method"`
		Received        *int64       `json:"received"`
	}{c.OrderID, c.OrderNo, c.Type, c.CreatedAt, []hashedLine{{"m1", 2, 18000, ptr("less sugar")}}, c.Total, c.Method, c.Received})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(old)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("hash %s, want %s as before vouchers", got, want)
	}
	c.VoucherCode = "KOPI10"
	if with, _ := orderHash(c); with == got {
		t.Error("the voucher isn't in the hash")
	}
}
