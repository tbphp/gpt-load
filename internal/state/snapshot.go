// Package state owns immutable runtime configuration snapshots.
package state

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
)

type CompileInput struct {
	SystemSettings  config.Settings
	ChannelRegistry *channel.Registry
	Groups          []GroupConfig
	Credentials     []CredentialConfig
	AccessKeys      []AccessKeyConfig
}

type GroupConfig struct {
	ID              uint
	Name            string
	ChannelID       channel.ID
	Params          json.RawMessage
	ValidationModel string
	Models          []ModelConfig
	Settings        config.Settings
	WeightManual    *int
	Enabled         bool
}

// CredentialConfig contains only non-secret credential metadata required to
// validate a runtime configuration publication.
type CredentialConfig struct {
	ID                 uint
	GroupID            uint
	Status             CredentialStatus
	WeightManual       *int
	Version            uint64
	IdentityGeneration uint64
	Fingerprint        string
}

type ModelConfig struct {
	ID    string
	Alias string
}

func externalModelName(model ModelConfig) string {
	if alias := strings.TrimSpace(model.Alias); alias != "" {
		return alias
	}
	return strings.TrimSpace(model.ID)
}

type AccessKeyConfig struct {
	ID       uint
	Name     string
	KeyHash  string
	Status   AccessKeyStatus
	Filters  FilterSet
	RPMLimit int64
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
	Mode            channel.RouteMode
	ResolvedTarget  channel.ResolvedTarget
}

// NoModelRouteKey identifies operations whose upstream resource ID, rather
// than a model, determines the target after affinity resolution.
const NoModelRouteKey = ""

// ExecutionCandidateIndex indexes targets by client protocol, logical
// operation, and external model. Resource operations use NoModelRouteKey.
type ExecutionCandidateIndex map[protocol.Protocol]map[execution.Operation]map[string][]RouteTarget

type TimeoutConfig struct {
	FirstByte  time.Duration
	Request    time.Duration
	StreamIdle time.Duration
}

type HeaderRules struct {
	Set    map[string]string
	Remove []string
}

type GroupView struct {
	ID                 uint
	Name               string
	ChannelID          channel.ID
	Params             json.RawMessage
	ResolvedTarget     channel.ResolvedTarget
	ValidationModel    string
	ClientProtocols    []protocol.Protocol
	Models             []ModelConfig
	Timeouts           TimeoutConfig
	HeaderRules        HeaderRules
	InjectUsageOptions bool
	WeightManual       *int
}

type GroupCatalogView struct {
	ID           uint
	Name         string
	Enabled      bool
	WeightManual *int
}

type AccessKeyView struct {
	ID       uint
	Name     string
	Status   AccessKeyStatus
	Filters  FilterSet
	RPMLimit int64
}

type ConfigSnapshot struct {
	Revision              uint64
	Settings              RuntimeSettings
	ExecutionCandidates   ExecutionCandidateIndex
	ExecutionRouteCatalog ExecutionCandidateIndex
	Groups                map[uint]GroupView
	AccessKeysByHash      map[string]AccessKeyView
	GroupCatalog          map[uint]GroupCatalogView
	AccessKeysByID        map[uint]AccessKeyView
}

