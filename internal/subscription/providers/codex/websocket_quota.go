package codex

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/buger/jsonparser"
)

type websocketQuotaWindow struct {
	UsedPercent       *float64 `json:"used_percent"`
	WindowMinutes     *int64   `json:"window_minutes"`
	ResetAt           *int64   `json:"reset_at"`
	ResetAfterSeconds *int64   `json:"reset_after_seconds"`
}

type websocketQuotaRate struct {
	Allowed      *bool                 `json:"allowed"`
	LimitReached *bool                 `json:"limit_reached"`
	Primary      *websocketQuotaWindow `json:"primary"`
	Secondary    *websocketQuotaWindow `json:"secondary"`
}

// NormalizeWebsocketQuotaWindows 读取 Codex 原生额度事件，不改动发给客户端的消息。
// WS 的具名附加额度由已有快照解析来源，不能按 HTTP 响应头命名空间推导 SourceID。
func NormalizeWebsocketQuotaWindows(payload []byte, observedAt time.Time) []quotaWindow {
	kind, err := jsonparser.GetString(payload, "type")
	if err != nil || kind != "codex.rate_limits" {
		return nil
	}
	var event struct {
		RateLimits           *websocketQuotaRate            `json:"rate_limits"`
		MeteredLimitName     string                         `json:"metered_limit_name"`
		LimitName            string                         `json:"limit_name"`
		AdditionalRateLimits map[string]*websocketQuotaRate `json:"additional_rate_limits"`
	}
	if json.Unmarshal(payload, &event) != nil || len(event.AdditionalRateLimits) > 8 {
		return nil
	}
	additional := make([]quotaWindow, 0, 2*len(event.AdditionalRateLimits))
	names := make([]string, 0, len(event.AdditionalRateLimits))
	for name := range event.AdditionalRateLimits {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		windows := normalizeWebsocketQuotaRate(event.AdditionalRateLimits[name], observedAt)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, window := range windows {
			window.SourceName = name
			additional = append(additional, window)
		}
	}
	sourceID := normalizeQuotaSourceID(event.MeteredLimitName)
	if sourceID == "" {
		sourceID = normalizeQuotaSourceID(event.LimitName)
	}
	if sourceID == codexAccountActiveLimit {
		sourceID = codexAccountQuotaSourceID
	}
	primary := normalizeWebsocketQuotaRate(event.RateLimits, observedAt)
	result := make([]quotaWindow, 0, len(primary)+len(additional))
	for _, window := range primary {
		if sourceID == "" {
			if !websocketQuotaTopLevelIsAccount(window, additional) {
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

// websocketQuotaTopLevelIsAccount 用同一事件内的附加窗口排除顶层副本。
// 槽位和用量不代表身份；同周期的 reset_at 才能区分当前额度窗口。
func websocketQuotaTopLevelIsAccount(window quotaWindow, additional []quotaWindow) bool {
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
	return true
}

func sameWebsocketQuotaReset(left, right quotaWindow) bool {
	if left.ResetAtMS == nil || right.ResetAtMS == nil || *left.ResetAtMS <= 0 || *right.ResetAtMS <= 0 {
		return false
	}
	delta := *left.ResetAtMS - *right.ResetAtMS
	return delta >= -1000 && delta <= 1000
}

func normalizeWebsocketQuotaRate(rate *websocketQuotaRate, observedAt time.Time) []quotaWindow {
	if rate == nil {
		return nil
	}
	fields := map[string]any{}
	if rate.Allowed != nil {
		fields["allowed"] = *rate.Allowed
	}
	if rate.LimitReached != nil {
		fields["limit_reached"] = *rate.LimitReached
	}
	for _, slot := range []struct {
		name   string
		window *websocketQuotaWindow
	}{{"primary", rate.Primary}, {"secondary", rate.Secondary}} {
		window := slot.window
		if window == nil {
			continue
		}
		if window.UsedPercent == nil || math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) ||
			*window.UsedPercent < 0 || *window.UsedPercent > 100 || window.WindowMinutes == nil ||
			*window.WindowMinutes <= 0 || *window.WindowMinutes > passiveQuotaMaxResetAtSeconds/60 {
			continue
		}
		values := map[string]string{
			"used-percent":   strconv.FormatFloat(*window.UsedPercent, 'f', -1, 64),
			"window-minutes": strconv.FormatInt(*window.WindowMinutes, 10),
		}
		if window.ResetAt != nil {
			values["reset-at"] = strconv.FormatInt(*window.ResetAt, 10)
		}
		if window.ResetAfterSeconds != nil {
			values["reset-after-seconds"] = strconv.FormatInt(*window.ResetAfterSeconds, 10)
		}
		fields[slot.name+"_window"] = passiveQuotaWindowFields(values, observedAt)
	}
	windows := normalizeRateWindows(fields, "", "", "")
	for index := range windows {
		windows[index].Label, windows[index].LabelKey = "", ""
		windows[index].Scope, windows[index].Unit = "", ""
	}
	return windows
}
