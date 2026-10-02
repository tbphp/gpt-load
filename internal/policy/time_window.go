package policy

import (
	"fmt"
	"time"
)

// parseTimeHM 解析严格 ASCII 格式 "HH:MM"，禁止符号或空格，范围 00:00-23:59
func parseTimeHM(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("invalid time format %q, expected HH:MM", s)
	}
	for _, idx := range []int{0, 1, 3, 4} {
		c := s[idx]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid character %q in time %q, expected digits", c, s)
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, fmt.Errorf("time %q out of range (00:00-23:59)", s)
	}
	return h*60 + m, nil
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
	if containsInt(weekdays, todayWeekday) {
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
	// 起始日必须是本地日历的前一天
	yesterday := now.AddDate(0, 0, -1)
	yesterdayWeekday := int(yesterday.Weekday())
	if containsInt(weekdays, yesterdayWeekday) {
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

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
