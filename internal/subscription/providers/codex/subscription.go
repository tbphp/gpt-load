package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func observeSubscription(ctx context.Context, credential Credential, apiRoot string) *providerobservation.SubscriptionSummary {
	result := &providerobservation.SubscriptionSummary{State: "error"}
	endpoints, err := cpaembedded.ResolveCodexAPIEndpoints(apiRoot)
	if err != nil {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	options, err := codexOptions(ctx)
	if err != nil {
		return result
	}
	response, err := cpaembedded.ObserveCodexSubscription(ctx, credentialToBridge(credential), endpoints.AccountBase, options)
	if err != nil {
		var upstream *cpaembedded.UpstreamHTTPError
		if errors.As(err, &upstream) && upstream.StatusCode == http.StatusNotFound {
			result.State = "unavailable"
		}
		return result
	}
	var payload struct {
		ActiveUntil string `json:"active_until"`
		WillRenew   *bool  `json:"will_renew"`
	}
	if err := json.Unmarshal(response.Payload, &payload); err != nil {
		return result
	}
	end, err := time.Parse(time.RFC3339, payload.ActiveUntil)
	if err != nil || end.UnixMilli() <= 0 {
		return result
	}
	endMS := end.UnixMilli()
	return &providerobservation.SubscriptionSummary{
		State: "confirmed", PeriodEndMS: &endMS, WillRenew: payload.WillRenew,
	}
}
