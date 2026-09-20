package automodel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/pricing"
)

type decisionDoer func(*http.Request) (*http.Response, error)

func (call decisionDoer) Do(request *http.Request) (*http.Response, error) { return call(request) }

func TestDecideUsesProviderContractAndQuotesOnce(t *testing.T) {
	for _, provider := range []string{"typesafe", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			config := DefaultConfig()
			config.Provider = provider
			config.APIKey = "secret"
			if provider == "openrouter" {
				config.Model = "~typesafe/jev-latest"
			}
			config.Models = []Entry{Template()}
			compiled, err := Compile(config, nil)
			if err != nil {
				t.Fatal(err)
			}
			entry, _ := compiled.Lookup("auto")
			calls := 0
			client := decisionDoer(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Header.Get("Authorization") != "Bearer secret" {
					t.Fatal("missing provider authentication")
				}
				want := "https://api.typesafe.ai/v1/systemone"
				if provider == "openrouter" {
					want = "https://openrouter.ai/api/alpha/decisions"
				}
				if request.URL.String() != want {
					t.Fatalf("endpoint = %s", request.URL)
				}
				body, _ := io.ReadAll(request.Body)
				if strings.Contains(string(body), "gpt-5.6") || strings.Contains(string(body), `"uncertain"`) || !strings.Contains(string(body), "最新任务") {
					t.Fatalf("decision payload violates task/preset boundary: %s", body)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"model":"jev-pinned","answers":{"preset":{"choice":"medium","confidence":0.9}},"usage":{"input_tokens":2000,"output_tokens":10,"cost":0.000084}}`))}, nil
			})
			multiplier, _ := pricing.ParsePriceMultiplier("2")
			result := Decide(context.Background(), client, compiled, entry.Presets, TaskState{CurrentTask: "最新任务"}, multiplier)
			if result.Status != "selected" || result.Choice != "medium" || calls != 1 || result.EstimatedCostNanoUSD != 168000 || result.ProviderCostUSD != "0.000084" {
				encoded, _ := json.Marshal(result)
				t.Fatalf("decision = %s, calls=%d", encoded, calls)
			}
		})
	}
}

func TestDecideAcceptsValidChoiceRegardlessOfConfidence(t *testing.T) {
	config := DefaultConfig()
	config.APIKey = "secret"
	config.Models = []Entry{Template()}
	compiled, err := Compile(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := compiled.Lookup("auto")
	client := decisionDoer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"low","confidence":0.01}},"usage":{"input_tokens":100,"output_tokens":1}}`))}, nil
	})
	result := Decide(t.Context(), client, compiled, entry.Presets, TaskState{CurrentTask: "task"}, pricing.DefaultPriceMultiplier)
	if result.Status != "selected" || result.Source != "jev" || result.Choice != "low" || result.Confidence == nil || *result.Confidence != 0.01 {
		t.Fatalf("decision = %#v", result)
	}
}

func TestDecideKeepsBilledUsageOnInvalidSelection(t *testing.T) {
	compiled, _ := Compile(DefaultConfig(), nil)
	client := decisionDoer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"unconfigured","confidence":0.9}},"usage":{"input_tokens":2000,"output_tokens":0}}`))}, nil
	})
	result := Decide(t.Context(), client, compiled, nil, TaskState{CurrentTask: "hello"}, pricing.DefaultPriceMultiplier)
	if result.Status != "fallback" || result.Reason != "invalid_choice" || result.EstimatedCostNanoUSD != 84000 {
		t.Fatalf("invalid selection discarded billed usage: %#v", result)
	}
}

func TestDecideRetainsUsageOnMalformedAnswerShape(t *testing.T) {
	compiled, _ := Compile(DefaultConfig(), nil)
	client := decisionDoer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":"invalid","usage":{"input_tokens":2000,"output_tokens":0}}`))}, nil
	})
	result := Decide(t.Context(), client, compiled, nil, TaskState{CurrentTask: "task"}, pricing.DefaultPriceMultiplier)
	if result.Status != "fallback" || result.EstimatedCostNanoUSD != 84000 {
		t.Fatalf("malformed answers discarded known usage: %#v", result)
	}
}
