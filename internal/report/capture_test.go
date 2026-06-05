package report

import (
	"context"
	"testing"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestCaptureServerPersists(t *testing.T) {
	st := newTestStore(t)
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", `{"page":{"totalPages":1},"content":[
		{"id":"j1","category":"PROTECT","state":"COMPLETED","createdAt":"2026-06-05T01:00:00Z",
		 "result":{"status":"SUCCESS","bytesTransferred":1048576},"asset":{"id":"a1","name":"vm-app01"}}]}`)
	m.SetJSONPrefix("/api/v2/copies", `{"page":{"totalPages":1},"content":[
		{"id":"c1","assetId":"a1","copyType":"FULL","createTime":"2026-06-05T01:04:00Z","retentionLock":true}]}`)
	m.SetJSONPrefix("/api/v2/assets", `{"page":{"totalPages":1},"content":[
		{"id":"a1","name":"vm-app01","type":"VMWARE_VIRTUAL_MACHINE","protectionStatus":"PROTECTED"}]}`)
	m.SetJSONPrefix("/api/v3/protection-policies", `{"page":{"totalPages":1},"content":[
		{"id":"p1","name":"Gold-VM","objectives":[{"type":"BACKUP"}]}]}`)

	cap := NewCapturer(st, "v-test", 400)
	if err := cap.CaptureServer(context.Background(), "acme", m); err != nil {
		t.Fatalf("CaptureServer: %v", err)
	}
	ctx := context.Background()
	for tbl, want := range map[string]int{"backup_jobs": 1, "copies": 1, "assets": 1, "protection_policies": 1} {
		var n int
		_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n)
		if n != want {
			t.Errorf("%s rows = %d, want %d", tbl, n, want)
		}
	}
	var ok bool
	_ = st.pool.QueryRow(ctx, `SELECT ok FROM capture_runs WHERE server='ppdm01' ORDER BY id DESC LIMIT 1`).Scan(&ok)
	if !ok {
		t.Fatal("capture_run not ok")
	}
}
