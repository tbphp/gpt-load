package policy

import (
	"testing"
	"time"
)

// TestTimeWindowNormalAndMidnightCross 覆盖周五 22:00-02:00 跨午夜区间，
// 含左闭右开边界与跨日归属。
func TestTimeWindowNormalAndMidnightCross(t *testing.T) {
	tr, err := compileTimeRange("22:00", "02:00")
	if err != nil {
		t.Fatalf("compileTimeRange failed: %v", err)
	}
	weekdays := []int{5} // 周五
	ranges := []TimeRange{tr}

	loc := time.FixedZone("UTC+8", 8*3600)
	// 2026-10-02 是周五。
	fridayDate := time.Date(2026, 10, 2, 0, 0, 0, 0, loc)
	if fridayDate.Weekday() != time.Friday {
		t.Fatalf("sanity check: 2026-10-02 must be Friday, got %v", fridayDate.Weekday())
	}

	for _, tc := range []struct {
		name     string
		now      time.Time
		expected TruthValue
	}{
		{"周五 21:59:59 未开始", time.Date(2026, 10, 2, 21, 59, 59, 0, loc), TruthFalse},
		{"周五 22:00:00 左闭起点", time.Date(2026, 10, 2, 22, 0, 0, 0, loc), TruthTrue},
		{"周五 23:59:00 当天午夜前", time.Date(2026, 10, 2, 23, 59, 0, 0, loc), TruthTrue},
		{"周六 00:00:00 跨午夜进入次日", time.Date(2026, 10, 3, 0, 0, 0, 0, loc), TruthTrue},
		{"周六 01:00:00 合同指定包含", time.Date(2026, 10, 3, 1, 0, 0, 0, loc), TruthTrue},
		{"周六 01:59:59 右开边界前一刻", time.Date(2026, 10, 3, 1, 59, 59, 0, loc), TruthTrue},
		{"周六 02:00:00 右开边界结束", time.Date(2026, 10, 3, 2, 0, 0, 0, loc), TruthFalse},
		{"周五 01:00:00 合同指定不包含（开始日为周四）", time.Date(2026, 10, 2, 1, 0, 0, 0, loc), TruthFalse},
		{"周日 01:00:00 非周五跨午夜次日", time.Date(2026, 10, 4, 1, 0, 0, 0, loc), TruthFalse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := evaluateTimeWindow(weekdays, ranges, tc.now); got != tc.expected {
				t.Fatalf("evaluateTimeWindow at %v = %v, want %v", tc.now, got, tc.expected)
			}
		})
	}
}

func TestTimeWindowMultipleRangesAndWeekdays(t *testing.T) {
	// 工作日 09:00-12:00 与 14:00-18:00。
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
	monday := time.Date(2026, 10, 5, 0, 0, 0, 0, loc) // 周一
	for _, tc := range []struct {
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
		{10, 0, time.Sunday, TruthFalse},
		{15, 0, time.Saturday, TruthFalse},
	} {
		now := monday.AddDate(0, 0, int(tc.weekday-time.Monday)).Add(time.Duration(tc.hour)*time.Hour + time.Duration(tc.min)*time.Minute)
		if got := evaluateTimeWindow(weekdays, ranges, now); got != tc.expected {
			t.Fatalf("evaluate at %v = %v, want %v", now, got, tc.expected)
		}
	}
}

func TestTimeWindowRejectsStartEqualsEnd(t *testing.T) {
	if _, err := compileTimeRange("10:00", "10:00"); err == nil {
		t.Fatal("expected error when start == end, got nil")
	}
}

// TestTimeWindowDSTLocalDate 验证 DST 切换日按本地日历日归属。
func TestTimeWindowDSTLocalDate(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("skipping DST test: America/New_York not available")
	}
	// 2026-03-08（周日）02:00 跳到 03:00；规则为周六 22:00 至 04:00。
	r, err := compileTimeRange("22:00", "04:00")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 3, 30, 0, 0, loc)
	if got := evaluateTimeWindow([]int{6}, []TimeRange{r}, now); got != TruthTrue {
		t.Fatalf("expected DST transition Sunday 03:30 to match Saturday overnight rule, got %v", got)
	}
}

// TestTimeWindowDSTSantiagoMidnightCross 验证 AddDate 在 DST 跳变日不会漂移前一日星期。
func TestTimeWindowDSTSantiagoMidnightCross(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skip("skipping DST test: America/Santiago not available")
	}
	// 2024-09-08（周日）00:00 直接跳到 01:00；规则为周日 22:00 至 02:00。
	r, err := compileTimeRange("22:00", "02:00")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2024, 9, 9, 0, 30, 0, 0, loc) // 周一 00:30
	if got := evaluateTimeWindow([]int{0}, []TimeRange{r}, now); got != TruthTrue {
		t.Fatalf("expected Santiago DST Monday 00:30 to match Sunday overnight rule, got %v", got)
	}
}
