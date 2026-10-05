package codex

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/buger/jsonparser"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func normalizeCredits(payload map[string]any) *providerobservation.CreditSummary {
	result := &providerobservation.CreditSummary{}
	var balance string
	switch value := payload["balance"].(type) {
	case string:
		balance = strings.TrimSpace(value)
	case json.Number:
		balance = value.String()
	}
	// 沿用有限数值校验，但不把原始余额转换为浮点数保存。
	if len(balance) <= 128 {
		if _, valid := passiveQuotaFloat(balance); valid {
			result.Balance = balance
		}
	}
	if value, ok := firstValue(payload, "has_credits", "hasCredits").(bool); ok {
		result.HasCredits = &value
	}
	if value, ok := payload["unlimited"].(bool); ok {
		result.Unlimited = &value
	}
	if result.Balance == "" && result.HasCredits == nil && result.Unlimited == nil {
		return nil
	}
	return result
}

// NormalizePassiveCredits 从 HTTP 响应头读取账号点数，不依赖额度窗口是否存在。
func NormalizePassiveCredits(signals map[string]string, observedAt time.Time) *providerobservation.CreditSummary {
	payload := make(map[string]any, 3)
	for name, value := range signals {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "x-codex-credits-balance":
			payload["balance"] = value
		case "x-codex-credits-has-credits":
			if parsed, ok := passiveQuotaBool(value); ok {
				payload["has_credits"] = parsed
			}
		case "x-codex-credits-unlimited":
			if parsed, ok := passiveQuotaBool(value); ok {
				payload["unlimited"] = parsed
			}
		}
	}
	return stampCredits(normalizeCredits(payload), observedAt)
}

// NormalizeWebsocketCredits 读取原生额度事件中的账号点数，不与具名窗口累加。
func NormalizeWebsocketCredits(payload []byte, observedAt time.Time) *providerobservation.CreditSummary {
	kind, err := jsonparser.GetString(payload, "type")
	if err != nil || kind != "codex.rate_limits" {
		return nil
	}
	event, ok := decodeWebsocketQuotaEvent(payload)
	if !ok {
		return nil
	}
	credits, ok := object(event["credits"])
	if !ok {
		return nil
	}
	return stampCredits(normalizeCredits(credits), observedAt)
}

func stampCredits(credits *providerobservation.CreditSummary, observedAt time.Time) *providerobservation.CreditSummary {
	if credits != nil && !observedAt.IsZero() {
		at := observedAt.UnixMilli()
		credits.ObservedAtMS = &at
	}
	return credits
}
