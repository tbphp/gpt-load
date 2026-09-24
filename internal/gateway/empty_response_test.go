package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

// TestEmptyResponsePrereadLimits 固定预读窗口的三重上限。事件数是主判据，
// 时间与字节数兜底：任一耗尽都必须立即提交，不能无限压住响应。
func TestEmptyResponsePrereadLimits(t *testing.T) {
	t.Parallel()

	chunk := []byte("data: {}\n\n")
	t.Run("disabled never holds", func(t *testing.T) {
		t.Parallel()
		preread := newEmptyResponsePreread(false, nil, 0)
		if preread.hold(chunk, 1, false) {
			t.Fatal("hold() = true, want disabled preread to commit immediately")
		}
	})
	t.Run("event limit", func(t *testing.T) {
		t.Parallel()
		preread := newEmptyResponsePreread(true, nil, 0)
		if !preread.hold(chunk, emptyResponsePrereadEvents-1, false) {
			t.Fatal("hold() = false below the event limit")
		}
		if preread.hold(chunk, emptyResponsePrereadEvents, false) {
			t.Fatal("hold() = true at the event limit")
		}
	})
	t.Run("byte limit", func(t *testing.T) {
		t.Parallel()
		preread := newEmptyResponsePreread(true, nil, 0)
		if preread.hold(bytes.Repeat([]byte("x"), emptyResponsePrereadBytes), 1, false) {
			t.Fatal("hold() = true at the byte limit")
		}
	})
	t.Run("time limit", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, time.September, 23, 12, 0, 0, 0, time.UTC)
		preread := newEmptyResponsePreread(true, func() time.Time { return now }, 0)
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
		preread := newEmptyResponsePreread(true, nil, 0)
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

// TestEmptyResponsePrereadCommitsOnSilentUpstream 固定预读窗口是真正的时限：上游发完
// 无产出的前导后静默（例如推理模型不带推理摘要），不会再有事件触发检查，网关必须在
// 窗口到期时主动提交，而不是把响应头一直压到上游恢复输出。窗口到期提交后，这次尝试
// 不再参与空回判定。
func TestEmptyResponsePrereadCommitsOnSilentUpstream(t *testing.T) {
	t.Parallel()

	const preamble = "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"
	endings := map[string]string{
		"content after silence": "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late\"}}]}\n\n" +
			"data: [DONE]\n\n",
		"no content after silence": "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	}
	for name, ending := range endings {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			committedDuringSilence := make(chan struct{})
			executor := fakeExecutionExecutor{stream: func(
				_ context.Context,
				_ execution.AttemptSpec,
				sink execution.StreamSink,
			) execution.StreamResult {
				for _, event := range []execution.StreamEvent{
					{
						Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK,
						Header: http.Header{"Content-Type": {"text/event-stream"}},
					},
					{Sequence: 2, Kind: execution.StreamEventData, Data: []byte(preamble)},
				} {
					if err := sink(event); err != nil {
						t.Errorf("sink: %v", err)
					}
				}
				// 上游静默：不再调用 sink，只能由网关自己在窗口到期时提交。
				select {
				case <-committedDuringSilence:
				case <-time.After(2 * time.Second):
					t.Error("held preamble was not committed while upstream stayed silent")
				}
				if err := sink(execution.StreamEvent{
					Sequence: 3, Kind: execution.StreamEventData, Data: []byte(ending),
				}); err != nil {
					t.Errorf("sink: %v", err)
				}
				return execution.StreamResult{
					DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				}
			}}
			input := executionForwardInput()
			input.EmptyResponseRetry = true
			var signal sync.Once
			input.OnStreamReady = func() { signal.Do(func() { close(committedDuringSilence) }) }
			forwarder := NewExecutionForwarder(executor)
			forwarder.emptyResponseWindow = 20 * time.Millisecond
			recorder := httptest.NewRecorder()

			result := forwarder.ForwardStream(context.Background(), input, recorder)
			if !result.Committed || result.EmptyResponseBeforeCommit {
				t.Fatalf("Committed = %v, EmptyResponseBeforeCommit = %v; want a committed stream",
					result.Committed, result.EmptyResponseBeforeCommit)
			}
			if got := recorder.Body.String(); got != preamble+ending {
				t.Fatalf("body = %q, want %q", got, preamble+ending)
			}
		})
	}
}
