package observation

import "math"

// QuotaRecovered 判断唯一匹配的窗口是否恢复了至少 5 个百分点。
func QuotaRecovered(previous, current []QuotaWindow) bool {
	for _, next := range current {
		if next.State != "available" {
			continue
		}
		before := uniqueRecoveryWindow(previous, next)
		if before == nil || before.Unit != next.Unit || uniqueRecoveryWindow(current, next) == nil {
			continue
		}
		oldUsed, oldOK := usedPercent(*before)
		newUsed, newOK := usedPercent(next)
		// 百分比转换可能产生浮点尾差；容差仅覆盖机器精度。
		if oldOK && newOK && oldUsed-newUsed >= 5-1e-9 {
			return true
		}
	}
	return false
}

func uniqueRecoveryWindow(windows []QuotaWindow, target QuotaWindow) *QuotaWindow {
	var matched *QuotaWindow
	for index := range windows {
		window := &windows[index]
		if target.SourceID != "" {
			if window.SourceID != target.SourceID || target.WindowSeconds == nil || *target.WindowSeconds <= 0 ||
				window.WindowSeconds == nil || *window.WindowSeconds != *target.WindowSeconds {
				continue
			}
		} else if window.SourceID != "" || target.ID == "" || window.ID != target.ID || window.Scope != target.Scope {
			continue
		}
		if (window.WindowSeconds == nil) != (target.WindowSeconds == nil) ||
			window.WindowSeconds != nil && *window.WindowSeconds != *target.WindowSeconds {
			continue
		}
		if matched != nil {
			return nil
		}
		matched = window
	}
	return matched
}

func usedPercent(window QuotaWindow) (float64, bool) {
	var value float64
	switch {
	case window.Unit == "percent" && window.Used != nil:
		value = *window.Used
	case window.Utilization != nil:
		value = *window.Utilization * 100
	case window.Remaining != nil && window.Limit != nil && *window.Limit > 0:
		value = (1 - *window.Remaining / *window.Limit) * 100
	default:
		return 0, false
	}
	return value, !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}
