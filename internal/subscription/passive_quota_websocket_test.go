package subscription

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestFlushWebsocketQuotaCapturedEventsKeepAccountAndSparkSeparate(t *testing.T) {
	for _, sample := range []string{"quota-ws-account.json", "quota-ws-spark.json"} {
		t.Run(sample, func(t *testing.T) {
			manager, db, registry, _, credential := newCredentialManagerFixture(t,
				credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			active, err := os.ReadFile("providers/codex/testdata/quota-active.json")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := codex.NormalizeQuota(active, nil)
			if err != nil {
				t.Fatal(err)
			}
			newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, string(raw))
			payload, err := os.ReadFile("providers/codex/testdata/" + sample)
			if err != nil {
				t.Fatal(err)
			}
			ref, _ := registry.CredentialRef(credential.ID)
			for _, observedAtMS := range []int64{2000, 3000} {
				windows := codex.NormalizeWebsocketQuotaWindows(payload, time.UnixMilli(observedAtMS))
				manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, observedAtMS, windows)
				if pending, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || pending {
					t.Fatalf("flush: pending=%v error=%v", pending, err)
				}
				var stored models.CredentialObservation
				if err := db.Take(&stored, "credential_id = ?", credential.ID).Error; err != nil {
					t.Fatal(err)
				}
				var snapshot providerobservation.Snapshot
				if err := json.Unmarshal(stored.SnapshotJSON, &snapshot); err != nil {
					t.Fatal(err)
				}
				if stored.ObservedAtMS == nil || *stored.ObservedAtMS != observedAtMS || len(snapshot.QuotaWindows) != 3 {
					t.Fatalf("quota event did not refresh the existing windows: %s", stored.SnapshotJSON)
				}
				if bytes.Contains(stored.SnapshotJSON, []byte("SourceName")) || bytes.Contains(stored.SnapshotJSON, []byte("source_name")) {
					t.Fatal("transient source hint was persisted")
				}
				for _, window := range snapshot.QuotaWindows {
					if window.SourceID == "codex" && (*window.Used != 6 || *window.Remaining != 94) {
						t.Fatalf("Spark overwrote account quota: %#v", window)
					}
					if window.SourceID == "codex_bengalfox" && (*window.Used != 0 || *window.Remaining != 100) {
						t.Fatalf("Spark quota was not retained: %#v", window)
					}
					for _, patch := range windows {
						if patch.WindowSeconds != nil && *patch.WindowSeconds == *window.WindowSeconds &&
							(patch.SourceID == window.SourceID || patch.SourceName == window.Scope) {
							if patch.ResetAtMS == nil || window.ResetAtMS == nil || *window.ResetAtMS != *patch.ResetAtMS {
								t.Fatalf("quota reset was not refreshed: source=%s period=%d", window.SourceID, *window.WindowSeconds)
							}
						}
					}
				}
			}
		})
	}
}

