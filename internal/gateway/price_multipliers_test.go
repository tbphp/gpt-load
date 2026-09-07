package gateway

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

func TestHandlerFreezesPriceMultipliersAndAccountsTheSameEstimate(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "ordinary"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header), RequestWritten: true,
				Body:  []byte(`{"ok":true}`),
				Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1, Output: 1}},
			}}}
			if stream {
				forwarder.results[0].Committed = true
				forwarder.results[0].Stream = StreamObservation{EndReason: StreamEndCleanEOF}
				forwarder.streamResults = forwarder.results
			}
			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first",
			)
			runtime := accessquota.NewRuntime()
			rules := []accessquota.Rule{{ID: 901, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 10_000_000}}
			if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: rules}); err != nil {
				t.Fatal(err)
			}
			handler.accessQuota = runtime
			table, err := pricing.NewTable([]pricing.Rule{{
				Identity: pricing.Identity{ChannelID: "openai", ModelID: "gpt-4o"},
				Prices: pricing.Prices{
					Input:  pricing.Price{NanoUSDPerMillion: 600_000, Set: true},
					Output: pricing.Price{NanoUSDPerMillion: 600_000, Set: true},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
			input := gatewayAccessQuotaCompileInput(handler, rules)
			setGatewayPriceMultipliers(t, &input, "2", "1")
			if _, err := manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			forwarder.onCall = func(int) {
				setGatewayPriceMultipliers(t, &input, "7", "9")
				if _, err := manager.Publish(input); err != nil {
					t.Fatal(err)
				}
			}
			forwarder.onStreamCall = func(index int, _ http.ResponseWriter) { forwarder.onCall(index) }
			body := `{"model":"gpt-4o"}`
			if stream {
				body = `{"model":"gpt-4o","stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].Usage.Pricing.EstimatedCostNanoUSD != 4 {
				t.Fatalf("frozen multiplier estimate = %#v, want original base 2 adjusted to 4", events)
			}
			var receipt struct {
				SchemaVersion    int    `json:"schema_version"`
				BaseTotalNanoUSD *int64 `json:"base_total_nano_usd"`
				PriceMultipliers struct {
					Group     string `json:"group"`
					AccessKey string `json:"access_key"`
				} `json:"price_multipliers"`
			}
			if err := json.Unmarshal([]byte(events[0].Usage.Pricing.ReceiptJSON), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.SchemaVersion != 6 || receipt.PriceMultipliers.Group != "2" || receipt.PriceMultipliers.AccessKey != "1" || receipt.BaseTotalNanoUSD == nil || *receipt.BaseTotalNanoUSD != 2 {
				t.Fatalf("frozen receipt = %#v", receipt)
			}
			view := runtime.Snapshot(1, time.Now())
			if len(view.Rules) != 1 || view.Rules[0].UsedNanoUSD != 4 {
				t.Fatalf("quota = %#v, want the same adjusted estimate", view)
			}
		})
	}
}

func setGatewayPriceMultipliers(t *testing.T, input *state.CompileInput, group, accessKey string) {
	t.Helper()
	groupMultiplier, err := pricing.ParsePriceMultiplier(group)
	if err != nil {
		t.Fatal(err)
	}
	keyMultiplier, err := pricing.ParsePriceMultiplier(accessKey)
	if err != nil {
		t.Fatal(err)
	}
	input.Groups[0].PriceMultiplier = &groupMultiplier
	input.AccessKeys[0].PriceMultiplier = &keyMultiplier
}

func TestHandlerCrossGroupRetryUsesFinalUsageGroupMultiplier(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode: http.StatusTooManyRequests, Header: make(http.Header), RequestWritten: true,
			Body:               []byte(`{"error":{"type":"rate_limit_error"}}`),
			ClassificationBody: []byte(`{"error":{"type":"rate_limit_error"}}`),
			Usage:              usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000}},
		},
		{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
			Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1000}},
		},
	}}
	sink := &recordingRequestLogSink{}
	engine, handler, manager, registry := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first", "sk-second",
	)
	entries, err := registry.SnapshotGroupCredentialEntriesExact(1, []uint{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	entries[1].GroupID = 2
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	input := gatewayAccessQuotaCompileInput(handler, nil)
	setGatewayPriceMultipliers(t, &input, "7", "1.5")
	second := input.Groups[0]
	second.ID, second.Name = 2, "second"
	secondMultiplier := pricing.PriceMultiplier(800_000)
	second.PriceMultiplier = &secondMultiplier
	input.Groups = append(input.Groups, second)
	input.Credentials = append(input.Credentials, state.CredentialConfig{
		ID: 2, GroupID: 2, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2", Status: state.CredentialStatusActive,
	})
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	handler.priceTables = &mutableGatewayPriceTableProvider{table: mustGatewayPriceTable(t, 2_000_000_000, true)}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	events := sink.snapshot()
	if response.Code != http.StatusOK || len(events) != 1 || len(events[0].Attempts) != 2 ||
		events[0].Usage.GroupID != 2 || events[0].Usage.AttemptSequence != 2 || events[0].Usage.Pricing.EstimatedCostNanoUSD != 2_400_000 {
		t.Fatalf("retry response = %d; events = %#v", response.Code, events)
	}
}
