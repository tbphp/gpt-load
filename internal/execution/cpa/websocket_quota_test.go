package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

type quotaEventSession struct {
	execution.WebsocketSession
	event      []byte
	result     execution.WebsocketResult
	beforeEmit func()
}

type quotaTestWebsocketProvider struct {
	*codexProviderBridge
	event    []byte
	header   http.Header
	headerAt time.Time
}

func (p *quotaTestWebsocketProvider) openWebsocket(_ execution.AttemptSpec, _ providerCredential, _, _ string, observeHeaders func(http.Header, time.Time)) (execution.WebsocketSession, error) {
	return &quotaEventSession{event: p.event, result: execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Header: p.header, HeaderObservedAt: p.headerAt},
		beforeEmit: func() { observeHeaders(p.header, p.headerAt) }}, nil
}

func (s *quotaEventSession) ExecuteTurn(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
	if s.beforeEmit != nil {
		s.beforeEmit()
	}
	if err := emit(ctx, s.event); err != nil {
		return execution.WebsocketResult{Error: &execution.ErrorEvidence{Code: "consumer_failed"}}
	}
	return s.result
}

func TestWebsocketQuotaEventsRefreshBeforeTurnCompletesWithoutHeaders(t *testing.T) {
	adapter, _, _, crypt, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	manager := adapter.credentials.(*subscription.CredentialManager)
	event, err := os.ReadFile("../../subscription/providers/codex/testdata/quota-ws-account.json")
	if err != nil {
		t.Fatal(err)
	}
	session := &observedWebsocketSession{
		WebsocketSession: &quotaEventSession{event: event}, adapter: adapter, spec: validSpec(t, row, crypt),
	}
	var lastVersion uint64
	for turn := 0; turn < 2; turn++ {
		startedAt := time.Now().UnixMilli()
		result := session.ExecuteTurn(t.Context(), nil, func(_ context.Context, forwarded []byte) error {
			if !bytes.Equal(forwarded, event) {
				t.Fatal("quota observation changed the forwarded event")
			}
			dirty := manager.DirtyPassiveQuotaObservations(1)
			if len(dirty) != 1 || len(dirty[0].Windows) != 2 {
				t.Fatalf("quota event was not recorded before downstream delivery: %#v", dirty)
			}
			if dirty[0].ObservedAtMS < startedAt || dirty[0].Version <= lastVersion {
				t.Fatalf("reused WS session did not refresh quota evidence: %#v", dirty[0])
			}
			lastVersion = dirty[0].Version
			return nil
		})
		if result.Error != nil || len(result.Header) != 0 {
			t.Fatalf("unexpected WS result: %+v", result)
		}
	}
}

func TestWebsocketQuotaKeepsHandshakeWhenEventOnlyUpdatesSpark(t *testing.T) {
	for _, flushDuringTurn := range []bool{false, true} {
		t.Run(fmt.Sprintf("flush_during_turn=%t", flushDuringTurn), func(t *testing.T) {
			adapter, db, _, crypt, credential := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			manager := adapter.credentials.(*subscription.CredentialManager)
			active, err := os.ReadFile("../../subscription/providers/codex/testdata/quota-active.json")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := codex.NormalizeQuota(active, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&models.CredentialObservation{}); err != nil {
				t.Fatal(err)
			}
			storedAt := int64(1000)
			row := models.CredentialObservation{CredentialID: credential.ID, State: models.CredentialObservationFresh,
				SnapshotJSON: snapshot, ObservedAtMS: &storedAt, ObservationVersion: 1, UpdatedAtMS: storedAt}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			event, err := os.ReadFile("../../subscription/providers/codex/testdata/quota-ws-account.json")
			if err != nil {
				t.Fatal(err)
			}
			// 条件复现：实测握手未带额度，此处显式提供受支持的 HTTP 额度字段。
			provider := &quotaTestWebsocketProvider{
				codexProviderBridge: adapter.providers[channel.ProviderCodex].(*codexProviderBridge), event: event,
				headerAt: time.Now().Add(-time.Second),
				header: http.Header{
					"X-Codex-Active-Limit": {"premium"}, "X-Codex-Primary-Used-Percent": {"12"},
					"X-Codex-Primary-Window-Minutes": {"10080"}, "X-Codex-Primary-Reset-At": {"1800000000"},
				},
			}
			adapter.providers[channel.ProviderCodex] = provider
			spec := validSpec(t, credential, crypt)
			session, opened := adapter.OpenWebsocket(t.Context(), spec)
			if opened.Error != nil {
				t.Fatalf("open: %+v", opened.Error)
			}
			checkQuota := func() error {
				if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
					return fmt.Errorf("flush: remaining=%t error=%v", remaining, err)
				}
				if err := db.Take(&row, "credential_id = ?", credential.ID).Error; err != nil {
					return err
				}
				var snapshot providerobservation.Snapshot
				if err := json.Unmarshal(row.SnapshotJSON, &snapshot); err != nil {
					return err
				}
				if len(snapshot.QuotaWindows) != 3 {
					return fmt.Errorf("unexpected quota windows: %s", row.SnapshotJSON)
				}
				for _, window := range snapshot.QuotaWindows {
					if window.SourceID == "codex" && (window.Used == nil || *window.Used != 12) {
						return fmt.Errorf("handshake account quota lost: %s", row.SnapshotJSON)
					}
					if window.SourceID == "codex_bengalfox" && (window.Used == nil || *window.Used != 0) {
						return fmt.Errorf("event did not retain Spark quota: %s", row.SnapshotJSON)
					}
				}
				return nil
			}
			var quotaErr error
			result := session.ExecuteTurn(t.Context(), spec.Body, func(_ context.Context, forwarded []byte) error {
				if bytes.Equal(forwarded, event) && flushDuringTurn {
					quotaErr = checkQuota()
				}
				return nil
			})
			if result.Error != nil || quotaErr != nil {
				t.Fatalf("turn: result=%+v quota_error=%v", result, quotaErr)
			}
			if err := checkQuota(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