func Compile(input CompileInput) (*ConfigSnapshot, error) {
	if err := validateCompileInput(input); err != nil {
		return nil, err
	}
	runtimeSettings, err := ResolveRuntimeSettings(input.SystemSettings)
	if err != nil {
		return nil, err
	}

	snapshot := &ConfigSnapshot{
		Settings:              runtimeSettings,
		ExecutionCandidates:   make(ExecutionCandidateIndex),
		ExecutionRouteCatalog: make(ExecutionCandidateIndex),
		Groups:                make(map[uint]GroupView),
		AccessKeysByHash:      make(map[string]AccessKeyView),
		GroupCatalog:          make(map[uint]GroupCatalogView),
		AccessKeysByID:        make(map[uint]AccessKeyView),
	}

	for _, group := range input.Groups {
		snapshot.GroupCatalog[group.ID] = GroupCatalogView{
			ID: group.ID, Name: group.Name, Enabled: group.Enabled,
			WeightManual: cloneWeight(group.WeightManual),
		}
		if err := appendExecutionTargets(snapshot.ExecutionRouteCatalog, input.ChannelRegistry, group); err != nil {
			return nil, err
		}
		resolved, err := ResolveGroupRuntimeSettings(runtimeSettings, group.Settings)
		if err != nil {
			return nil, fmt.Errorf("compile group %d settings: %w", group.ID, err)
		}
		if !group.Enabled {
			continue
		}

		view := GroupView{
			ID:                 group.ID,
			Name:               group.Name,
			ValidationModel:    strings.TrimSpace(group.ValidationModel),
			Models:             append([]ModelConfig(nil), group.Models...),
			Timeouts:           resolved.Timeouts,
			HeaderRules:        resolved.HeaderRules,
			InjectUsageOptions: resolved.InjectUsageOptions,
			WeightManual:       cloneWeight(group.WeightManual),
		}
		params, err := input.ChannelRegistry.ValidateParams(group.ChannelID, group.Params)
		if err != nil {
			return nil, fmt.Errorf("compile group %d params: %w", group.ID, err)
		}
		target, err := input.ChannelRegistry.Resolve(group.ChannelID, group.Params)
		if err != nil {
			return nil, fmt.Errorf("compile group %d channel: %w", group.ID, err)
		}
		descriptor, _ := input.ChannelRegistry.Get(group.ChannelID)
		view.ChannelID = group.ChannelID
		view.Params = params.CanonicalJSON()
		view.ResolvedTarget = cloneResolvedTarget(target)
		view.ClientProtocols = append([]protocol.Protocol(nil), descriptor.ClientProtocols...)
		if err := appendExecutionTargets(snapshot.ExecutionCandidates, input.ChannelRegistry, group); err != nil {
			return nil, err
		}
		snapshot.Groups[group.ID] = view
	}

	for _, accessKey := range input.AccessKeys {
		snapshot.AccessKeysByID[accessKey.ID] = newAccessKeyView(accessKey)
		if accessKey.Status == AccessKeyStatusActive {
			snapshot.AccessKeysByHash[accessKey.KeyHash] = newAccessKeyView(accessKey)
		}
	}

	sortExecutionRouteIndex(snapshot.ExecutionCandidates)
	sortExecutionRouteIndex(snapshot.ExecutionRouteCatalog)
	return snapshot, nil
}

func newAccessKeyView(input AccessKeyConfig) AccessKeyView {
	return AccessKeyView{
		ID: input.ID, Name: input.Name, Status: input.Status,
		Filters: cloneFilterSet(input.Filters), RPMLimit: input.RPMLimit,
	}
}

func appendExecutionTargets(
	index ExecutionCandidateIndex,
	registry *channel.Registry,
	group GroupConfig,
) error {
	target, err := registry.Resolve(group.ChannelID, group.Params)
	if err != nil {
		return fmt.Errorf("compile group %d channel: %w", group.ID, err)
	}
	descriptor, ok := registry.Get(group.ChannelID)
	if !ok {
		return fmt.Errorf("compile group %d channel: unknown channel %q", group.ID, group.ChannelID)
	}
	for _, clientProtocol := range descriptor.ClientProtocols {
		capabilities := target.Capabilities(clientProtocol)
		for _, operation := range capabilities.Operations() {
			if operation == execution.OperationListModels || operation == execution.OperationProbe {
				continue
			}
			mode, ok := target.Mode(clientProtocol, operation)
			if !ok {
				return fmt.Errorf("compile group %d channel has no route mode for %q/%q", group.ID, clientProtocol, operation)
			}
			switch operation {
			case execution.OperationResponsesRetrieve,
				execution.OperationResponsesDelete,
				execution.OperationResponsesCancel,
				execution.OperationResponsesInputItems:
				appendExecutionTarget(index, clientProtocol, operation, NoModelRouteKey, RouteTarget{
					GroupID: group.ID, Mode: mode, ResolvedTarget: cloneResolvedTarget(target),
				})
			case execution.OperationResponsesPassthrough:
				appendExecutionTarget(index, clientProtocol, operation, NoModelRouteKey, RouteTarget{
					GroupID: group.ID, Mode: mode, ResolvedTarget: cloneResolvedTarget(target),
				})
				fallthrough
			case execution.OperationChatCompletion,
				execution.OperationResponsesCreate,
				execution.OperationResponsesCompact,
				execution.OperationResponsesInputTokens:
				for _, model := range group.Models {
					appendExecutionTarget(index, clientProtocol, operation, externalModelName(model), RouteTarget{
						GroupID: group.ID, UpstreamModelID: strings.TrimSpace(model.ID),
						Mode: mode, ResolvedTarget: cloneResolvedTarget(target),
					})
				}
			default:
				return fmt.Errorf("compile group %d channel has unsupported routable operation %q", group.ID, operation)
			}
		}
	}
	return nil
}

func appendExecutionTarget(
	index ExecutionCandidateIndex,
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	externalModel string,
	target RouteTarget,
) {
	if index[clientProtocol] == nil {
		index[clientProtocol] = make(map[execution.Operation]map[string][]RouteTarget)
	}
	if index[clientProtocol][operation] == nil {
		index[clientProtocol][operation] = make(map[string][]RouteTarget)
	}
	index[clientProtocol][operation][externalModel] = append(
		index[clientProtocol][operation][externalModel],
		target,
	)
}

