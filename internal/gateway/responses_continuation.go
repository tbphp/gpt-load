package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

func (handler *Handler) responseBindingObserver(
	accessKeyID uint,
	selection scheduler.Selection,
	ref state.CredentialRef,
	request *dialect.ParsedRequest,
) func([]byte) error {
	if request == nil || request.Method != http.MethodPost || request.Path != "/v1/responses" ||
		selection.RouteMode != execution.RouteNative || selection.ResponsesStoreDowngraded ||
		selection.ResolvedTarget.ResponsesStoreHandling(
			protocol.OpenAIResponses, execution.OperationResponsesCreate,
		) != channel.ResponsesStoreHandlingUpstreamManaged {
		return nil
	}
	var options struct {
		Store *bool `json:"store"`
	}
	if json.Unmarshal(request.Body, &options) != nil || (options.Store != nil && !*options.Store) {
		return nil
	}
	return func(payload []byte) error {
		var response struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Store  *bool  `json:"store"`
		}
		if json.Unmarshal(payload, &response) != nil || response.Object != "response" ||
			response.ID == "" || (response.Store != nil && !*response.Store) {
			return nil
		}
		if !handler.responseBindings.Record(accessKeyID, response.ID, ref) {
			return fmt.Errorf("%w: response ownership could not be recorded", ErrUpstreamProtocol)
		}
		return nil
	}
}
