// Package state owns immutable runtime configuration snapshots.
package state

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
)

type CompileInput struct {
	SystemSettings config.Settings
	Groups         []GroupConfig
	AccessKeys     []AccessKeyConfig
}

type GroupConfig struct {
	ID          uint
	Name        string
	UpstreamURL string
	Protocols   []protocol.Protocol
	Models      []ModelConfig
	Settings    config.Settings
	Enabled     bool
}

type ModelConfig struct {
	ID    string
	Alias string
}

type AccessKeyConfig struct {
	ID      uint
	Name    string
	KeyHash string
	Status  AccessKeyStatus
	Filters FilterSet
}

type AccessKeyStatus string

const (
	AccessKeyStatusActive   AccessKeyStatus = "active"
	AccessKeyStatusDisabled AccessKeyStatus = "disabled"
)

type FilterSet struct {
	Groups    map[uint]struct{}
	Protocols map[protocol.Protocol]struct{}
	Models    map[string]struct{}
}

type RouteTarget struct {
	GroupID         uint
	UpstreamModelID string
}

type TimeoutConfig struct {
	Connect    time.Duration
	FirstByte  time.Duration
	Request    time.Duration
	StreamIdle time.Duration
}

var defaultTimeouts = TimeoutConfig{
	Connect:    15 * time.Second,
	FirstByte:  120 * time.Second,
	Request:    600 * time.Second,
	StreamIdle: 300 * time.Second,
}

type HeaderRules struct {
	Set    map[string]string
	Remove []string
}

type GroupView struct {
	ID          uint
	Name        string
	UpstreamURL string
	Protocols   []protocol.Protocol
	Models      []ModelConfig
	Timeouts    TimeoutConfig
	HeaderRules HeaderRules
}

type AccessKeyView struct {
	ID      uint
	Name    string
	Filters FilterSet
}

type ConfigSnapshot struct {
	Revision         uint64
	Candidates       map[protocol.Protocol]map[string][]RouteTarget
	Groups           map[uint]GroupView
	AccessKeysByHash map[string]AccessKeyView
}

func Compile(input CompileInput) (*ConfigSnapshot, error) {
	if err := validateCompileInput(input); err != nil {
		return nil, err
	}

	snapshot := &ConfigSnapshot{
		Candidates:       make(map[protocol.Protocol]map[string][]RouteTarget),
		Groups:           make(map[uint]GroupView),
		AccessKeysByHash: make(map[string]AccessKeyView),
	}

	for _, group := range input.Groups {
		if !group.Enabled {
			continue
		}
		timeouts, headerRules, err := compileRuntimeSettings(input.SystemSettings, group.Settings)
		if err != nil {
			return nil, err
		}
		view := GroupView{
			ID: group.ID, Name: group.Name, UpstreamURL: group.UpstreamURL,
			Protocols:   append([]protocol.Protocol(nil), group.Protocols...),
			Models:      append([]ModelConfig(nil), group.Models...),
			Timeouts:    timeouts,
			HeaderRules: headerRules,
		}
		snapshot.Groups[group.ID] = view

		seen := make(map[protocol.Protocol]map[string]struct{})
		for _, model := range group.Models {
			modelID := strings.TrimSpace(model.ID)
			for _, p := range group.Protocols {
				if seen[p] == nil {
					seen[p] = make(map[string]struct{})
				}
				if _, duplicate := seen[p][modelID]; duplicate {
					continue
				}
				seen[p][modelID] = struct{}{}
				if snapshot.Candidates[p] == nil {
					snapshot.Candidates[p] = make(map[string][]RouteTarget)
				}
				snapshot.Candidates[p][modelID] = append(
					snapshot.Candidates[p][modelID],
					RouteTarget{GroupID: group.ID, UpstreamModelID: modelID},
				)
			}
		}
	}

	for _, accessKey := range input.AccessKeys {
		if accessKey.Status != AccessKeyStatusActive {
			continue
		}
		snapshot.AccessKeysByHash[accessKey.KeyHash] = AccessKeyView{
			ID: accessKey.ID, Name: accessKey.Name, Filters: cloneFilterSet(accessKey.Filters),
		}
	}

	for _, byModel := range snapshot.Candidates {
		for modelID := range byModel {
			sort.Slice(byModel[modelID], func(i, j int) bool {
				left, right := byModel[modelID][i], byModel[modelID][j]
				if left.GroupID != right.GroupID {
					return left.GroupID < right.GroupID
				}
				return left.UpstreamModelID < right.UpstreamModelID
			})
		}
	}
	return snapshot, nil
}

