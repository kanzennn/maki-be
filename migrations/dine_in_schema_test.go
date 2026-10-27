package migrations_test

import (
	"testing"

	"github.com/kervzent/maki-be/internal/tests/dbtest"
)

func TestDineInSettingsRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	if err := insertOutlet(t, pool, "o3", "O3"); err != nil {
		t.Fatal(err)
	}
	setting := func(outletID, mode, by string) error {
		return insertRow(t, pool, "dine_in_settings", map[string]any{
			"outlet_id": outletID, "mode": mode, "updated_by": by,
		}, nil)
	}
	for _, err := range []error{setting("o1", "number", "u1"), setting("o2", "table", "u1")} {
		if err != nil {
			t.Fatalf("valid settings: %v", err)
		}
	}
	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"a second row for an outlet", setting("o1", "table", "u1"), "dine_in_settings_pkey"},
		{"an unknown mode", setting("o3", "queue", "u1"), "dine_in_settings_mode_check"},
		{"an outlet that doesn't exist", setting("nope", "number", "u1"), "dine_in_settings_outlet_id_fkey"},
		{"a user that doesn't exist", setting("o3", "number", "nope"), "dine_in_settings_updated_by_fkey"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestOrdersNameTheirStand(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	tables(t, pool)
	atStand := func(number any, extra map[string]any) map[string]any {
		row := map[string]any{"type": "dine_in", "stand_number": number}
		for k, v := range extra {
			row[k] = v
		}
		return row
	}
	for _, err := range []error{
		insertOrder(t, pool, "ok1", atStand("12", nil)),
		insertOrder(t, pool, "ok2", atStand("A5", nil)),
		insertOrder(t, pool, "ok3", atStand("B-3", nil)),
		insertOrder(t, pool, "ok4", atStand("1234567890", nil)),
		// The same stand again: another order of the customers who took it.
		insertOrder(t, pool, "ok5", atStand("12", nil)),
	} {
		if err != nil {
			t.Fatalf("valid orders: %v", err)
		}
	}
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"a takeaway at a stand", insertOrder(t, pool, "b1", atStand("12", map[string]any{"type": "takeaway"}))},
		{"a stand and a table", insertOrder(t, pool, "b2", atStand("12", map[string]any{"table_id": "t1", "table_name": "A1"}))},
		{"a number left blank", insertOrder(t, pool, "b3", atStand("", nil))},
		{"a small letter", insertOrder(t, pool, "b4", atStand("a5", nil))},
		{"past 10 characters", insertOrder(t, pool, "b5", atStand("12345678901", nil))},
		{"a space", insertOrder(t, pool, "b6", atStand("A 5", nil))},
		{"a hyphen first", insertOrder(t, pool, "b7", atStand("-12", nil))},
		{"a hyphen last", insertOrder(t, pool, "b8", atStand("12-", nil))},
		{"another sign", insertOrder(t, pool, "b9", atStand("12#", nil))},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, "orders_stand_number_check") })
	}
}
