package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestHandlerLogsDeferredToolSearchBilledUsageOnFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		failAt     int32
		disconnect bool
		wantStatus int
	}{
		{name: "rejected before model work", failAt: 1, wantStatus: http.StatusInternalServerError},
		{name: "failed after billed tool search", failAt: 2, wantStatus: http.StatusInternalServerError},
		{name: "disconnected before model work", failAt: 1, disconnect: true, wantStatus: http.StatusBadGateway},
		{name: "disconnected after billed tool search", failAt: 2, disconnect: true, wantStatus: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if calls.Add(1) >= test.failAt {
					if test.disconnect {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						if err := conn.Close(); err != nil {
							t.Error(err)
						}
						return
					}
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, `{"error":{"code":500,"message":"upstream failure","status":"INTERNAL"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"responseId":"first","modelVersion":"gemini-test","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"bifrost_tool_search","args":{"query":"select:lookup"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":12}}`)
			}))
			defer server.Close()

			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t, newTestExecutionForwarder(t), &recordingAccessKeyRPMLimiter{}, sink, "sk-first",
			)
			handler.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
			if _, err := manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups: []state.GroupConfig{{
					ConnectionType: "api_key", ID: 1, Name: "gemini", ChannelID: channel.Gemini,
					Params: json.RawMessage(`{"base_url":"` + server.URL + `/v1beta"}`),
					Models: []state.ModelConfig{{ID: "gemini-test"}}, Enabled: true,
				}},
				Credentials: []state.CredentialConfig{{
					ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
					Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
				}},
				AccessKeys: []state.AccessKeyConfig{{
					ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
					Status: state.AccessKeyStatusActive,
				}},
			}); err != nil {
				t.Fatal(err)
			}
			price := pricing.Price{NanoUSDPerMillion: 1_000_000_000, Set: true}
			table, err := pricing.NewTable([]pricing.Rule{{
				Identity: pricing.Identity{ChannelID: string(channel.Gemini), ModelID: "gemini-test"},
				Prices:   pricing.Prices{Input: price, CacheRead: price, Output: price},
			}})
			if err != nil {
				t.Fatal(err)
			}
			handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
				`{"model":"gemini-test","input":"hello","store":false,"tools":[{"type":"function","name":"lookup","defer_loading":true,"parameters":{"type":"object","properties":{}}}]}`,
			))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			events := sink.snapshot()
			if calls.Load() != test.failAt || response.Code != test.wantStatus ||
				len(events) != 1 || events[0].Status != telemetry.RequestStatusError || len(events[0].Attempts) != 1 {
				t.Fatalf("calls=%d response=%d events=%+v", calls.Load(), response.Code, events)
			}
			want := usage.Result{State: usage.StateNotApplicable}
			wantPricing := telemetry.PricingObservation{
				UpstreamModel: "gemini-test", CostState: string(pricing.CostStateNotApplicable),
				PricingCompleteness: string(pricing.CompletenessNotApplicable),
			}
			if test.failAt == 2 {
				want = usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 2}}
				wantPricing.CostState = string(pricing.CostStatePriced)
				wantPricing.PricingCompleteness = string(pricing.CompletenessPartial)
				wantPricing.EstimatedCostNanoUSD = 12_000
			}
			observation := events[0].Usage
			if observation.Result != want || withoutPricingReceipt(observation.Pricing) != wantPricing ||
				observation.GroupID != 1 || observation.ChannelID != channel.Gemini ||
				observation.CredentialID != 1 || observation.AttemptSequence != 1 {
				t.Fatalf("usage=%+v, want result=%+v pricing=%+v", observation, want, wantPricing)
			}
		})
	}
}

