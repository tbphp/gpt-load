package control

import (
	"encoding/json"

	"gpt-load/internal/channel"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

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

func subscriptionTargetFromResolved(resolved channel.ResolvedTarget) subscriptionruntime.Target {
	return subscriptionruntime.NewTarget(resolved.TargetConfig)
}
