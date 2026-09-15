package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

type websocketQuotaRateObservation struct {
	windows              []quotaWindow
	comparisonWindows    []quotaWindow
	comparisonIncomplete bool
}

// NormalizeWebsocketQuotaWindows 读取 Codex 原生额度事件，不改动发给客户端的消息。
// WS 的具名附加额度由已有快照解析来源，不能按 HTTP 响应头命名空间推导 SourceID。
func NormalizeWebsocketQuotaWindows(payload []byte, observedAt time.Time) []quotaWindow {
	event, ok := decodeWebsocketQuotaEvent(payload)
	if !ok || cleanString(event["type"]) != "codex.rate_limits" {
		return nil
	}

	var additionalRates map[string]any
	additionalValid := true
	if rawAdditional, present := event["additional_rate_limits"]; present && rawAdditional != nil {
		additionalRates, additionalValid = object(rawAdditional)
	}
	if additionalValid && len(additionalRates) > 8 {
		return nil
	}
	additional := make([]quotaWindow, 0, 2*len(additionalRates))
	comparisonWindows := make([]quotaWindow, 0, 2*len(additionalRates))
	comparisonIncomplete := !additionalValid
	names := make([]string, 0, len(additionalRates))
	for name := range additionalRates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, rawName := range names {
		observation := normalizeWebsocketQuotaRate(additionalRates[rawName], observedAt)
		comparisonWindows = append(comparisonWindows, observation.comparisonWindows...)
		comparisonIncomplete = comparisonIncomplete || observation.comparisonIncomplete
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		for _, window := range observation.windows {
			window.SourceName = name
			additional = append(additional, window)
		}
	}

	sourceID := normalizeQuotaSourceID(cleanString(event["metered_limit_name"]))
	if sourceID == "" {
		sourceID = normalizeQuotaSourceID(cleanString(event["limit_name"]))
	}
	if sourceID == codexAccountActiveLimit {
		sourceID = codexAccountQuotaSourceID
	}

	primary := normalizeWebsocketQuotaRate(event["rate_limits"], observedAt)
	result := make([]quotaWindow, 0, len(primary.windows)+len(additional))
	for _, window := range primary.windows {
		if sourceID == "" {
			if !websocketQuotaTopLevelIsAccount(window, comparisonWindows, comparisonIncomplete) {
				continue
			}
			window.SourceID = codexAccountQuotaSourceID
		} else {
			window.SourceID = sourceID
		}
		result = append(result, window)
	}
	// 具名来源仍由已有快照解析 SourceID；顶层副本已在本事件内完成去重。
	return append(result, additional...)
}

func decodeWebsocketQuotaEvent(payload []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var event map[string]any
	if decoder.Decode(&event) != nil || event == nil {
		return nil, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, false
	}
	return event, true
}

// websocketQuotaTopLevelIsAccount 用同一事件内的附加窗口排除顶层副本。
// 槽位和用量不代表身份；同周期的 reset_at 才能区分当前额度窗口。
func websocketQuotaTopLevelIsAccount(window quotaWindow, additional []quotaWindow, comparisonIncomplete bool) bool {
	for _, candidate := range additional {
		if window.WindowSeconds == nil || candidate.WindowSeconds == nil ||
			*window.WindowSeconds != *candidate.WindowSeconds {
			continue
		}
		// 同周期附加窗口存在但缺少锚点时无法完成比较，保守跳过顶层窗口。
		if window.ResetAtMS == nil || candidate.ResetAtMS == nil {
			return false
		}
		if sameWebsocketQuotaReset(window, candidate) {
			return false
		}
	}
	// 存在无法读取周期的附加窗口时，不能证明顶层窗口不属于该来源。
	return !comparisonIncomplete
}

func sameWebsocketQuotaReset(left, right quotaWindow) bool {
	if left.ResetAtMS == nil || right.ResetAtMS == nil || *left.ResetAtMS <= 0 || *right.ResetAtMS <= 0 {
		return false
	}
	delta := *left.ResetAtMS - *right.ResetAtMS
	return delta >= -1000 && delta <= 1000
}

// normalizeWebsocketQuotaRate 分开保留来源比较所需的窗口锚点和可写入的额度值。
// 单个窗口的用量损坏不会抹掉其周期身份，也不会阻断同一事件里的其他有效窗口。
func normalizeWebsocketQuotaRate(raw any, observedAt time.Time) websocketQuotaRateObservation {
	var result websocketQuotaRateObservation
	if raw == nil {
		return result
	}
	rate, valid := object(raw)
	if !valid {
		result.comparisonIncomplete = true
		return result
	}
	allowed, hasAllowed := rate["allowed"].(bool)
	limitReached, hasLimitReached := rate["limit_reached"].(bool)
	for _, slot := range []string{"primary", "secondary"} {
		rawWindow, exists := rate[slot]
		if !exists || rawWindow == nil {
			continue
		}
		window, valid := object(rawWindow)
		if !valid {
			result.comparisonIncomplete = true
			continue
		}
		minutes, ok := integer(window["window_minutes"])
		if !ok || minutes <= 0 || minutes > passiveQuotaMaxResetAtSeconds/60 {
			result.comparisonIncomplete = true
			continue
		}
		seconds := minutes * 60
		resetAtMS := websocketQuotaResetAtMS(window, observedAt)
		result.comparisonWindows = append(result.comparisonWindows, quotaWindow{
			WindowSeconds: &seconds,
			ResetAtMS:     resetAtMS,
		})

		used, ok := number(window["used_percent"])
		if !ok || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > 100 {
			continue
		}
		limit, remaining, utilization := 100.0, 100-used, used/100
		state := "available"
		if used >= 100 || (hasAllowed && !allowed) || (hasLimitReached && limitReached) {
			state = "exhausted"
		}
		result.windows = append(result.windows, quotaWindow{
			ID:            slot,
			Used:          &used,
			Limit:         &limit,
			Remaining:     &remaining,
			Utilization:   &utilization,
			ResetAtMS:     resetAtMS,
			WindowSeconds: &seconds,
			State:         state,
		})
	}
	return result
}

func websocketQuotaResetAtMS(window map[string]any, observedAt time.Time) *int64 {
	if absolute, ok := integer(window["reset_at"]); ok &&
		absolute > 0 && absolute <= passiveQuotaMaxResetAtSeconds {
		value := absolute * 1000
		return &value
	}
	relative, ok := integer(window["reset_after_seconds"])
	if !ok || relative <= 0 {
		return nil
	}
	base := observedAt.Unix()
	if base < 0 || relative > passiveQuotaMaxResetAtSeconds-base {
		return nil
	}
	value := (base + relative) * 1000
	return &value
}
