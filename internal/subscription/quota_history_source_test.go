package subscription

import (
	"slices"
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestQuotaHistoryPreservesBatchedCrossTransportRebound(t *testing.T) {
	for _, namedFirst := range []bool{false, true} {
		name := "HTTP-to-WS"
		if namedFirst {
			name = "WS-to-HTTP"
		}
		t.Run(name, func(t *testing.T) {
			manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[{"id":"spark-primary","source_id":"codex_spark","scope":"Spark","label":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)
			ref, _ := registry.CredentialRef(credential.ID)
			seconds := int64(604800)
			for index, used := range []float64{.2, .9, .8} {
				window := providerobservation.QuotaWindow{ID: "primary", WindowSeconds: &seconds, Utilization: &used}
				if (index < 2) == namedFirst {
					window.SourceName = "Spark"
				} else {
					window.SourceID = "codex_spark"
				}
				manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, int64(index+1)*10000, []providerobservation.QuotaWindow{window})
			}
			for attempt := 0; attempt < 10; attempt++ {
				remaining, err := manager.FlushPassiveQuotaObservations(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if !remaining {
					break
				}
				if attempt == 9 {
					t.Fatal("history did not drain")
				}
			}
			var rows []models.CredentialQuotaHistory
			if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 || rows[1].ObservedAtMS != 20000 || rows[1].UsedBasisPoints != 9000 || rows[2].ObservedAtMS != 30000 || rows[2].UsedBasisPoints != 8000 {
				t.Fatalf("lost boundary: %+v", rows)
			}
		})
	}
}

func TestQuotaHistoryPrefersNamedCopyForSameEvent(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		order := "top-first"
		if reverse {
			order = "named-first"
		}
		t.Run(order, func(t *testing.T) {
			for _, warm := range []bool{false, true} {
				name := "cold-source-cache"
				if warm {
					name = "warm-source-cache"
				}
				t.Run(name, func(t *testing.T) {
					manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
					snapshot, err := codex.NormalizeQuota([]byte(`{"rate_limit":{"primary_window":{"used_percent":6,"limit_window_seconds":604800}},"additional_rate_limits":[{"limit_name":"Spark","metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":90,"limit_window_seconds":604800}}}]}`), nil)
					if err != nil {
						t.Fatal(err)
					}
					newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, string(snapshot))
					ref, _ := registry.CredentialRef(credential.ID)
					if warm {
						seconds, used := int64(604800), .9
						manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 10000, []providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex_bengalfox", WindowSeconds: &seconds, Utilization: &used}})
						if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
							t.Fatal(err)
						}
					}
					windows := codex.NormalizeWebsocketQuotaWindows([]byte(`{"type":"codex.rate_limits","metered_limit_name":"codex_bengalfox","rate_limits":{"secondary":{"used_percent":1,"window_minutes":10080,"reset_at":1800000001}},"additional_rate_limits":{"Spark":{"primary":{"used_percent":0,"window_minutes":10080,"reset_at":1800000000}}}}`), time.UnixMilli(30000))
					if reverse {
						slices.Reverse(windows)
					}
					manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 30000, windows)
					for attempt := 0; attempt < 10; attempt++ {
						remaining, err := manager.FlushPassiveQuotaObservations(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						if !remaining {
							break
						}
						if attempt == 9 {
							t.Fatal("history did not drain")
						}
					}
					var observation models.CredentialObservation
					if err := db.Take(&observation, "credential_id = ?", credential.ID).Error; err != nil {
						t.Fatal(err)
					}
					var row models.CredentialQuotaHistory
					if err := db.Where("source_id = ? AND observed_at_ms = ?", "codex_bengalfox", 30000).Take(&row).Error; err != nil {
						t.Fatal(err)
					}
					if row.UsedBasisPoints != 0 {
						t.Fatalf("top-level duplicate won: history=%d; real snapshot=%s", row.UsedBasisPoints, observation.SnapshotJSON)
					}
				})
			}
		})
	}
}

func TestQuotaHistoryDiscardedTopCopyDoesNotCreateRebound(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[{"id":"spark-primary","source_id":"codex_spark","scope":"Spark","label":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	seconds := int64(604_800)
	for index, used := range []float64{0, .03} {
		manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, int64(index+1)*10_000,
			[]providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex_spark", WindowSeconds: &seconds, Utilization: &used}})
		if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	manager.passiveQuota.mu.Lock()
	clear(manager.passiveQuota.historySources)
	manager.passiveQuota.mu.Unlock()
	topUsed, namedUsed := .02, .04
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 30_000, []providerobservation.QuotaWindow{
		{ID: "secondary", SourceID: "codex_spark", WindowSeconds: &seconds, Utilization: &topUsed},
		{ID: "primary", SourceName: "Spark", WindowSeconds: &seconds, Utilization: &namedUsed},
	})
	for attempt := 0; attempt < 10; attempt++ {
		remaining, err := manager.FlushPassiveQuotaObservations(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !remaining {
			break
		}
		if attempt == 9 {
			t.Fatal("history did not drain")
		}
	}
	used := .06
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 3_610_000,
		[]providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex_spark", WindowSeconds: &seconds, Utilization: &used}})
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
		t.Fatalf("hourly flush: remaining=%t error=%v", remaining, err)
	}
	var rows []models.CredentialQuotaHistory
	if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].ObservedAtMS != 10_000 || rows[1].ObservedAtMS != 20_000 || rows[1].UsedBasisPoints != 300 || rows[2].ObservedAtMS != 30_000 || rows[2].UsedBasisPoints != 400 || rows[3].ObservedAtMS != 3_610_000 || rows[3].UsedBasisPoints != 600 {
		t.Fatalf("discarded top copy changed the canonical history series: %+v", rows)
	}
}
