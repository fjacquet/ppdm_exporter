package report

import (
	"context"
	"testing"
)

func TestPrunePerTenant(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	// Two tenants, each with an old (~500d) and a recent (~1d) job + copy.
	exec(`INSERT INTO backup_jobs (id,tenant,server,created_at,captured_at) VALUES
		('ja_old','acme','s1', now()-interval '500 days', now()),
		('ja_new','acme','s1', now()-interval '1 day', now()),
		('jg_old','globex','s1', now()-interval '500 days', now()),
		('jg_new','globex','s1', now()-interval '1 day', now())`)
	exec(`INSERT INTO copies (id,tenant,server,create_time,captured_at) VALUES
		('ca_old','acme','s1', now()-interval '500 days', now()),
		('ca_new','acme','s1', now()-interval '1 day', now()),
		('cg_old','globex','s1', now()-interval '500 days', now()),
		('cg_new','globex','s1', now()-interval '1 day', now())`)

	// acme keeps 730 days; globex falls to the 400-day default.
	if err := st.Prune(ctx, 400, map[string]int{"acme": 730}); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	has := func(table, id string) bool {
		var n int
		_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE id=$1", id).Scan(&n)
		return n == 1
	}
	// acme's 500d rows survive (within 730); recent rows survive everywhere.
	for _, c := range []struct {
		table, id string
		want      bool
	}{
		{"backup_jobs", "ja_old", true}, // acme override 730 > 500
		{"backup_jobs", "ja_new", true},
		{"backup_jobs", "jg_old", false}, // globex default 400 < 500 -> pruned
		{"backup_jobs", "jg_new", true},
		{"copies", "ca_old", true},
		{"copies", "ca_new", true},
		{"copies", "cg_old", false},
		{"copies", "cg_new", true},
	} {
		if got := has(c.table, c.id); got != c.want {
			t.Errorf("%s/%s present=%v, want %v", c.table, c.id, got, c.want)
		}
	}
}

func TestPruneEmptyOverridesIsGlobal(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.pool.Exec(ctx, `INSERT INTO backup_jobs (id,tenant,server,created_at,captured_at)
		VALUES ('old','t','s1', now()-interval '500 days', now()), ('new','t','s1', now()-interval '1 day', now())`); err != nil {
		t.Fatal(err)
	}
	if err := st.Prune(ctx, 400, nil); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	var old, recent int
	_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM backup_jobs WHERE id='old'").Scan(&old)
	_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM backup_jobs WHERE id='new'").Scan(&recent)
	if old != 0 || recent != 1 {
		t.Errorf("global prune: old=%d (want 0) new=%d (want 1)", old, recent)
	}
}
