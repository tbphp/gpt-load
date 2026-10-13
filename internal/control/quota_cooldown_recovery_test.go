package control

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestQuotaObservationRecoversBothCooldownLevels(t *testing.T) {
	for _, test := range []struct {
		name   string
		used   float64
		period int64
		mode   observationRefreshMode
		want   bool
	}{
		{"five points", 95, 604800, observationRefreshManual, true},
		{"less than five", 95.01, 604800, observationRefreshManual, false},
		{"different period", 0, 18000, observationRefreshManual, false},
		{"reset follow-up does not recover twice", 0, 604800, observationRefreshAfterReset, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, groupID, id := newSubscriptionCredentialFixture(t)
			now := time.Now().UTC()
			fixture.service.now = func() time.Time { return now }
			used, period := 100.0, int64(604800)
			setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
				return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{"rate_limit":{"primary_window":{"used_percent":%g,"limit_window_seconds":%d,"reset_at":%d}}}`, used, period, now.Add(7*24*time.Hour).Unix()))}, nil
			})
			if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, id); err != nil {
				t.Fatal(err)
			}
			ref, _ := fixture.registry.CredentialRef(id)
			fixture.registry.SetQuotaCooldown(ref, "", now.Add(time.Hour), now)
			fixture.registry.SetQuotaCooldown(ref, "a", now.Add(time.Hour), now)
			fixture.registry.SetQuotaCooldown(ref, "b", now.Add(time.Hour), now)
			fixture.registry.SetModelCooldown(ref, "other", now.Add(10*time.Minute), now)
			used, period, now = test.used, test.period, now.Add(time.Minute)
			if _, err := fixture.service.refreshCredentialObservation(t.Context(), groupID, id, test.mode); err != nil {
				t.Fatal(err)
			}
			view := fixture.registry.Snapshot()[0]
			for name, recovered := range map[string]bool{
				"account": view.CooldownUntil.IsZero(),
				"model a": view.ModelCooldowns["a"].IsZero(),
				"model b": view.ModelCooldowns["b"].IsZero(),
			} {
				if recovered != test.want {
					t.Errorf("%s recovered = %t, want %t", name, recovered, test.want)
				}
			}
			if view.ModelCooldowns["other"].IsZero() {
				t.Fatal("non-quota model cooldown was cleared")
			}
			if test.want {
				after, _ := fixture.registry.CredentialRef(id)
				fixture.registry.SetQuotaCooldown(after, "a", now.Add(time.Hour), now)
				now = now.Add(time.Minute)
				if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, id); err != nil {
					t.Fatal(err)
				}
				if fixture.registry.Snapshot()[0].ModelCooldowns["a"].IsZero() {
					t.Fatal("unchanged observation cleared a new cooldown")
				}
				if accepted, _ := fixture.registry.SetQuotaCooldown(ref, "a", now.Add(time.Hour), now); accepted {
					t.Fatal("pre-recovery rejection was accepted")
				}
			}
		})
	}
}

type quotaObservationMutationFunc func(uint, func())

func (fn quotaObservationMutationFunc) Do(id uint, apply func()) { fn(id, apply) }

func TestQuotaObservationPreservesRejectionBeforeCommit(t *testing.T) {
	for _, delay := range []time.Duration{0, time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			fixture, groupID, id := newSubscriptionCredentialFixture(t)
			start := time.Now().UTC().Truncate(time.Millisecond)
			now, used := start, 100.0
			fixture.service.now = func() time.Time { return now }
			setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
				return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{"rate_limit":{"primary_window":{"used_percent":%g,"limit_window_seconds":604800,"reset_at":1800000000}}}`, used))}, nil
			})
			if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, id); err != nil {
				t.Fatal(err)
			}
			ref, _ := fixture.registry.CredentialRef(id)
			until := start.Add(time.Hour)
			fixture.registry.SetQuotaCooldown(ref, "", until, start)
			fixture.registry.SetQuotaCooldown(ref, "model", until, start)
			observedAt := start.Add(time.Second)
			now, used = observedAt, 95
			coordinator := fixture.service.mutations
			fixture.service.mutations = quotaObservationMutationFunc(func(credentialID uint, finish func()) {
				// 响应已返回，模拟业务请求先取得同一凭据锁并写入新的拒绝。
				coordinator.Do(credentialID, func() {
					for _, model := range []string{"", "model"} {
						accepted, changed := fixture.registry.SetQuotaCooldown(ref, model, until, observedAt.Add(delay))
						if !accepted || changed {
							t.Fatalf("same-deadline rejection = (%t, %t)", accepted, changed)
						}
					}
				})
				now = observedAt.Add(time.Second)
				coordinator.Do(credentialID, finish)
			})
			response, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, id)
			if err != nil {
				t.Fatal(err)
			}
			if response.ObservedAtMS == nil || *response.ObservedAtMS != observedAt.UnixMilli() {
				t.Errorf("observation time = %v, want the response time %d", response.ObservedAtMS, observedAt.UnixMilli())
			}
			view := fixture.registry.Snapshot()[0]
			if !view.CooldownUntil.Equal(until) || !view.ModelCooldowns["model"].Equal(until) {
				t.Error("active observation cleared a rejection recorded after the response")
			}
			after, _ := fixture.registry.CredentialRef(id)
			if after.QuotaCooldownGeneration != ref.QuotaCooldownGeneration {
				t.Error("stale active observation advanced the quota recovery generation")
			}
		})
	}
}

