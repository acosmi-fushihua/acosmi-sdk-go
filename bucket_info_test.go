// =============================================================================
// 文件: bucket_info_test.go (V0.18 V30 entitlement-listing)
// 覆盖: BucketInfo struct 反序列化 / omitempty / 向后兼容 / nil-safe
//
// 设计理由: 上游可能返回三种形态:
//   1) 老 nexus / admin 路径 → 不带 bucketInfo 字段 → BucketInfo == nil
//   2) 普通用户 + 已购模型 → 带 bucketInfo 完整对象
//   3) 永久权益桶 → bucketInfo.expiresAt 缺失 / null
// SDK 用 *BucketInfo 指针 + omitempty, 三种形态都不破解码.
// =============================================================================

package acosmi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBucketInfo_OmitemptyOnEmpty(t *testing.T) {
	bi := BucketInfo{}
	out, err := json.Marshal(bi)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "expiresAt") {
		t.Errorf("空 BucketInfo 不该序列化 expiresAt: %s", s)
	}
	if strings.Contains(s, "expired") {
		t.Errorf("Expired=false 应被 omitempty: %s", s)
	}
}

func TestManagedModel_NilBucketInfoOmitted(t *testing.T) {
	m := ManagedModel{
		ID: "id-1", Name: "test", ModelID: "x", Provider: "openai", IsEnabled: true,
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "bucketInfo") {
		t.Errorf("nil BucketInfo 必须 omitempty 不出现, got: %s", out)
	}
}

func TestManagedModel_DeserializeWithoutBucketInfo(t *testing.T) {
	// 老 v0.17 nexus 响应: 不带 bucketInfo 字段, SDK 应正常解码 BucketInfo=nil
	raw := `{
		"id":"id-1","name":"Alpha","modelId":"a","provider":"openai",
		"maxTokens":4096,"isEnabled":true,
		"capabilities":{}
	}`
	var m ManagedModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.BucketInfo != nil {
		t.Errorf("无 bucketInfo 字段时 BucketInfo 必须 nil, got %+v", m.BucketInfo)
	}
	if m.Name != "Alpha" {
		t.Errorf("Name = %q", m.Name)
	}
}

func TestManagedModel_DeserializeWithBucketInfo(t *testing.T) {
	raw := `{
		"id":"id-1","name":"Alpha","modelId":"a","provider":"openai",
		"maxTokens":4096,"isEnabled":true,
		"capabilities":{},
		"bucketInfo":{
			"quotaEtu":1000,
			"usedEtu":150,
			"remainingEtu":850,
			"expiresAt":"2026-12-31T23:59:59Z",
			"bucketClass":"COMMERCIAL",
			"expired":false
		}
	}`
	var m ManagedModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.BucketInfo == nil {
		t.Fatal("BucketInfo 不应为 nil")
	}
	if m.BucketInfo.QuotaEtu != 1000 || m.BucketInfo.UsedEtu != 150 || m.BucketInfo.RemainingEtu != 850 {
		t.Errorf("quota/used/remaining 解码错误: %+v", m.BucketInfo)
	}
	if m.BucketInfo.BucketClass != "COMMERCIAL" {
		t.Errorf("BucketClass = %q", m.BucketInfo.BucketClass)
	}
	if m.BucketInfo.Expired {
		t.Errorf("Expired 应为 false")
	}
	if m.BucketInfo.ExpiresAt == nil {
		t.Fatal("ExpiresAt 不应为 nil")
	}
	if m.BucketInfo.ExpiresAt.Year() != 2026 {
		t.Errorf("ExpiresAt year = %d, want 2026", m.BucketInfo.ExpiresAt.Year())
	}
}