func compileRuntimeSettings(system, group config.Settings) (TimeoutConfig, HeaderRules, error) {
	allowedGroupSettings := map[string]struct{}{
		"connect_timeout": {}, "first_byte_timeout": {},
		"request_timeout": {}, "stream_idle_timeout": {}, "header_rules": {},
	}
	for key := range group {
		if _, ok := allowedGroupSettings[key]; !ok {
			return TimeoutConfig{}, HeaderRules{}, fmt.Errorf("unknown group setting %q", key)
		}
	}

	merged := config.MergeSettings(system, group)
	timeouts := defaultTimeouts
	fields := []struct {
		key    string
		target *time.Duration
	}{
		{key: "connect_timeout", target: &timeouts.Connect},
		{key: "first_byte_timeout", target: &timeouts.FirstByte},
		{key: "request_timeout", target: &timeouts.Request},
		{key: "stream_idle_timeout", target: &timeouts.StreamIdle},
	}
	for _, field := range fields {
		value, ok := merged[field.key]
		if !ok {
			continue
		}
		seconds, err := positiveWholeSeconds(field.key, value)
		if err != nil {
			return TimeoutConfig{}, HeaderRules{}, err
		}
		*field.target = time.Duration(seconds) * time.Second
	}

	rules, err := parseHeaderRules(merged["header_rules"])
	if err != nil {
		return TimeoutConfig{}, HeaderRules{}, err
	}
	return timeouts, rules, nil
}

func positiveWholeSeconds(path string, value any) (int64, error) {
	const maxTimeoutSeconds = int64((1<<63)-1) / int64(time.Second)

	var seconds int64
	switch typed := value.(type) {
	case int:
		seconds = int64(typed)
	case int64:
		seconds = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed > float64(maxTimeoutSeconds) {
			return 0, fmt.Errorf("%s must be a positive whole number", path)
		}
		seconds = int64(typed)
	case json.Number:
		literal := typed.String()
		parsed, ok := new(big.Rat).SetString(literal)
		if !json.Valid([]byte(literal)) || !ok || !parsed.IsInt() || !parsed.Num().IsInt64() {
			return 0, fmt.Errorf("%s must be a positive whole number", path)
		}
		seconds = parsed.Num().Int64()
	default:
		return 0, fmt.Errorf("%s must be a positive whole number", path)
	}
	if seconds <= 0 || seconds > maxTimeoutSeconds {
		return 0, fmt.Errorf("%s must be a positive whole number within duration range", path)
	}
	return seconds, nil
}

