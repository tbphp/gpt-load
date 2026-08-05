package utils

import "testing"

func TestFormatDurationMS(t *testing.T) {
	tests := []struct {
		name string
		ms   int64
		want string
	}{
		{name: "zero", ms: 0, want: "0ms"},
		{name: "milliseconds", ms: 999, want: "999ms"},
		{name: "short seconds", ms: 1_234, want: "1.23s"},
		{name: "long seconds", ms: 14_109, want: "14.1s"},
		{name: "minute", ms: 61_000, want: "1m01s"},
		{name: "hour", ms: 3_723_000, want: "1h02m03s"},
		{name: "negative", ms: -1, want: "?"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := FormatDurationMS(test.ms); got != test.want {
				t.Fatalf("FormatDurationMS(%d) = %q, want %q", test.ms, got, test.want)
			}
		})
	}
}

func TestFormatNanoUSD(t *testing.T) {
	tests := []struct {
		name  string
		nanos int64
		want  string
	}{
		{name: "zero", nanos: 0, want: "0"},
		{name: "fraction", nanos: 55_000, want: "0.000055"},
		{name: "one dollar", nanos: 1_000_000_000, want: "1"},
		{name: "precise", nanos: 1_234_567_890, want: "1.23456789"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := FormatNanoUSD(test.nanos); got != test.want {
				t.Fatalf("FormatNanoUSD(%d) = %q, want %q", test.nanos, got, test.want)
			}
		})
	}
}