func TestHandlerRetainsBilledUsageOnCancellationAndTimeout(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, kind := range []execution.ErrorKind{execution.ErrorKindCanceled, execution.ErrorKindTimeout} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, kind), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				want := usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 2}}
				fail := func() execution.AttemptResult {
					if kind == execution.ErrorKindCanceled {
						cancel()
					}
					return execution.AttemptResult{
						DispatchState: execution.DispatchMaybeSent,
						Usage:         &execution.UsageEvidence{Normalized: want},
						Error:         &execution.ErrorEvidence{Kind: kind, Summary: "later upstream call failed"},
					}
				}
				executor := fakeExecutionExecutor{
					unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult { return fail() },
					stream: func(context.Context, execution.AttemptSpec, execution.StreamSink) execution.StreamResult {
						result := fail()
						return execution.StreamResult{DispatchState: result.DispatchState, Usage: result.Usage, Error: result.Error}
					},
				}
				sink := &recordingRequestLogSink{}
				engine, _, _, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(executor), &recordingAccessKeyRPMLimiter{}, sink, "sk-first")
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"gpt-4o","stream":%t}`, stream))).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)

				wantStatus, wantCode := telemetry.RequestStatusError, "upstream_timeout"
				if kind == execution.ErrorKindCanceled {
					wantStatus, wantCode = telemetry.RequestStatusCanceled, "client_canceled"
				} else if response.Code != http.StatusGatewayTimeout {
					t.Fatalf("HTTP status = %d, want 504", response.Code)
				}
				events := sink.snapshot()
				if len(events) != 1 || events[0].Status != wantStatus || events[0].ErrorCode != wantCode ||
					events[0].Usage.Result != want || events[0].Usage.CredentialID != 1 || events[0].Usage.AttemptSequence != 1 {
					t.Fatalf("events = %+v, want status=%s code=%s usage=%+v", events, wantStatus, wantCode, want)
				}
			})
		}
	}
}

func TestHandlerClassifiesConversionFailureAfterBilledWork(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, dispatch := range []execution.DispatchState{execution.DispatchNotSent, execution.DispatchMaybeSent} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, dispatch), func(t *testing.T) {
				calls := 0
				wantUsage := usage.Result{State: usage.StateNotApplicable}
				if dispatch == execution.DispatchMaybeSent {
					wantUsage = usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 2}}
				}
				fail := func() execution.AttemptResult {
					calls++
					result := execution.AttemptResult{DispatchState: dispatch, Error: &execution.ErrorEvidence{
						Kind: execution.ErrorKindConversionUnsupported, Code: execution.ErrorCodeTargetSerializationFailed,
						Summary: "target request serialization failed", OriginHint: execution.ErrorOriginInternal,
						ScopeHint: execution.ErrorScopeGroup,
					}}
					if dispatch == execution.DispatchMaybeSent {
						result.Usage = &execution.UsageEvidence{Normalized: wantUsage}
					}
					return result
				}
				executor := fakeExecutionExecutor{
					unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult { return fail() },
					stream: func(context.Context, execution.AttemptSpec, execution.StreamSink) execution.StreamResult {
						result := fail()
						return execution.StreamResult{DispatchState: result.DispatchState, Usage: result.Usage, Error: result.Error}
					},
				}
				sink := &recordingRequestLogSink{}
				engine, _, _, registry := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(executor), &recordingAccessKeyRPMLimiter{}, sink, "sk-first", "sk-second")
				before := registry.Snapshot()
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"gpt-4o","stream":%t}`, stream)))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)

				events := sink.snapshot()
				if response.Code != http.StatusUnprocessableEntity ||
					!strings.Contains(response.Body.String(), `"code":"protocol_conversion_unsupported"`) ||
					calls != 1 || len(events) != 1 || len(events[0].Attempts) != 1 {
					t.Fatalf("response=%d %s calls=%d events=%+v", response.Code, response.Body.String(), calls, events)
				}
				event, attempt := events[0], events[0].Attempts[0]
				wantRetry := telemetry.RetryNextCandidate
				if dispatch == execution.DispatchMaybeSent {
					wantRetry = telemetry.RetryNone
				}
				if event.ErrorCode != "protocol_conversion_unsupported" || event.Usage.Result != wantUsage ||
					attempt.ErrorCode != "protocol_conversion_unsupported" || attempt.DispatchState != dispatch ||
					attempt.FailureCategory != telemetry.FailureCategoryConversionUnsupported ||
					attempt.FailureOrigin != execution.ErrorOriginInternal || attempt.FailureScope != execution.ErrorScopeGroup ||
					attempt.RetryDirective != wantRetry || attempt.Effect != telemetry.EffectSkipGroup || attempt.WillRetry {
					t.Fatalf("event = %+v, want conversion failure with retry=%s usage=%+v", event, wantRetry, wantUsage)
				}
				if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
					t.Fatalf("credential health changed: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}
