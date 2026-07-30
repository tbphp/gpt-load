package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAccessKeyMillisWireOmitsLegacyTimestampKeys(t *testing.T) {
	encoded, err := json.Marshal(AccessKeyMetadata{
		ID:          7,
		CreatedAtMS: 1_784_894_400_000,
		UpdatedAtMS: 1_784_898_000_000,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"created_at_ms":1784894400000`) ||
		!strings.Contains(text, `"updated_at_ms":1784898000000`) ||
		strings.Contains(text, `"created_at"`) ||
		strings.Contains(text, `"updated_at"`) {
		t.Fatalf("AccessKeyMetadata JSON = %s", text)
	}
}

func TestRevealMillisWireOmitsLegacyTimestampKey(t *testing.T) {
	assertManagementWireObject(
		t,
		AccessKeyRevealResult{ID: 7, Key: "secret", RevealedAtMS: 1_784_894_400_000},
		[]string{"id", "key", "revealed_at_ms"},
	)
}

func TestHealthMillisWireUsesNullableEpochMilliseconds(t *testing.T) {
	cooldownUntilMS := int64(1_784_894_460_000)
	assertManagementWireObject(
		t,
		runtimeHealthResponse{
			ObservedAtMS: 1_784_894_400_000,
			CooldownKeys: []healthProblemKeyResponse{{
				CooldownUntilMS: &cooldownUntilMS,
				Recovery: healthRecoveryResponse{
					Automatic: true,
					Mode:      "cooldown_expiry",
					AtMS:      &cooldownUntilMS,
				},
			}},
			RequestLog: requestLogHealthResponse{
				LastWriteFailureAtMS: &cooldownUntilMS,
			},
		},
		[]string{
			"observed_at_ms",
			"cooldown_keys",
			"request_log",
		},
	)
}

func TestInspectAndUpstreamMillisWireUseEpochMilliseconds(t *testing.T) {
	cooldownUntilMS := int64(1_784_894_460_000)
	assertManagementWireObject(
		t,
		routeInspectResponse{
			ObservedAtMS: 1_784_894_400_000,
			Groups: []routeInspectGroupResponse{{
				Keys: []routeInspectKeyResponse{{
					CooldownUntilMS: &cooldownUntilMS,
				}},
			}},
		},
		[]string{"observed_at_ms", "groups"},
	)
	assertManagementWireObject(
		t,
		UpstreamKeyResponse{CooldownUntilMS: &cooldownUntilMS},
		[]string{"cooldown_until_ms"},
	)
}

func TestRequestLogMillisCostWireUsesCursorV2Fields(t *testing.T) {
	assertManagementWireObject(
		t,
		requestLogCursorPayload{
			Version:       requestLogCursorV2,
			CompletedAtMS: 1_784_894_400_000,
			RequestID:     "00000000-0000-4000-8000-000000000001",
		},
		[]string{"v", "completed_at_ms", "request_id"},
	)
	assertManagementWireObject(
		t,
		requestLogItemResponse{
			CompletedAtMS:        1_784_894_400_000,
			EstimatedCostNanoUSD: "125000000",
		},
		[]string{"completed_at_ms", "estimated_cost_nano_usd"},
	)
}

func TestUsageMillisCostWireUsesIntegerBucketsAndStringCost(t *testing.T) {
	assertManagementWireObject(
		t,
		usageResponse{
			FromMS:       1_784_894_400_000,
			ToMS:         1_784_898_000_000,
			ObservedAtMS: 1_784_895_000_000,
			Summary: usageAggregateResponse{
				EstimatedCostNanoUSD: "125000000",
			},
			Series: []usageSeriesResponse{{
				BucketStartMS: 1_784_894_400_000,
				BucketEndMS:   1_784_898_000_000,
			}},
		},
		[]string{"from_ms", "to_ms", "observed_at_ms", "summary", "series"},
	)
}

func TestModelPriceWireUsesFiveNullableDecimalStrings(t *testing.T) {
	zero := "0"
	encoded, err := json.Marshal(modelPriceRuleResponse{
		Prices: modelPriceValuesResponse{
			InputPrice: &zero,
		},
		UpdatedAtMS: 1_784_894_400_000,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var document struct {
		Prices      map[string]*string `json:"prices"`
		UpdatedAtMS int64              `json:"updated_at_ms"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	wantPriceKeys := []string{
		"input_price_usd_per_million_tokens",
		"output_price_usd_per_million_tokens",
		"cache_read_price_usd_per_million_tokens",
		"cache_write_5m_price_usd_per_million_tokens",
		"cache_write_1h_price_usd_per_million_tokens",
	}
	if len(document.Prices) != len(wantPriceKeys) ||
		document.UpdatedAtMS != 1_784_894_400_000 {
		t.Fatalf("ModelPrice JSON = %s", encoded)
	}
	for _, key := range wantPriceKeys {
		if _, exists := document.Prices[key]; !exists {
			t.Fatalf("ModelPrice JSON missing %q: %s", key, encoded)
		}
	}
}

func TestIdempotencyOperationMillisWireOmitsLegacyTimestampKey(t *testing.T) {
	assertManagementWireObject(
		t,
		operationExpiredData{
			OperationID:   "00000000-0000-4000-8000-000000000001",
			OperationKind: operationKindAccessKeyCreate,
			CompletedAtMS: 1_784_894_400_000,
		},
		[]string{"operation_id", "operation_kind", "resource_identity", "completed_at_ms"},
	)
}

func assertManagementWireObject(t *testing.T, value any, requiredKeys []string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, key := range requiredKeys {
		if _, exists := document[key]; !exists {
			t.Fatalf("management wire missing %q: %s", key, encoded)
		}
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assertNoLegacyManagementWireKeys(t, decoded, encoded)
}

func assertNoLegacyManagementWireKeys(t *testing.T, value any, encoded []byte) {
	t.Helper()
	legacyKeys := map[string]struct{}{}
	for _, key := range []string{
		"created_at",
		"updated_at",
		"completed_at",
		"observed_at",
		"cooldown_until",
		"bucket_start",
		"bucket_end",
		"revealed_at",
		"from",
		"to",
		"estimated_cost" + "_usd",
	} {
		legacyKeys[key] = struct{}{}
	}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if _, legacy := legacyKeys[key]; legacy {
					t.Fatalf("management wire exposes legacy key %q: %s", key, encoded)
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
}