func parseHeaderRules(value any) (HeaderRules, error) {
	rules := HeaderRules{Set: make(map[string]string)}
	if value == nil {
		return rules, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return HeaderRules{}, fmt.Errorf("header_rules must be an object")
	}
	for key := range object {
		if key != "set" && key != "remove" {
			return HeaderRules{}, fmt.Errorf("unknown header_rules field %q", key)
		}
	}
	if rawSet, exists := object["set"]; exists {
		set, ok := rawSet.(map[string]any)
		if !ok {
			return HeaderRules{}, fmt.Errorf("header_rules.set must be an object")
		}
		for name, rawValue := range set {
			text, ok := rawValue.(string)
			if !ok {
				return HeaderRules{}, fmt.Errorf("header_rules.set.%s must be a string", name)
			}
			rules.Set[name] = text
		}
	}
	if rawRemove, exists := object["remove"]; exists {
		remove, ok := rawRemove.([]any)
		if !ok {
			return HeaderRules{}, fmt.Errorf("header_rules.remove must be an array")
		}
		rules.Remove = make([]string, 0, len(remove))
		for index, rawName := range remove {
			name, ok := rawName.(string)
			if !ok {
				return HeaderRules{}, fmt.Errorf("header_rules.remove[%d] must be a string", index)
			}
			rules.Remove = append(rules.Remove, name)
		}
	}
	return rules, nil
}

func validateCompileInput(input CompileInput) error {
	enabledGroupIDs := make(map[uint]struct{})
	activeHashes := make(map[string]struct{})
	for _, group := range input.Groups {
		if !group.Enabled {
			continue
		}
		if group.ID == 0 {
			return fmt.Errorf("group id is required")
		}
		if _, duplicate := enabledGroupIDs[group.ID]; duplicate {
			return fmt.Errorf("duplicate group id %d", group.ID)
		}
		enabledGroupIDs[group.ID] = struct{}{}
		if len(group.Protocols) == 0 {
			return fmt.Errorf("group %d protocols are required", group.ID)
		}
		protocols := make(map[protocol.Protocol]struct{}, len(group.Protocols))
		for _, p := range group.Protocols {
			if !p.Valid() {
				return fmt.Errorf("group %d has invalid protocol %q", group.ID, p)
			}
			if _, duplicate := protocols[p]; duplicate {
				return fmt.Errorf("group %d has duplicate protocol %q", group.ID, p)
			}
			protocols[p] = struct{}{}
		}
		for _, model := range group.Models {
			modelID := strings.TrimSpace(model.ID)
			if modelID == "" {
				return fmt.Errorf("group %d model id is required", group.ID)
			}
		}
	}
	for _, accessKey := range input.AccessKeys {
		switch accessKey.Status {
		case AccessKeyStatusDisabled:
			continue
		case AccessKeyStatusActive:
		default:
			return fmt.Errorf("access key %d has invalid status %q", accessKey.ID, accessKey.Status)
		}
		if strings.TrimSpace(accessKey.KeyHash) == "" {
			return fmt.Errorf("access key %d key hash is required", accessKey.ID)
		}
		if _, duplicate := activeHashes[accessKey.KeyHash]; duplicate {
			return fmt.Errorf("duplicate access key hash %q", accessKey.KeyHash)
		}
		activeHashes[accessKey.KeyHash] = struct{}{}
		if err := validateFilterSet(accessKey.ID, accessKey.Filters); err != nil {
			return err
		}
	}
	return nil
}

func validateFilterSet(accessKeyID uint, filters FilterSet) error {
	for p := range filters.Protocols {
		if !p.Valid() {
			return fmt.Errorf("access key %d filter has invalid protocol %q", accessKeyID, p)
		}
	}
	for model := range filters.Models {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("access key %d filter model is required", accessKeyID)
		}
	}
	return nil
}

func cloneFilterSet(source FilterSet) FilterSet {
	cloned := FilterSet{}
	if source.Groups != nil {
		cloned.Groups = make(map[uint]struct{}, len(source.Groups))
		for id := range source.Groups {
			cloned.Groups[id] = struct{}{}
		}
	}
	if source.Protocols != nil {
		cloned.Protocols = make(map[protocol.Protocol]struct{}, len(source.Protocols))
		for p := range source.Protocols {
			cloned.Protocols[p] = struct{}{}
		}
	}
	if source.Models != nil {
		cloned.Models = make(map[string]struct{}, len(source.Models))
		for model := range source.Models {
			cloned.Models[model] = struct{}{}
		}
	}
	return cloned
}
