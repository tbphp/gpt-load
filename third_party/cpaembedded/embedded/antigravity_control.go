package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	antigravityauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/antigravity"
)

const maxAntigravityModels = 20_000

// AntigravityModel is one model that the authenticated account actually
// exposes. It is intentionally not backed by CPA's pinned static registry.
type AntigravityModel struct {
	ID              string
	DisplayName     string
	MaxTokens       int64
	MaxOutputTokens int64
}

// AntigravityCredit is a display-only Google One AI credit balance. Amount is
// remaining credit; MinimumAmount is the upstream activation threshold, not a
// total quota or utilization denominator.
type AntigravityCredit struct {
	Amount        float64
	MinimumAmount float64
}

// AntigravityAccountObservation is the bounded, control-plane account view
// consumed by GPT-Load's provider-neutral observation normalizer.
type AntigravityAccountObservation struct {
	PlanID             string
	CurrentTierID      string
	GoogleOneAICredits *AntigravityCredit
}

var antigravityExcludedModelIDs = map[string]struct{}{
	"chat_20706":                  {},
	"chat_23310":                  {},
	"tab_flash_lite_preview":      {},
	"tab_jump_flash_lite_preview": {},
	"gemini-2.5-flash-thinking":   {},
	"gemini-2.5-pro":              {},
}

// DiscoverAntigravityModels retrieves the live, account-bound model map.
func DiscoverAntigravityModels(
	ctx context.Context,
	credential AntigravityCredential,
	options AntigravityOptions,
) ([]AntigravityModel, error) {
	if err := validateAntigravityCredential(credential); err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(options.FetchModelsURL)
	if endpoint == "" {
		endpoint = antigravityExecutionBase + "/" + antigravityauth.APIVersion + ":fetchAvailableModels"
	}
	payload, err := json.Marshal(map[string]string{"project": credential.ProjectID})
	if err != nil {
		return nil, err
	}
	defer clear(payload)
	body, err := fetchAntigravityJSON(ctx, endpoint, credential.AccessToken, payload, "fetchAvailableModels", options)
	if err != nil {
		return nil, err
	}
	defer clear(body)
	var response struct {
		Models map[string]struct {
			DisplayName     string `json:"displayName"`
			MaxTokens       int64  `json:"maxTokens"`
			MaxOutputTokens int64  `json:"maxOutputTokens"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Models == nil {
		return nil, fmt.Errorf("decode Antigravity models response")
	}
	if len(response.Models) > maxAntigravityModels {
		return nil, fmt.Errorf("Antigravity models response exceeds limit")
	}
	models := make([]AntigravityModel, 0, len(response.Models))
	for id, details := range response.Models {
		id = strings.TrimSpace(id)
		if !validAntigravityModelID(id) {
			continue
		}
		if _, excluded := antigravityExcludedModelIDs[id]; excluded {
			continue
		}
		models = append(models, AntigravityModel{
			ID:              id,
			DisplayName:     boundedAntigravityDisplayName(details.DisplayName),
			MaxTokens:       positiveAntigravityLimit(details.MaxTokens),
			MaxOutputTokens: positiveAntigravityLimit(details.MaxOutputTokens),
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// ObserveAntigravityAccount reads only plan and display-only Google One AI
// credit information. It never invents reset windows or utilization values.
func ObserveAntigravityAccount(
	ctx context.Context,
	credential AntigravityCredential,
	options AntigravityOptions,
) (AntigravityAccountObservation, error) {
	if err := validateAntigravityCredential(credential); err != nil {
		return AntigravityAccountObservation{}, err
	}
	endpoint := strings.TrimSpace(options.LoadCodeAssistURL)
	if endpoint == "" {
		endpoint = antigravityauth.APIEndpoint + "/" + antigravityauth.APIVersion + ":loadCodeAssist"
	}
	payload, err := json.Marshal(map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}})
	if err != nil {
		return AntigravityAccountObservation{}, err
	}
	defer clear(payload)
	body, err := fetchAntigravityJSON(ctx, endpoint, credential.AccessToken, payload, "loadCodeAssist", options)
	if err != nil {
		return AntigravityAccountObservation{}, err
	}
	defer clear(body)
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil || response == nil {
		return AntigravityAccountObservation{}, fmt.Errorf("decode Antigravity loadCodeAssist response")
	}
	currentTierID := antigravityNestedID(response, "currentTier")
	paidTier, paidTierPresent := response["paidTier"].(map[string]any)
	planID := ""
	if paidTierPresent {
		planID = antigravityMapString(paidTier, "id")
	}
	if planID == "" {
		planID = currentTierID
	}
	return AntigravityAccountObservation{
		PlanID:             planID,
		CurrentTierID:      currentTierID,
		GoogleOneAICredits: antigravityGoogleOneAICredits(paidTier),
	}, nil
}

func validAntigravityModelID(value string) bool {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func boundedAntigravityDisplayName(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func positiveAntigravityLimit(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func antigravityNestedID(payload map[string]any, key string) string {
	value, _ := payload[key].(map[string]any)
	return antigravityMapString(value, "id")
}

func antigravityMapString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func antigravityGoogleOneAICredits(paidTier map[string]any) *AntigravityCredit {
	if paidTier == nil {
		return nil
	}
	credits, ok := paidTier["availableCredits"].([]any)
	if !ok {
		return nil
	}
	for _, raw := range credits {
		credit, ok := raw.(map[string]any)
		if !ok || !strings.EqualFold(antigravityMapString(credit, "creditType"), "GOOGLE_ONE_AI") {
			continue
		}
		amount, amountOK := antigravityFloat(credit["creditAmount"])
		minimum, minimumOK := antigravityFloat(credit["minimumCreditAmountForUsage"])
		if !amountOK || !minimumOK || amount < 0 || minimum < 0 {
			return nil
		}
		return &AntigravityCredit{Amount: amount, MinimumAmount: minimum}
	}
	return nil
}

func antigravityFloat(value any) (float64, bool) {
	var result float64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		result = parsed
	case float64:
		result = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		result = parsed
	default:
		return 0, false
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0, false
	}
	return result, true
}