func cloneResolvedTarget(target channel.ResolvedTarget) channel.ResolvedTarget {
	target.TargetConfig = append(json.RawMessage(nil), target.TargetConfig...)
	return target
}

func sortExecutionRouteIndex(index ExecutionCandidateIndex) {
	for _, byOperation := range index {
		for _, byModel := range byOperation {
			for model := range byModel {
				sort.Slice(byModel[model], func(i, j int) bool {
					left, right := byModel[model][i], byModel[model][j]
					if left.Mode != right.Mode {
						return left.Mode == channel.RouteNative
					}
					if left.GroupID != right.GroupID {
						return left.GroupID < right.GroupID
					}
					return left.UpstreamModelID < right.UpstreamModelID
				})
			}
		}
	}
}

func validateCompileInput(input CompileInput) error {
	groupIDs := make(map[uint]struct{}, len(input.Groups))
	for _, group := range input.Groups {
		if group.ID == 0 {
			return fmt.Errorf("group id is required")
		}
		if _, duplicate := groupIDs[group.ID]; duplicate {
			return fmt.Errorf("duplicate group id %d", group.ID)
		}
		groupIDs[group.ID] = struct{}{}
		if input.ChannelRegistry == nil {
			return fmt.Errorf("group %d channel registry is required", group.ID)
		}
		if group.ChannelID == "" {
			return fmt.Errorf("group %d channel id is required", group.ID)
		}
		if _, err := input.ChannelRegistry.Resolve(group.ChannelID, group.Params); err != nil {
			return fmt.Errorf("group %d channel %q: %w", group.ID, group.ChannelID, err)
		}
		if err := validateManualWeight(fmt.Sprintf("group %d", group.ID), group.WeightManual); err != nil {
			return err
		}
		seenModels := make(map[string]struct{}, len(group.Models))
		for _, model := range group.Models {
			if strings.TrimSpace(model.ID) == "" {
				return fmt.Errorf("group %d model id is required", group.ID)
			}
			external := externalModelName(model)
			if _, duplicate := seenModels[external]; duplicate {
				return fmt.Errorf("group %d has duplicate external model %q", group.ID, external)
			}
			seenModels[external] = struct{}{}
		}
	}

	credentialIDs := make(map[uint]struct{}, len(input.Credentials))
	for _, credential := range input.Credentials {
		if credential.ID == 0 {
			return fmt.Errorf("credential id is required")
		}
		if _, duplicate := credentialIDs[credential.ID]; duplicate {
			return fmt.Errorf("duplicate credential id %d", credential.ID)
		}
		credentialIDs[credential.ID] = struct{}{}
		if credential.GroupID == 0 {
			return fmt.Errorf("credential %d group id is required", credential.ID)
		}
		if _, ok := groupIDs[credential.GroupID]; !ok {
			return fmt.Errorf("credential %d belongs to unknown group %d", credential.ID, credential.GroupID)
		}
		switch credential.Status {
		case CredentialStatusActive, CredentialStatusDisabled:
		default:
			return fmt.Errorf("credential %d has invalid status %q", credential.ID, credential.Status)
		}
		if err := validateManualWeight(fmt.Sprintf("credential %d", credential.ID), credential.WeightManual); err != nil {
			return err
		}
		if credential.Version == 0 {
			return fmt.Errorf("credential %d version is required", credential.ID)
		}
		if credential.IdentityGeneration == 0 {
			return fmt.Errorf("credential %d identity generation is required", credential.ID)
		}
		if strings.TrimSpace(credential.Fingerprint) == "" {
			return fmt.Errorf("credential %d fingerprint is required", credential.ID)
		}
	}

	accessKeyIDs := make(map[uint]struct{}, len(input.AccessKeys))
	hashes := make(map[string]struct{}, len(input.AccessKeys))
	for _, accessKey := range input.AccessKeys {
		if accessKey.ID == 0 {
			return fmt.Errorf("access key id is required")
		}
		if _, duplicate := accessKeyIDs[accessKey.ID]; duplicate {
			return fmt.Errorf("duplicate access key id %d", accessKey.ID)
		}
		accessKeyIDs[accessKey.ID] = struct{}{}
		if accessKey.RPMLimit < 0 {
			return fmt.Errorf("access key %d rpm limit must not be negative", accessKey.ID)
		}
		switch accessKey.Status {
		case AccessKeyStatusActive, AccessKeyStatusDisabled:
		default:
			return fmt.Errorf("access key %d has invalid status %q", accessKey.ID, accessKey.Status)
		}
		if strings.TrimSpace(accessKey.KeyHash) == "" {
			return fmt.Errorf("access key %d key hash is required", accessKey.ID)
		}
		if _, duplicate := hashes[accessKey.KeyHash]; duplicate {
			return fmt.Errorf("duplicate access key hash %q", accessKey.KeyHash)
		}
		hashes[accessKey.KeyHash] = struct{}{}
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
