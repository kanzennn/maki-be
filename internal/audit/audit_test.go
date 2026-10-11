package audit_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
)

type row struct {
	action, userID, outletID, targetType, targetID, ip *string
	before, after                                      []byte
	occurredAt                                         time.Time
}

func rows(t *testing.T, pool *pgxpool.Pool) []row {
	t.Helper()
	r, err := pool.Query(t.Context(), `SELECT action, user_id, outlet_id, target_type, target_id, host(ip), before_json, after_json, occurred_at FROM action_logs`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(r, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.action, &x.userID, &x.outletID, &x.targetType, &x.targetID, &x.ip, &x.before, &x.after, &x.occurredAt)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRecordCommitsWithTheAction(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()
	occurred := time.Date(2026, 9, 29, 7, 30, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `INSERT INTO outlets (outlet_id, code, name, timezone, locale)
		VALUES ('o1', 'O1', 'Outlet', 'Asia/Jakarta', 'id')`); err != nil {
		t.Fatal(err)
	}

	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return audit.Recorder{}.Record(ctx, tx, audit.Entry{
			Action:     "menu.update",
			OutletID:   "o1",
			TargetType: "menu",
			TargetID:   "m1",
			Before:     map[string]any{"price": 25000},
			After:      map[string]any{"price": 27000},
			IP:         "2001:db8::7",
			OccurredAt: occurred,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	got := rows(t, pool)
	if len(got) != 1 {
		t.Fatalf("%d rows, want 1", len(got))
	}
	r := got[0]
	if *r.action != "menu.update" || *r.outletID != "o1" || *r.targetType != "menu" || *r.targetID != "m1" || *r.ip != "2001:db8::7" {
		t.Errorf("row = %+v", r)
	}
	if r.userID != nil {
		t.Errorf("user_id = %v, want NULL for an entry without a user", *r.userID)
	}
	var before, after map[string]int
	if json.Unmarshal(r.before, &before) != nil || json.Unmarshal(r.after, &after) != nil || before["price"] != 25000 || after["price"] != 27000 {
		t.Errorf("before = %s, after = %s", r.before, r.after)
	}
	if !r.occurredAt.Equal(occurred) {
		t.Errorf("occurred_at = %v, want the time given", r.occurredAt)
	}
}

func TestRecordRollsBackWithTheAction(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()
	errAction := errors.New("the action failed")

	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := (audit.Recorder{}).Record(ctx, tx, audit.Entry{Action: "menu.delete", IP: "203.0.113.7"}); err != nil {
			return err
		}
		return errAction
	})
	if !errors.Is(err, errAction) {
		t.Fatalf("WithTx = %v, want the action's error", err)
	}
	if n := len(rows(t, pool)); n != 0 {
		t.Errorf("%d rows after the rollback, want 0: an entry must not outlive its action", n)
	}
}

func TestRecordDefaultsAndChecks(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()
	before := time.Now().Add(-time.Second)

	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return audit.Recorder{}.Record(ctx, tx, audit.Entry{Action: "auth.owner_created"})
	})
	if err != nil {
		t.Fatal(err)
	}
	r := rows(t, pool)[0]
	if r.ip != nil || r.before != nil || r.after != nil {
		t.Errorf("row = %+v, want NULL ip and JSON when none are given", r)
	}
	if r.occurredAt.Before(before) {
		t.Errorf("occurred_at = %v, want now when none is given", r.occurredAt)
	}

	for name, e := range map[string]audit.Entry{
		"no action": {IP: "203.0.113.7"},
		"bad IP":    {Action: "auth.login", IP: "not-an-ip"},
		"not JSON":  {Action: "auth.login", After: func() {}},
	} {
		err := db.WithTx(ctx, pool, func(tx pgx.Tx) error { return audit.Recorder{}.Record(ctx, tx, e) })
		if err == nil {
			t.Errorf("%s: Record succeeded, want an error", name)
		}
	}
}
