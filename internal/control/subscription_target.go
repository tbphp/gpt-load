package control

import (
	"encoding/json"

	"gpt-load/internal/channel"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// resolveSubscriptionTarget validates stored channel parameters and freezes
// their resolved provider configuration for one subscription operation.
func (s *Service) resolveSubscriptionTarget(
	channelID channel.ID,
	params []byte,
) (subscriptionruntime.Target, error) {
	resolved, err := s.channelRegistry.Resolve(channelID, json.RawMessage(params))
	if err != nil {
		return subscriptionruntime.Target{}, err
	}
	return subscriptionruntime.NewTarget(resolved.TargetConfig), nil
}

// subscriptionTargetFromResolved copies a previously validated channel target
// into the provider-neutral subscription runtime representation.
func subscriptionTargetFromResolved(resolved channel.ResolvedTarget) subscriptionruntime.Target {
	return subscriptionruntime.NewTarget(resolved.TargetConfig)
}
