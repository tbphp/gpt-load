package gateway

import (
	"net/http"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

var reasonConcurrencyLimit = reason{Status: http.StatusTooManyRequests, Code: "concurrency_limit_exceeded", Message: "Concurrency limit exceeded."}

// 每次准入都在发布锁内读取完整的新配置；活动计数不跟随快照重建。
func (h *Handler) acquireRequestConcurrency(keyID uint) (release func(), failure *reason) {
	failure = &reasonConfigurationChanged
	h.manager.WithCurrentSnapshotRead(func(snapshot *state.ConfigSnapshot) bool {
		if snapshot == nil {
			return false
		}
		key, exists := snapshot.AccessKeysByID[keyID]
		if !exists {
			return false
		}
		limit := snapshot.Settings.DefaultAccessKeyConcurrencyLimit
		if key.ConcurrencyLimit != nil {
			limit = *key.ConcurrencyLimit
		}
		var allowed bool
		release, allowed = h.manager.Concurrency().TryAcquireRequest(keyID, snapshot.Settings.GlobalConcurrencyLimit, limit)
		if !allowed {
			failure = &reasonConcurrencyLimit
		} else {
			failure = nil
		}
		return true
	})
	return
}

func (h *Handler) acquireGroupConcurrency(groupID uint) (release func(), failure *reason) {
	failure = &reasonConfigurationChanged
	h.manager.WithCurrentSnapshotRead(func(snapshot *state.ConfigSnapshot) bool {
		if snapshot == nil {
			return false
		}
		group, exists := snapshot.Groups[groupID]
		if !exists {
			return false
		}
		var allowed bool
		release, allowed = h.manager.Concurrency().TryAcquireGroup(groupID, group.ConcurrencyLimit)
		if !allowed {
			failure = &reasonConcurrencyLimit
		} else {
			failure = nil
		}
		return true
	})
	return
}

func concurrencyControlRequest(request *http.Request, route route) bool {
	if route.Protocol != protocol.OpenAIResponses {
		return false
	}
	metadata, err := dialect.NewOpenAIResponses().InspectRequest(&dialect.ParsedRequest{Method: request.Method, Path: request.URL.Path, RawQuery: request.URL.RawQuery})
	return err == nil && metadata.Operation == execution.OperationResponsesCancel
}
