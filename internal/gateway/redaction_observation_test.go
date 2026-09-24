package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/usage"
)

func TestRedactionFailurePreservesUsageEvidence(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	for _, state := range []usage.State{usage.StateComplete, usage.StatePartial} {
		t.Run(string(state), func(t *testing.T) {
			want := usage.Result{State: state, Tokens: usage.Tokens{UncachedInput: 123, Output: 45}}
			in := executionForwardInput()
			// 还原结果含上游凭据时必须拒绝，用它触发还原失败。
			token, err := cipher.EncryptToken(in.APIKey)
			if err != nil {
				t.Fatal(err)
			}
			executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
				return execution.AttemptResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"choices":[{"message":{"content":"` + token + `"}}]}`), Usage: &execution.UsageEvidence{Normalized: want}}
			}}
			in.RedactionCipher = cipher
			in.ObserveUsage = true
			got := NewExecutionForwarder(executor).Forward(context.Background(), in)
			if !errors.Is(got.Err, errUnaryRestore) || len(got.Body) != 0 || len(got.ClassificationBody) != 0 {
				t.Fatal("restore failure changed rejection or body isolation")
			}
			if got.Usage != want {
				t.Fatalf("usage evidence lost: got=%+v want=%+v", got.Usage, want)
			}
		})
	}
}

// CR 终止边界到 EOF 才确定，覆盖 Finish 的首次提交和后续 flush。
func TestRedactionFinishObservesDelivery(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	for _, failFlush := range []bool{false, true} {
		name := "first response"
		if failFlush {
			name = "flush failure"
		}
		t.Run(name, func(t *testing.T) {
			firstResponses := 0
			writeErr := errors.New("synthetic downstream flush failure")
			executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
				chunks := []string{"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\r\r"}
				if failFlush {
					chunks = append([]string{"data: {\"type\":\"response.created\",\"response\":{}}\n\n"}, chunks...)
				}
				if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
					t.Fatal(err)
				}
				for i, chunk := range chunks {
					if err := sink(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: []byte(chunk)}); err != nil {
						t.Fatal(err)
					}
				}
				return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}
			}}
			in := responsesExecutionForwardInput()
			in.RedactionCipher = cipher
			in.OnFirstResponse = func() { firstResponses++ }
			writer := &redactionFinishWriter{ResponseRecorder: httptest.NewRecorder(), err: writeErr, fail: failFlush}
			got := NewExecutionForwarder(executor).ForwardStream(context.Background(), in, writer)
			if firstResponses != 1 || !got.Committed {
				t.Fatalf("first response=%d committed=%v", firstResponses, got.Committed)
			}
			if failFlush {
				if !errors.Is(got.Err, writeErr) || got.Stream.EndReason != StreamEndDownstreamWriteFailure {
					t.Fatalf("flush failure misclassified: err=%v end=%v", got.Err, got.Stream.EndReason)
				}
			} else if got.Err != nil || got.Stream.EndReason != StreamEndCleanEOF {
				t.Fatalf("unexpected completion: %+v", got.Stream)
			}
		})
	}
}

type redactionFinishWriter struct {
	*httptest.ResponseRecorder
	flushes int
	err     error
	fail    bool
}

func (w *redactionFinishWriter) FlushError() error {
	w.flushes++
	if w.fail && w.flushes == 2 {
		return w.err
	}
	return nil
}
