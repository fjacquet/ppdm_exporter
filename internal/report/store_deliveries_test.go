package report

import (
	"context"
	"testing"
)

func TestDeliveriesExistsAndRecord(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ok, err := st.DeliveryExists(ctx, "acme", "2026-W23")
	if err != nil || ok {
		t.Fatalf("initial exists = %v,%v want false,nil", ok, err)
	}
	// A failed delivery must NOT count as existing (so it retries).
	if err := st.RecordDelivery(ctx, "acme", "2026-W23", false, "smtp down", []string{"a@b.c"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-W23"); ok {
		t.Error("failed delivery should not count as existing")
	}
	// A later success for the same period upserts and now counts.
	if err := st.RecordDelivery(ctx, "acme", "2026-W23", true, "", []string{"a@b.c", "d@e.f"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-W23"); !ok {
		t.Error("successful delivery should count as existing")
	}
	// Different period is independent.
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-W24"); ok {
		t.Error("other period should not exist")
	}
}