func TestQuotaObservationRecoveryIgnoresFailurePartialAndConcurrentBaseline(t *testing.T) {
	for _, mode := range []string{"failure", "partial", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			fixture, groupID, id := newSubscriptionCredentialFixture(t)
			setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
				return codex.AccountObservation{Payload: []byte(`{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":1800000000}}}`)}, nil
			})
			if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, id); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			ref, _ := fixture.registry.CredentialRef(id)
			fixture.registry.SetQuotaCooldown(ref, "a", now.Add(time.Hour), now)
			fixture.service.observeSubscriptionAccount = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
				if mode == "failure" {
					return subscriptionruntime.Observation{}, errors.New("upstream unavailable")
				}
				payload := []byte(`{"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","state":"available","used":0,"window_seconds":604800}]}`)
				if mode == "concurrent" {
					if err := fixture.db.Model(&models.CredentialObservation{}).Where("credential_id = ?", id).Updates(map[string]any{"snapshot_json": models.JSON(payload), "observation_version": 2}).Error; err != nil {
						t.Fatal(err)
					}
				}
				return subscriptionruntime.Observation{Payload: payload, Partial: mode == "partial", QuotaObserved: mode != "partial"}, nil
			}
			_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, id)
			if (err != nil) != (mode == "failure") {
				t.Fatalf("refresh error = %v", err)
			}
			if fixture.registry.Snapshot()[0].ModelCooldowns["a"].IsZero() {
				t.Fatal("invalid recovery cleared model cooldown")
			}
		})
	}
}

func TestResetCreditRecoveryDoesNotClearNewRejectionDuringQuotaFollowUp(t *testing.T) {
	fixture, groupID, id := newSubscriptionCredentialFixture(t)
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":1800000000}}}`)}, nil
	})
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, id); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ref, _ := fixture.registry.CredentialRef(id)
	fixture.registry.SetQuotaCooldown(ref, "", now.Add(time.Hour), now)
	fixture.registry.SetQuotaCooldown(ref, "old", now.Add(time.Hour), now)
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		current, _ := fixture.registry.CredentialRef(id)
		fixture.registry.SetQuotaCooldown(current, "new", now.Add(time.Hour), now)
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1800000000}}}`)}, nil
	})
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, id, resetCreditTestKey); err != nil {
		t.Fatal(err)
	}
	view := fixture.registry.Snapshot()[0]
	if !view.CooldownUntil.IsZero() || !view.ModelCooldowns["old"].IsZero() || view.ModelCooldowns["new"].IsZero() {
		t.Fatalf("reset follow-up cleared the wrong cooldowns: %#v", view)
	}
}
