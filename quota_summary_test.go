package acosmi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestQuotaSummary_JSONUnmarshal 锚定 wire format 契约 — 后端字段名/类型变化必须立即报错.
func TestQuotaSummary_JSONUnmarshal(t *testing.T) {
	raw := `{
		"freeTotalEtu": 500000,
		"paidTotalEtu": 12000000,
		"freeBuckets": [
			{
				"bucketId": "free-1",
				"modelId": "*",
				"bucketClass": "GENERIC",
				"tokenQuota": 1000000,
				"tokenUsed": 500000,
				"tokenRemaining": 500000,
				"expiresAt": "2026-06-01T00:00:00Z"
			}
		],
		"paidBuckets": [
			{
				"bucketId": "paid-1",
				"modelId": "*",
				"bucketClass": "COMMERCIAL",
				"tokenQuota": 50000000,
				"tokenUsed": 38000000,
				"tokenRemaining": 12000000,
				"expiresAt": "2026-12-31T23:59:59Z"
			}
		],
		"nextFreeExpiresAt": "2026-06-01T00:00:00Z",
		"nextPaidExpiresAt": "2026-12-31T23:59:59Z"
	}`

	var s QuotaSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if s.FreeTotalEtu != 500000 {
		t.Errorf("FreeTotalEtu want 500000 got %d", s.FreeTotalEtu)
	}
	if s.PaidTotalEtu != 12000000 {
		t.Errorf("PaidTotalEtu want 12000000 got %d", s.PaidTotalEtu)
	}
	if len(s.FreeBuckets) != 1 || s.FreeBuckets[0].BucketID != "free-1" {
		t.Errorf("FreeBuckets parse: %+v", s.FreeBuckets)
	}
	if len(s.PaidBuckets) != 1 || !s.PaidBuckets[0].IsCommercial() {
		t.Errorf("PaidBuckets / IsCommercial: %+v", s.PaidBuckets)
	}

	wantFree := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if s.NextFreeExpiresAt == nil || !s.NextFreeExpiresAt.Equal(wantFree) {
		t.Errorf("NextFreeExpiresAt want %v got %v", wantFree, s.NextFreeExpiresAt)
	}
}

// TestQuotaSummary_EmptyBucketsAreSlices 空切片 vs nil — UI 不应吃 null.
func TestQuotaSummary_EmptyBucketsAreSlices(t *testing.T) {
	raw := `{
		"freeTotalEtu": 0,
		"paidTotalEtu": 0,
		"freeBuckets": [],
		"paidBuckets": []
	}`

	var s QuotaSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.FreeBuckets == nil {
		t.Error("FreeBuckets must be non-nil empty slice for UI safety")
	}
	if s.PaidBuckets == nil {
		t.Error("PaidBuckets must be non-nil empty slice")
	}
	if len(s.FreeBuckets) != 0 || len(s.PaidBuckets) != 0 {
		t.Error("empty arrays should yield len=0")
	}
	if s.NextFreeExpiresAt != nil || s.NextPaidExpiresAt != nil {
		t.Error("omitempty fields default to nil when absent")
	}
}

// TestQuotaSummary_PermanentBucketNoExpiresAt 永久桶 expiresAt nil 不破解析.
func TestQuotaSummary_PermanentBucketNoExpiresAt(t *testing.T) {
	raw := `{
		"freeTotalEtu": 5000000,
		"paidTotalEtu": 0,
		"freeBuckets": [
			{
				"bucketId": "perm-free",
				"modelId": "*",
				"bucketClass": "GENERIC",
				"tokenQuota": 5000000,
				"tokenUsed": 0,
				"tokenRemaining": 5000000
			}
		],
		"paidBuckets": []
	}`

	var s QuotaSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.FreeBuckets[0].ExpiresAt != nil {
		t.Error("permanent bucket expiresAt must be nil")
	}
	if s.NextFreeExpiresAt != nil {
		t.Error("permanent bucket → no NextFreeExpiresAt")
	}
}

// TestGetQuotaSummary_HTTPRoundtrip 端到端 client.GetQuotaSummary.
func TestGetQuotaSummary_HTTPRoundtrip(t *testing.T) {
	expected := QuotaSummary{
		FreeTotalEtu: 1000,
		PaidTotalEtu: 5000,
		FreeBuckets: []BucketRow{
			{BucketID: "f1", ModelID: "*", BucketClass: BucketClassGeneric, TokenRemaining: 1000},
		},
		PaidBuckets: []BucketRow{
			{BucketID: "p1", ModelID: "*", BucketClass: BucketClassCommercial, TokenRemaining: 5000},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/entitlements/quota-summary" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": expected,
			"code": 0,
		})
	}))
	defer srv.Close()

	cli := testClient(t, srv.URL)

	got, err := cli.GetQuotaSummary(context.Background())
	if err != nil {
		t.Fatalf("GetQuotaSummary: %v", err)
	}

	if got.FreeTotalEtu != expected.FreeTotalEtu || got.PaidTotalEtu != expected.PaidTotalEtu {
		t.Errorf("totals mismatch: got=%+v want=%+v", got, expected)
	}
	if len(got.FreeBuckets) != 1 || got.FreeBuckets[0].BucketID != "f1" {
		t.Errorf("FreeBuckets mismatch: %+v", got.FreeBuckets)
	}
	if len(got.PaidBuckets) != 1 || !got.PaidBuckets[0].IsCommercial() {
		t.Errorf("PaidBuckets mismatch: %+v", got.PaidBuckets)
	}
}

// TestBucketInfo_FreePaidFields 校验 BucketInfo 新增的 Free/Paid 字段反序列化.
func TestBucketInfo_FreePaidFields(t *testing.T) {
	raw := `{
		"quotaEtu": 10000,
		"usedEtu": 3000,
		"remainingEtu": 7000,
		"sharedPoolEtu": 0,
		"bucketClass": "COMMERCIAL",
		"freeRemainingEtu": 2000,
		"paidRemainingEtu": 5000
	}`
	var b BucketInfo
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if b.FreeRemainingEtu != 2000 {
		t.Errorf("FreeRemainingEtu want 2000 got %d", b.FreeRemainingEtu)
	}
	if b.PaidRemainingEtu != 5000 {
		t.Errorf("PaidRemainingEtu want 5000 got %d", b.PaidRemainingEtu)
	}
	// 旧字段保留兼容
	if b.RemainingEtu != 7000 {
		t.Errorf("RemainingEtu (legacy) want 7000 got %d", b.RemainingEtu)
	}
}

// TestBucketRow_IsCommercial 大小写不敏感.
func TestBucketRow_IsCommercial(t *testing.T) {
	cases := []struct {
		class string
		want  bool
	}{
		{"COMMERCIAL", true},
		{"commercial", true},
		{"Commercial", true},
		{"GENERIC", false},
		{"generic", false},
		{"", false},
	}
	for _, c := range cases {
		r := &BucketRow{BucketClass: c.class}
		if got := r.IsCommercial(); got != c.want {
			t.Errorf("IsCommercial(%q) want %v got %v", c.class, c.want, got)
		}
	}
	var nilRow *BucketRow
	if nilRow.IsCommercial() {
		t.Error("nil receiver must be safe")
	}
}
