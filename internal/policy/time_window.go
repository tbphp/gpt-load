package policy

import (
	"fmt"
	"slices"
	"time"
)

// parseTimeHM 解析严格 ASCII 格式 "HH:MM"，禁止符号或空格，范围 00:00-23:59
func parseTimeHM(s string) (int, error) {
	v, err := time.Parse("15:04", s)
	if err != nil || v.Format("15:04") != s {
		return 0, fmt.Errorf("invalid time format %q, expected HH:MM", s)
	}
	return v.Hour()*60 + v.Minute(), nil
}

// compileTimeRange 编译并验证单个时间区间，返回 [startMin, endMin]
func compileTimeRange(start, end string) (TimeRange, error) {
	startMin, err := parseTimeHM(start)
	if err != nil {
		return TimeRange{}, fmt.Errorf("start time error: %w", err)
	}
	endMin, err := parseTimeHM(end)
	if err != nil {
		return TimeRange{}, fmt.Errorf("end time error: %w", err)
	}
	if startMin == endMin {
		return TimeRange{}, fmt.Errorf("range start %q cannot be equal to end %q", start, end)
	}
	return TimeRange{StartMin: startMin, EndMin: endMin}, nil
}

// evaluateTimeWindow 根据传入的上下文时刻 now（使用其关联的 Location，无隐式系统时钟）判断是否处于指定时间窗内
func evaluateTimeWindow(weekdays []int, ranges []TimeRange, now time.Time) TruthValue {
	nowMin := now.Hour()*60 + now.Minute()
	todayWeekday := int(now.Weekday()) // 0=Sunday, 1=Monday, ..., 6=Saturday

	// 1. 检查以今天为起始日的区间匹配
	if slices.Contains(weekdays, todayWeekday) {
		for _, r := range ranges {
			if r.StartMin < r.EndMin {
				// 普通不跨午夜区间 [start, end)
				if nowMin >= r.StartMin && nowMin < r.EndMin {
					return TruthTrue
				}
			} else {
				// 跨午夜区间当天部分 [start, 24:00)
				if nowMin >= r.StartMin {
					return TruthTrue
				}
			}
		}
	}

	// 2. 检查跨午夜区间次日凌晨部分 [00:00, end)
	// 起始日必须是本地日历的前一天（按模 7 递减，避免 AddDate 在 DST 跳时导致的日历漂移）
	yesterdayWeekday := (todayWeekday + 6) % 7
	if slices.Contains(weekdays, yesterdayWeekday) {
		for _, r := range ranges {
			if r.StartMin > r.EndMin {
				// 跨午夜区间次日凌晨部分 [00:00, end)
				if nowMin < r.EndMin {
					return TruthTrue
				}
			}
		}
	}

	return TruthFalse
}
