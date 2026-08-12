package storage

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDatabaseLoggerSuppressesOnlyCanceledRequestQueries(t *testing.T) {
	tests := []struct {
		name        string
		context     func() context.Context
		err         error
		wantLogged  bool
		wantMessage string
	}{
		{
			name: "canceled request",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			err: context.Canceled,
		},
		{
			name: "deadline exceeded",
			context: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				cancel()
				return ctx
			},
			err:         context.DeadlineExceeded,
			wantLogged:  true,
			wantMessage: context.DeadlineExceeded.Error(),
		},
		{
			name: "database failure after request cancellation",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			err:         errors.New("database unavailable"),
			wantLogged:  true,
			wantMessage: "database unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			queryEvaluated := false
			newDatabaseLogger(&output).Trace(
				test.context(),
				time.Now(),
				func() (string, int64) {
					queryEvaluated = true
					return "SELECT * FROM request_logs", 20
				},
				test.err,
			)

			if test.wantLogged {
				if !queryEvaluated || !strings.Contains(output.String(), test.wantMessage) {
					t.Fatalf("database log = %q, query evaluated = %t", output.String(), queryEvaluated)
				}
				return
			}
			if queryEvaluated || output.Len() != 0 {
				t.Fatalf("canceled query log = %q, query evaluated = %t", output.String(), queryEvaluated)
			}
		})
	}
}
