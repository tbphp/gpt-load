package policy

import (
	"testing"
	"time"
)

func TestTimeWindowNormalAndMidnightCross(t *testing.T) {
	// 规则：周五 (5) 22:00 至 02:00（跨午夜）
	tr, err := compileTimeRange("22:00", "02:00")
	if err != nil {
		t.Fatalf("compileTimeRange failed: %v", err)
	}
	weekdays := []int{5} // 周五
	ranges := []TimeRange{tr}

	loc := time.FixedZone("UTC+8", 8*3600)

	// 2026-10-02 是周五
	fridayDate := time.Date(2026, 10, 2, 0, 0, 0, 0, loc)
	if fridayDate.Weekday() != time.Friday {
		t.Fatalf("sanity check: 2026-10-02 must be Friday, got %v", fridayDate.Weekday())
	}

	tests := []struct {
		name     string
		now      time.Time
		expected TruthValue
	}{
		{
			name:     "周五 21:59:59 (未开始)",
			now:      time.Date(2026, 10, 2, 21, 59, 59, 0, loc),
			expected: TruthFalse,
		},
		{
			name:     "周五 22:00:00 (左闭区间起点)",
			now:      time.Date(2026, 10, 2, 22, 0, 0, 0, loc),
			expected: TruthTrue,
		},
		{
			name:     "周五 23:59:00 (当天午夜前)",
			now:      time.Date(2026, 10, 2, 23, 59, 0, 0, loc),
			expected: TruthTrue,
		},
		{
			name:     "周六 00:00:00 (跨午夜进入次日凌晨)",
			now:      time.Date(2026, 10, 3, 0, 0, 0, 0, loc),
			expected: TruthTrue,
		},
		{
			name:     "周六 01:00:00 (合同指定：包括周六01:00)",
			now:      time.Date(2026, 10, 3, 1, 0, 0, 0, loc),
			expected: TruthTrue,
		},
		{
			name:     "周六 01:59:59 (右开边界前一刻)",
			now:      time.Date(2026, 10, 3, 1, 59, 59, 0, loc),
			expected: TruthTrue,
		},
		{
			name:     "周六 02:00:00 (右开边界结束)",
			now:      time.Date(2026, 10, 3, 2, 0, 0, 0, loc),
			expected: TruthFalse,
		},
		{
			name:     "周五 01:00:00 (合同指定：不包括周五01:00，因开始日为周四)",
			now:      time.Date(2026, 10, 2, 1, 0, 0, 0, loc),
			expected: TruthFalse,
		},
		{
			name:     "周日 01:00:00 (非周五跨午夜次日)",
			now:      time.Date(2026, 10, 4, 1, 0, 0, 0, loc),
			expected: TruthFalse,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateTimeWindow(weekdays, ranges, tc.now)
			if got != tc.expected {
				t.Fatalf("evaluateTimeWindow at %v = %v, want %v", tc.now, got, tc.expected)
			}
		})
	}
}

func TestTimeWindowMultipleRangesAndWeekdays(t *testing.T) {
	// 工作日 09:00-12:00 与 14:00-18:00
	r1, err := compileTimeRange("09:00", "12:00")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := compileTimeRange("14:00", "18:00")
	if err != nil {
		t.Fatal(err)
	}
	weekdays := []int{1, 2, 3, 4, 5}
	ranges := []TimeRange{r1, r2}

	loc := time.UTC
	// 2026-10-05 是周一
	monday := time.Date(2026, 10, 5, 0, 0, 0, 0, loc)

	tests := []struct {
		hour, min int
		weekday   time.Weekday
		expected  TruthValue
	}{
		{8, 59, time.Monday, TruthFalse},
		{9, 0, time.Monday, TruthTrue},
		{11, 59, time.Monday, TruthTrue},
		{12, 0, time.Monday, TruthFalse}, // 右开
		{13, 0, time.Monday, TruthFalse},
		{14, 0, time.Monday, TruthTrue},
		{17, 59, time.Monday, TruthTrue},
		{18, 0, time.Monday, TruthFalse},
		// 周末不命中
		{10, 0, time.Sunday, TruthFalse},
		{15, 0, time.Saturday, TruthFalse},
	}

	for _, tc := range tests {
		now := monday.AddDate(0, 0, int(tc.weekday-time.Monday)).Add(time.Duration(tc.hour)*time.Hour + time.Duration(tc.min)*time.Minute)
		got := evaluateTimeWindow(weekdays, ranges, now)
		if got != tc.expected {
			t.Fatalf("evaluate at %v = %v, want %v", now, got, tc.expected)
		}
	}
}

func TestTimeWindowRejectsStartEqualsEnd(t *testing.T) {
	_, err := compileTimeRange("10:00", "10:00")
	if err == nil {
		t.Fatal("expected error when start == end, got nil")
	}
}

func TestTimeWindowDSTLocalDate(t *testing.T) {
	// 加载真实 DST 时区，例如 America/New_York
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("skipping DST test: America/New_York not available")
	}

	// 2026 年美国夏令时切换日为 2026-03-08（周日），从 02:00 跳到 03:00
	// 假设规则为周六 (6) 22:00 至 04:00 跨午夜
	r, err := compileTimeRange("22:00", "04:00")
	if err != nil {
		t.Fatal(err)
	}
	weekdays := []int{6} // 周六
	ranges := []TimeRange{r}

	// 周日凌晨 03:30（因为 02:00~03:00 被跳过，03:30 实际存在）
	now := time.Date(2026, 3, 8, 3, 30, 0, 0, loc)
	got := evaluateTimeWindow(weekdays, ranges, now)
	if got != TruthTrue {
		t.Fatalf("expected DST transition Sunday 03:30 to match Saturday overnight rule, got %v", got)
	}
}