func TestManagedModel_PermanentBucketNullExpiresAt(t *testing.T) {
	cases := []string{
		`{"bucketInfo":{"quotaEtu":9999,"remainingEtu":9999,"bucketClass":"COMMERCIAL"}}`,
		`{"bucketInfo":{"quotaEtu":9999,"remainingEtu":9999,"bucketClass":"COMMERCIAL","expiresAt":null}}`,
	}
	for i, raw := range cases {
		var m ManagedModel
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if m.BucketInfo == nil {
			t.Fatalf("case %d: BucketInfo 应非 nil", i)
		}
		if m.BucketInfo.ExpiresAt != nil {
			t.Errorf("case %d: 永久桶 ExpiresAt 必须 nil, got %v", i, m.BucketInfo.ExpiresAt)
		}
		if m.BucketInfo.RemainingEtu != 9999 {
			t.Errorf("case %d: RemainingEtu = %d", i, m.BucketInfo.RemainingEtu)
		}
	}
}

func TestManagedModel_RoundtripPreservesAllFields(t *testing.T) {
	exp := time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC)
	original := ManagedModel{
		ID: "id-1", Name: "Beta", ModelID: "b", Provider: "anthropic", IsEnabled: true,
		BucketInfo: &BucketInfo{
			QuotaEtu:     5000,
			UsedEtu:      1200,
			RemainingEtu: 3800,
			ExpiresAt:    &exp,
			BucketClass:  "GENERIC",
			Expired:      false,
		},
	}
	out, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\"bucketInfo\"") || !strings.Contains(string(out), "\"GENERIC\"") {
		t.Errorf("序列化丢字段: %s", out)
	}

	var roundtrip ManagedModel
	if err := json.Unmarshal(out, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if roundtrip.BucketInfo == nil {
		t.Fatal("roundtrip BucketInfo 不应为 nil")
	}
	if roundtrip.BucketInfo.QuotaEtu != original.BucketInfo.QuotaEtu {
		t.Errorf("QuotaEtu 丢: %d != %d", roundtrip.BucketInfo.QuotaEtu, original.BucketInfo.QuotaEtu)
	}
	if roundtrip.BucketInfo.BucketClass != original.BucketInfo.BucketClass {
		t.Errorf("BucketClass 丢")
	}
	if roundtrip.BucketInfo.ExpiresAt == nil {
		t.Fatal("ExpiresAt 丢")
	}
	if roundtrip.BucketInfo.ExpiresAt.Unix() != exp.Unix() {
		t.Errorf("ExpiresAt 时间偏移: %v != %v", roundtrip.BucketInfo.ExpiresAt, exp)
	}
}

