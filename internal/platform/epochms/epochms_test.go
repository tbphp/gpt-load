package epochms

import (
	"math"
	"testing"
	"time"
)

func TestFromTimeNormalizesToUTCAndTruncatesToMilliseconds(t *testing.T) {
	value := time.Date(2026, 7, 30, 8, 9, 10, 987_654_321, time.FixedZone("UTC+8", 8*60*60))

	got, err := FromTime(value)
	if err != nil {
		t.Fatalf("FromTime() error = %v", err)
	}
	if want := int64(1_785_370_150_987); got != want {
		t.Fatalf("FromTime() = %d, want %d", got, want)
	}
}

func TestToTimeRestoresInstantAtMillisecondPrecision(t *testing.T) {
	got, err := ToTime(1_785_370_150_987)
	if err != nil {
		t.Fatalf("ToTime() error = %v", err)
	}
	want := time.Date(2026, 7, 30, 0, 9, 10, 987_000_000, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("ToTime() = %s (%s), want %s (UTC)", got, got.Location(), want)
	}
}

func TestAlignDownUsesUnixEpochBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		value, width int64
		want         int64
	}{
		{name: "hour", value: 1_785_370_150_987, width: MillisecondsPerHour, want: 1_785_369_600_000},
		{name: "day", value: 1_785_370_150_987, width: MillisecondsPerDay, want: 1_785_369_600_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AlignDown(tt.value, tt.width)
			if err != nil {
				t.Fatalf("AlignDown() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("AlignDown(%d, %d) = %d, want %d", tt.value, tt.width, got, tt.want)
			}
		})
	}
}

func TestEpochMillisecondsRejectInvalidPersistentTimeAndWindowOverflow(t *testing.T) {
	if _, err := FromTime(time.Unix(-1, 0)); err == nil {
		t.Fatal("FromTime() accepted a negative instant")
	}
	if _, err := ToTime(-1); err == nil {
		t.Fatal("ToTime() accepted a negative instant")
	}
	for _, width := range []int64{0, -1} {
		if _, err := AlignDown(1, width); err == nil {
			t.Fatalf("AlignDown() accepted width %d", width)
		}
	}
	if _, err := AlignDown(-1, MillisecondsPerHour); err == nil {
		t.Fatal("AlignDown() accepted a negative instant")
	}
	if _, _, err := WindowEndingAt(math.MaxInt64, 2, 1); err == nil {
		t.Fatal("WindowEndingAt() accepted an overflowing bucket end")
	}
}

func TestWindowEndingAtIncludesCurrentBucket(t *testing.T) {
	from, to, err := WindowEndingAt(10_801_234, MillisecondsPerHour, 3)
	if err != nil {
		t.Fatalf("WindowEndingAt() error = %v", err)
	}
	if from != 3_600_000 || to != 14_400_000 {
		t.Fatalf("WindowEndingAt() = [%d, %d), want [3600000, 14400000)", from, to)
	}
}
