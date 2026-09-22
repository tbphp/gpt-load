package gateway

import (
	"bytes"
	"testing"
	"time"
)

// TestEmptyResponsePrereadLimits 固定预读窗口的三重上限。事件数是主判据，
// 时间与字节数兜底：任一耗尽都必须立即提交，不能无限压住响应。
func TestEmptyResponsePrereadLimits(t *testing.T) {
	t.Parallel()

	chunk := []byte("data: {}\n\n")
	t.Run("disabled never holds", func(t *testing.T) {
		t.Parallel()
		preread := newEmptyResponsePreread(false, nil)
		if preread.hold(chunk, 1, false) {
			t.Fatal("hold() = true, want disabled preread to commit immediately")
		}
	})
	t.Run("event limit", func(t *testing.T) {
		t.Parallel()
		preread := newEmptyResponsePreread(true, nil)
		if !preread.hold(chunk, emptyResponsePrereadEvents-1, false) {
			t.Fatal("hold() = false below the event limit")
		}
		if preread.hold(chunk, emptyResponsePrereadEvents, false) {
			t.Fatal("hold() = true at the event limit")
		}
	})
	t.Run("byte limit", func(t *testing.T) {
		t.Parallel()
		preread := newEmptyResponsePreread(true, nil)
		if preread.hold(bytes.Repeat([]byte("x"), emptyResponsePrereadBytes), 1, false) {
			t.Fatal("hold() = true at the byte limit")
		}
	})
	t.Run("time limit", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, time.September, 23, 12, 0, 0, 0, time.UTC)
		preread := newEmptyResponsePreread(true, func() time.Time { return now })
		if !preread.hold(chunk, 1, false) {
			t.Fatal("hold() = false when the window just started")
		}
		now = now.Add(emptyResponsePrereadWindow)
		if preread.hold(chunk, 2, false) {
			t.Fatal("hold() = true after the window elapsed")
		}
	})
	t.Run("flush reports held terminal", func(t *testing.T) {
		t.Parallel()
		preread := newEmptyResponsePreread(true, nil)
		preread.hold([]byte("a"), 1, false)
		preread.hold([]byte("b"), 2, true)
		held, terminal := preread.flush()
		if string(held) != "ab" || !terminal {
			t.Fatalf("flush() = %q, %v; want \"ab\", true", held, terminal)
		}
		if preread.holding() {
			t.Fatal("holding() = true after flush")
		}
	})
}