func TestCapturedHTTPAndWebsocketNamedQuotasAgreeAfterMatching(t *testing.T) {
	active, err := os.ReadFile("providers/codex/testdata/quota-active.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := codex.NormalizeQuota(active, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 故意留下旧的普通额度，验证 WS 不会靠数值推断无来源窗口。
	var baseline providerobservation.Snapshot
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	for index := range baseline.QuotaWindows {
		window := &baseline.QuotaWindows[index]
		if window.SourceID == "codex" {
			*window.Used, *window.Remaining, *window.Utilization = 90, 10, .9
		}
	}
	raw, err = json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"account", "spark"} {
		t.Run(name, func(t *testing.T) {
			headerJSON, err := os.ReadFile("providers/codex/testdata/quota-http-" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var headers http.Header
			if err := json.Unmarshal(headerJSON, &headers); err != nil {
				t.Fatal(err)
			}
			signals := make(map[string]string)
			for key := range headers {
				signals[key] = headers.Get(key)
			}
			payload, err := os.ReadFile("providers/codex/testdata/quota-ws-" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			observedAt := time.Unix(1789204875, 0)
			httpMerged, err := mergePassiveQuotaSnapshot(raw, codex.NormalizePassiveQuotaWindows(signals, observedAt))
			if err != nil {
				t.Fatal(err)
			}
			wsMerged, err := mergePassiveQuotaSnapshot(raw, codex.NormalizeWebsocketQuotaWindows(payload, observedAt))
			if err != nil {
				t.Fatal(err)
			}
			for index, httpWindow := range httpMerged.Windows {
				wsWindow := wsMerged.Windows[index]
				if wsWindow.SourceID == "codex" {
					if *wsWindow.Used != 90 {
						t.Fatal("WS event without source changed account quota")
					}
					continue
				}
				if httpWindow.SourceID != wsWindow.SourceID || *httpWindow.WindowSeconds != *wsWindow.WindowSeconds ||
					*httpWindow.Used != *wsWindow.Used || *httpWindow.Remaining != *wsWindow.Remaining || httpWindow.State != wsWindow.State {
					t.Fatalf("HTTP and WS disagree for source=%s period=%d", httpWindow.SourceID, *httpWindow.WindowSeconds)
				}
			}
		})
	}
}

func TestPassiveQuotaSourceNameRequiresUniqueOriginalNameAndPeriod(t *testing.T) {
	period := int64(18000)
	window := providerobservation.QuotaWindow{SourceID: "codex_bengalfox", ID: "spark-primary", Scope: "GPT-5.3-Codex-Spark", WindowSeconds: &period}
	patch := providerobservation.QuotaWindow{SourceName: window.Scope, WindowSeconds: &period}
	if got := matchPassiveQuotaWindow([]providerobservation.QuotaWindow{window}, patch); got != 0 {
		t.Fatalf("original source name did not resolve: %d", got)
	}
	for _, name := range []string{"GPT 5.3 Codex Spark", "Unknown", "account"} {
		patch.SourceName = name
		if got := matchPassiveQuotaWindow([]providerobservation.QuotaWindow{window}, patch); got != -1 {
			t.Fatalf("unconfirmed alias %q matched", name)
		}
	}
	patch.SourceName = window.Scope
	other := window
	other.SourceID = "codex_other"
	if got := matchPassiveQuotaWindow([]providerobservation.QuotaWindow{window, other}, patch); got != -1 {
		t.Fatal("ambiguous source name matched")
	}
	other.SourceID = ""
	if got := matchPassiveQuotaWindow([]providerobservation.QuotaWindow{window, other}, patch); got != -1 {
		t.Fatal("a candidate without a source ID was ignored during uniqueness checking")
	}
	if got := matchPassiveQuotaWindow([]providerobservation.QuotaWindow{other, window}, patch); got != -1 {
		t.Fatal("source-name ambiguity depends on candidate order")
	}
	patch.WindowSeconds = nil
	if got := matchPassiveQuotaWindow([]providerobservation.QuotaWindow{window}, patch); got != -1 {
		t.Fatal("source name without period matched")
	}
}

func TestMergeWebsocketQuotaPrefersNamedCopyForTheSameSourceAndPeriod(t *testing.T) {
	raw, err := codex.NormalizeQuota([]byte(`{
		"rate_limit":{"primary_window":{"used_percent":6,"limit_window_seconds":604800}},
		"additional_rate_limits":[{"limit_name":"Spark","metered_feature":"codex_bengalfox",
			"rate_limit":{"primary_window":{"used_percent":90,"limit_window_seconds":604800}}}]
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	windows := codex.NormalizeWebsocketQuotaWindows([]byte(`{
		"type":"codex.rate_limits","metered_limit_name":"codex_bengalfox",
		"rate_limits":{"secondary":{"used_percent":1,"window_minutes":10080,"reset_at":1800000001}},
		"additional_rate_limits":{"Spark":{"primary":{"used_percent":0,"window_minutes":10080,"reset_at":1800000000}}}
	}`), time.Unix(1000, 0))
	merged, err := mergePassiveQuotaSnapshot(raw, windows)
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range merged.Windows {
		want := 6.0
		if window.SourceID == "codex_bengalfox" {
			want = 0
		}
		if window.Used == nil || *window.Used != want {
			t.Fatalf("source=%s used=%v, want %.0f", window.SourceID, window.Used, want)
		}
	}
}

func TestFlushWebsocketQuotaPairPreservesEachSampleTime(t *testing.T) {
	tests := []struct {
		name                        string
		storedAt, headerAt, eventAt int64
		eventSource                 string
		wantAccount, wantSpark      float64
		wantObservedAt              int64
	}{
		{"complementary windows", 1000, 2000, 3000, "codex_bengalfox", 12, 20, 3000},
		{"active refresh between samples", 2500, 2000, 3000, "codex_bengalfox", 90, 20, 3000},
		{"active refresh ties handshake", 2000, 2000, 3000, "codex_bengalfox", 90, 20, 3000},
		{"active refresh ties event", 3000, 2000, 3000, "codex_bengalfox", 90, 5, 3000},
		{"samples in same millisecond", 1000, 2000, 2000, "codex_bengalfox", 12, 20, 2000},
		{"event supersedes same window", 1000, 2000, 3000, "codex", 20, 5, 3000},
		{"unmatched event keeps handshake time", 1000, 2000, 3000, "unknown", 12, 5, 2000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, db, registry, _, credential := newCredentialManagerFixture(t,
				credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
				`{"plan_summary":{},"quota_windows":[{"source_id":"codex","id":"account","scope":"account","window_seconds":604800,"used":90},{"source_id":"codex_bengalfox","id":"spark","scope":"GPT-5.3-Codex-Spark","window_seconds":604800,"used":5}]}`)
			if err := db.Model(&models.CredentialObservation{}).Where("credential_id = ?", credential.ID).
				Update("observed_at_ms", test.storedAt).Error; err != nil {
				t.Fatal(err)
			}
			period := int64(604800)
			header := PassiveQuotaSample{ObservedAtMS: test.headerAt, Windows: []providerobservation.QuotaWindow{
				{SourceID: "codex", WindowSeconds: &period, Used: floatPointer(12), Limit: floatPointer(100), Remaining: floatPointer(88), Utilization: floatPointer(.12)},
			}}
			event := PassiveQuotaSample{ObservedAtMS: test.eventAt, Windows: []providerobservation.QuotaWindow{
				{SourceID: test.eventSource, WindowSeconds: &period, Used: floatPointer(20), Limit: floatPointer(100), Remaining: floatPointer(80), Utilization: floatPointer(.20)},
			}}
			ref, _ := registry.CredentialRef(credential.ID)
			manager.RecordPassiveQuotaPair(credential.ID, ref.IdentityGeneration, header, event)
			if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
				t.Fatalf("flush: remaining=%t error=%v", remaining, err)
			}
			var stored models.CredentialObservation
			if err := db.Take(&stored, "credential_id = ?", credential.ID).Error; err != nil {
				t.Fatal(err)
			}
			var snapshot providerobservation.Snapshot
			if err := json.Unmarshal(stored.SnapshotJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			if stored.ObservedAtMS == nil || *stored.ObservedAtMS != test.wantObservedAt || len(snapshot.QuotaWindows) != 2 ||
				*snapshot.QuotaWindows[0].Used != test.wantAccount || *snapshot.QuotaWindows[1].Used != test.wantSpark {
				t.Fatalf("unexpected timestamp or quota values: time=%v snapshot=%s", stored.ObservedAtMS, stored.SnapshotJSON)
			}
		})
	}
}

func TestWebsocketQuotaPairCoalescesWithoutRedatingHandshake(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota(t)
	header := PassiveQuotaSample{ObservedAtMS: 1000, Windows: []providerobservation.QuotaWindow{{ID: "account", Used: floatPointer(12)}}}
	event := PassiveQuotaSample{ObservedAtMS: 2000, Windows: []providerobservation.QuotaWindow{{ID: "spark", Used: floatPointer(20)}}}
	manager.RecordPassiveQuotaPair(7, 100, header, event)
	event.ObservedAtMS = 3000
	*event.Windows[0].Used = 30
	manager.RecordPassiveQuotaPair(7, 100, header, event)
	*header.Windows[0].Used = 99
	*event.Windows[0].Used = 99
	dirty := manager.DirtyPassiveQuotaObservations(1)
	if len(dirty) != 1 || dirty[0].Preceding == nil || dirty[0].Preceding.ObservedAtMS != 1000 ||
		*dirty[0].Preceding.Windows[0].Used != 12 || dirty[0].ObservedAtMS != 3000 || *dirty[0].Windows[0].Used != 30 {
		t.Fatalf("coalescing lost the handshake time or retained caller pointers: %#v", dirty)
	}
	// HTTP 继续整份替换，不继承另一请求留下的握手样本。
	manager.RecordPassiveQuotaObservation(7, 100, 4000, []providerobservation.QuotaWindow{{ID: "http"}})
	dirty = manager.DirtyPassiveQuotaObservations(1)
	if len(dirty) != 1 || dirty[0].Preceding != nil || len(dirty[0].Windows) != 1 || dirty[0].Windows[0].ID != "http" {
		t.Fatalf("HTTP inherited a WS sample: %#v", dirty)
	}
}
