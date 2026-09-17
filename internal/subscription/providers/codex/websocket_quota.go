package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/buger/jsonparser"
)

// 原始绝对时间与同一事件中的倒计时分别比较，不能混入本地接收时间。
type websocketQuotaWindowAnchor struct {
	windowSeconds     int64
	resetAtSeconds    int64
	resetAfterSeconds int64
}

type websocketQuotaRateObservation struct {
	windows              []quotaWindow
	comparisonWindows    map[string]websocketQuotaWindowAnchor
	comparisonIncomplete bool
}

// NormalizeWebsocketQuotaWindows 读取 Codex 原生额度事件，不改动发给客户端的消息。
// WS 的具名附加额度由已有快照解析来源，不能按 HTTP 响应头命名空间推导 SourceID。
func NormalizeWebsocketQuotaWindows(payload []byte, observedAt time.Time) []quotaWindow {
	kind, err := jsonparser.GetString(payload, "type")
	if err != nil || kind != "codex.rate_limits" {
		return nil
	}
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
	comparisonWindows := make([]websocketQuotaWindowAnchor, 0, 2*len(additionalRates))
	comparisonIncomplete := !additionalValid
	names := make([]string, 0, len(additionalRates))
	for name := range additionalRates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, rawName := range names {
		observation := normalizeWebsocketQuotaRate(additionalRates[rawName], observedAt)
		for _, anchor := range observation.comparisonWindows {
			comparisonWindows = append(comparisonWindows, anchor)
		}
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
			if !websocketQuotaTopLevelIsAccount(primary.comparisonWindows[window.ID], comparisonWindows, comparisonIncomplete) {
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
// 槽位和用量不代表身份；同周期窗口需要在共同时间基准下排除副本。
func websocketQuotaTopLevelIsAccount(window websocketQuotaWindowAnchor, additional []websocketQuotaWindowAnchor, comparisonIncomplete bool) bool {
	// 存在无法读取周期的附加窗口时，不能证明顶层窗口不属于该来源。
	if comparisonIncomplete {
		return false
	}
	for _, candidate := range additional {
		if window.windowSeconds != candidate.windowSeconds {
			continue
		}
		compared := false
		for _, pair := range [][2]int64{
			{window.resetAtSeconds, candidate.resetAtSeconds},
			{window.resetAfterSeconds, candidate.resetAfterSeconds},
		} {
			if pair[0] == 0 || pair[1] == 0 {
				continue
			}
			compared = true
			delta := pair[0] - pair[1]
			// 任一共同基准指向副本就跳过；两种基准的判断冲突也不会写入账号。
			if delta >= -1 && delta <= 1 {
				return false
			}
		}
		if !compared {
			return false
		}
	}
	return true
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
	result.comparisonWindows = make(map[string]websocketQuotaWindowAnchor, 2)
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
		anchor := websocketQuotaWindowAnchor{windowSeconds: seconds}
		if absolute, ok := integer(window["reset_at"]); ok && absolute > 0 && absolute <= passiveQuotaMaxResetAtSeconds {
			anchor.resetAtSeconds = absolute
		}
		if relative, ok := integer(window["reset_after_seconds"]); ok && relative > 0 && relative <= passiveQuotaMaxResetAtSeconds {
			anchor.resetAfterSeconds = relative
		}
		result.comparisonWindows[slot] = anchor
		resetAtMS := websocketQuotaResetAtMS(anchor, observedAt)

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

func websocketQuotaResetAtMS(anchor websocketQuotaWindowAnchor, observedAt time.Time) *int64 {
	if anchor.resetAtSeconds > 0 {
		value := anchor.resetAtSeconds * 1000
		return &value
	}
	relative := anchor.resetAfterSeconds
	if relative == 0 {
		return nil
	}
	base := observedAt.Unix()
	if base < 0 || relative > passiveQuotaMaxResetAtSeconds-base {
		return nil
	}
	value := (base + relative) * 1000
	return &value
}