func TestManagedModel_ExpiredBucketSerialized(t *testing.T) {
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	m := ManagedModel{
		ID: "x", ModelID: "x", IsEnabled: true,
		BucketInfo: &BucketInfo{
			QuotaEtu: 1000, UsedEtu: 1000, RemainingEtu: 0,
			ExpiresAt: &past, BucketClass: "COMMERCIAL", Expired: true,
		},
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"expired":true`) {
		t.Errorf("Expired=true 应序列化 (UI 据此灰显), got: %s", out)
	}

	var got ManagedModel
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.BucketInfo.Expired {
		t.Errorf("解码后 Expired 应为 true")
	}
}

// V30 P1-6 audit fix: strict mode (DisallowUnknownFields) 客户端的兼容性锚定.
// 老 v0.17 SDK 客户端如果开了 strict mode + 升级到 nexus 0.18+, 会因 bucketInfo
// 字段拒绝解码. 本测试锚定该行为, 让任何下游 strict 客户端在 SDK 升级清单里看见此约束.
func TestManagedModel_StrictModeRejectsUnknownFieldsBeforeUpgrade(t *testing.T) {
	// 模拟 v0.17 client 收到 v0.18 nexus 响应 — strict 模式应 fail.
	type ManagedModelV017 struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		ModelID  string `json:"modelId"`
		Provider string `json:"provider"`
		// 注意: 故意没有 BucketInfo 字段, 模拟未升级到 v0.18 的客户端
	}
	raw := `{"id":"a","name":"A","modelId":"a","provider":"openai","bucketInfo":{"quotaEtu":1000,"bucketClass":"COMMERCIAL"}}`

	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var m ManagedModelV017
	err := dec.Decode(&m)
	if err == nil {
		t.Fatal("strict mode 应该拒绝 v0.17 schema 解析包含 bucketInfo 的响应")
	}
	if !strings.Contains(err.Error(), "bucketInfo") {
		t.Errorf("error 应提到 bucketInfo, got: %v", err)
	}

	// v0.18 SDK 自身的 ManagedModel struct 同 raw 输入, strict 也应过
	dec2 := json.NewDecoder(strings.NewReader(raw))
	dec2.DisallowUnknownFields()
	var m2 ManagedModel
	if err := dec2.Decode(&m2); err != nil {
		t.Fatalf("v0.18 SDK strict 解析自家响应不应失败: %v", err)
	}
	if m2.BucketInfo == nil || m2.BucketInfo.QuotaEtu != 1000 {
		t.Errorf("v0.18 strict 解析后字段应正常: %+v", m2.BucketInfo)
	}
}

// V30 P0-5 audit fix: SharedPoolEtu 字段反序列化 + omitempty
func TestBucketInfo_SharedPoolEtuRoundtrip(t *testing.T) {
	bi := &BucketInfo{
		QuotaEtu: 1500, UsedEtu: 0, RemainingEtu: 1500,
		SharedPoolEtu: 500, BucketClass: "COMMERCIAL",
	}
	out, err := json.Marshal(bi)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"sharedPoolEtu":500`) {
		t.Errorf("sharedPoolEtu 应序列化, got: %s", out)
	}

	// SharedPoolEtu = 0 时 omitempty 不出现
	bi2 := &BucketInfo{QuotaEtu: 1000, RemainingEtu: 1000, BucketClass: "COMMERCIAL"}
	out2, _ := json.Marshal(bi2)
	if strings.Contains(string(out2), "sharedPoolEtu") {
		t.Errorf("SharedPoolEtu=0 应被 omitempty: %s", out2)
	}

	// 老 v0.17 nexus 响应 (无 sharedPoolEtu) 解码到 v0.18 结构
	raw := `{"quotaEtu":1000,"remainingEtu":1000,"bucketClass":"GENERIC"}`
	var got BucketInfo
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.SharedPoolEtu != 0 {
		t.Errorf("无 sharedPoolEtu 字段时应解为 0, got %d", got.SharedPoolEtu)
	}
}

// 防回归: 混合 list (一个带 bucketInfo, 一个不带 admin fallback) 解码兼容
func TestListModelsResponse_BucketInfoMixedList(t *testing.T) {
	raw := `[
		{"id":"a","name":"A","modelId":"a","provider":"openai","maxTokens":1024,"isEnabled":true,"capabilities":{},
		 "bucketInfo":{"quotaEtu":1000,"usedEtu":0,"remainingEtu":1000,"bucketClass":"COMMERCIAL"}},
		{"id":"b","name":"B","modelId":"b","provider":"openai","maxTokens":1024,"isEnabled":true,"capabilities":{}}
	]`
	var models []ManagedModel
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("len = %d", len(models))
	}
	if models[0].BucketInfo == nil {
		t.Errorf("models[0] 应有 BucketInfo")
	} else if models[0].BucketInfo.RemainingEtu != 1000 {
		t.Errorf("models[0].RemainingEtu = %d", models[0].BucketInfo.RemainingEtu)
	}
	if models[1].BucketInfo != nil {
		t.Errorf("models[1] 不带 bucketInfo, 解码应 nil")
	}
}

// V30 二轮审计 D-P2-1 (case 1/3): 上游 buildBucketView 语义保证 Expired=true 时全部桶过期,
// RemainingEtu 应为 0. 但若上游有 bug 漏算 SharedPoolEtu, 可能出现 Expired=true && Remaining>0.
// SDK 端不修正语义矛盾, 而是忠实透传字段 (UI 层决定是否警告 / 灰显 / 报警).
func TestBucketInfo_ExpiredButRemainingPositive_FaithfullyTransported(t *testing.T) {
	raw := `{"id":"a","name":"A","modelId":"a","provider":"openai","maxTokens":1024,"isEnabled":true,"capabilities":{},
		"bucketInfo":{"quotaEtu":1000,"usedEtu":700,"remainingEtu":300,"bucketClass":"COMMERCIAL","expired":true}}`
	var m ManagedModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.BucketInfo == nil {
		t.Fatal("BucketInfo 应非 nil")
	}
	if !m.BucketInfo.Expired {
		t.Error("Expired 字段应为 true")
	}
	if m.BucketInfo.RemainingEtu != 300 {
		t.Errorf("RemainingEtu 应忠实透传 300, got %d (即使语义与 Expired=true 矛盾)", m.BucketInfo.RemainingEtu)
	}
}

// V30 二轮审计 D-P2-1 (case 2/3): ExpiresAt 非-Z 时区也应解析成功.
// Java Instant.toString 输出 Z 时区, 但若上游切 LocalDateTime.atZone(...) 输出含 offset,
// SDK *time.Time 仍能 unmarshal (RFC3339 with offset).
func TestBucketInfo_ExpiresAtTimezoneOffset(t *testing.T) {
	raw := `{"id":"a","name":"A","modelId":"a","provider":"openai","maxTokens":1024,"isEnabled":true,"capabilities":{},
		"bucketInfo":{"quotaEtu":1000,"usedEtu":0,"remainingEtu":1000,"bucketClass":"COMMERCIAL","expiresAt":"2026-12-31T23:59:59+08:00"}}`
	var m ManagedModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.BucketInfo == nil || m.BucketInfo.ExpiresAt == nil {
		t.Fatal("ExpiresAt 应解析成功")
	}
	if m.BucketInfo.ExpiresAt.Year() != 2026 {
		t.Errorf("Year = %d, want 2026", m.BucketInfo.ExpiresAt.Year())
	}
}

// V30 二轮审计 D-P2-1 (case 3/3): SharedPoolEtu > RemainingEtu 是上游异常 (通配桶子集 > 总和),
// SDK 不应当报错或修正, 应忠实透传 — UI 层可据此判定数据异常并告警.
func TestBucketInfo_SharedPoolGreaterThanRemaining_FaithfullyTransported(t *testing.T) {
	raw := `{"id":"a","name":"A","modelId":"a","provider":"openai","maxTokens":1024,"isEnabled":true,"capabilities":{},
		"bucketInfo":{"quotaEtu":500,"usedEtu":0,"remainingEtu":500,"sharedPoolEtu":800,"bucketClass":"GENERIC"}}`
	var m ManagedModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.BucketInfo == nil {
		t.Fatal("BucketInfo 应非 nil")
	}
	if m.BucketInfo.SharedPoolEtu != 800 {
		t.Errorf("SharedPoolEtu 应忠实透传 800 即便 > Remaining 500, got %d", m.BucketInfo.SharedPoolEtu)
	}
	if m.BucketInfo.RemainingEtu != 500 {
		t.Errorf("RemainingEtu 应忠实透传 500, got %d", m.BucketInfo.RemainingEtu)
	}
}

// V30 二轮审计 D-P1-3: BucketClass 常量 + IsCommercial() 大小写不敏感判定锚定.
func TestBucketInfo_IsCommercial_CaseInsensitive(t *testing.T) {
	cases := []struct {
		class string
		want  bool
	}{
		{BucketClassCommercial, true},
		{"COMMERCIAL", true},
		{"commercial", true},
		{"Commercial", true},
		{BucketClassGeneric, false},
		{"GENERIC", false},
		{"generic", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.class, func(t *testing.T) {
			b := &BucketInfo{BucketClass: tc.class}
			if got := b.IsCommercial(); got != tc.want {
				t.Errorf("IsCommercial(%q) = %v, want %v", tc.class, got, tc.want)
			}
		})
	}
	// nil safe
	var b *BucketInfo
	if b.IsCommercial() {
		t.Error("nil BucketInfo.IsCommercial() 应返 false")
	}
}
