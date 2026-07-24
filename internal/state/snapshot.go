// Package state owns immutable runtime configuration snapshots.
package state

import (
	"fmt"
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
	ID              uint
	Name            string
	UpstreamURL     string
	ValidationModel string
	Protocols       []protocol.Protocol
	Models          []ModelConfig
	Settings        config.Settings
	WeightManual    *int
	Enabled         bool
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
}

type TimeoutConfig struct {
	Connect    time.Duration
	FirstByte  time.Duration
	Request    time.Duration
	StreamIdle time.Duration
}

type HeaderRules struct {
	Set    map[string]string
	Remove []string
}

type GroupView struct {
	ID              uint
	Name            string
	UpstreamURL     string
	ValidationModel string
	Protocols       []protocol.Protocol
	Models          []ModelConfig
	Timeouts        TimeoutConfig
	HeaderRules     HeaderRules
	WeightManual    *int
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
	Revision         uint64
	Settings         RuntimeSettings
	Candidates       map[protocol.Protocol]map[string][]RouteTarget
	Groups           map[uint]GroupView
	AccessKeysByHash map[string]AccessKeyView
	RouteCatalog     map[protocol.Protocol]map[string][]RouteTarget
	GroupCatalog     map[uint]GroupCatalogView
	AccessKeysByID   map[uint]AccessKeyView
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
		Settings:         runtimeSettings,
		Candidates:       make(map[protocol.Protocol]map[string][]RouteTarget),
		Groups:           make(map[uint]GroupView),
		AccessKeysByHash: make(map[string]AccessKeyView),
		RouteCatalog:     make(map[protocol.Protocol]map[string][]RouteTarget),
		GroupCatalog:     make(map[uint]GroupCatalogView),
		AccessKeysByID:   make(map[uint]AccessKeyView),
	}

	for _, group := range input.Groups {
		snapshot.GroupCatalog[group.ID] = GroupCatalogView{
			ID: group.ID, Name: group.Name, Enabled: group.Enabled,
			WeightManual: cloneWeight(group.WeightManual),
		}
		appendRouteTarget(snapshot.RouteCatalog, group)
		timeouts, headerRules, err := ResolveGroupRuntimeSettings(runtimeSettings, group.Settings)
		if err != nil {
			return nil, fmt.Errorf("compile group %d settings: %w", group.ID, err)
		}
		if !group.Enabled {
			continue
		}
		view := GroupView{
			ID:              group.ID,
			Name:            group.Name,
			UpstreamURL:     group.UpstreamURL,
			ValidationModel: strings.TrimSpace(group.ValidationModel),
			Protocols:       append([]protocol.Protocol(nil), group.Protocols...),
			Models:          append([]ModelConfig(nil), group.Models...),
			Timeouts:        timeouts,
			HeaderRules:     headerRules,
			WeightManual:    cloneWeight(group.WeightManual),
		}
		snapshot.Groups[group.ID] = view
		appendRouteTarget(snapshot.Candidates, group)
	}

	for _, accessKey := range input.AccessKeys {
		snapshot.AccessKeysByID[accessKey.ID] = newAccessKeyView(accessKey)
		if accessKey.Status == AccessKeyStatusActive {
			snapshot.AccessKeysByHash[accessKey.KeyHash] = newAccessKeyView(accessKey)
		}
	}

	sortRouteIndex(snapshot.Candidates)
	sortRouteIndex(snapshot.RouteCatalog)
	return snapshot, nil
}

func newAccessKeyView(input AccessKeyConfig) AccessKeyView {
	return AccessKeyView{
		ID: input.ID, Name: input.Name, Status: input.Status,
		Filters: cloneFilterSet(input.Filters), RPMLimit: input.RPMLimit,
	}
}

func appendRouteTarget(index map[protocol.Protocol]map[string][]RouteTarget, group GroupConfig) {
	for _, model := range group.Models {
		target := RouteTarget{
			GroupID:         group.ID,
			UpstreamModelID: strings.TrimSpace(model.ID),
		}
		external := externalModelName(model)
		for _, value := range group.Protocols {
			if index[value] == nil {
				index[value] = make(map[string][]RouteTarget)
			}
			index[value][external] = append(index[value][external], target)
		}
	}
}

func sortRouteIndex(index map[protocol.Protocol]map[string][]RouteTarget) {
	for _, byModel := range index {
		for model := range byModel {
			sort.Slice(byModel[model], func(i, j int) bool {
				left, right := byModel[model][i], byModel[model][j]
				if left.GroupID != right.GroupID {
					return left.GroupID < right.GroupID
				}
				return left.UpstreamModelID < right.UpstreamModelID
			})
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
		if err := validateManualWeight(fmt.Sprintf("group %d", group.ID), group.WeightManual); err != nil {
			return err
		}
		if len(group.Protocols) == 0 {
			return fmt.Errorf("group %d protocols are required", group.ID)
		}
		seenProtocols := make(map[protocol.Protocol]struct{}, len(group.Protocols))
		for _, value := range group.Protocols {
			if !value.Valid() {
				return fmt.Errorf("group %d has invalid protocol %q", group.ID, value)
			}
			if _, duplicate := seenProtocols[value]; duplicate {
				return fmt.Errorf("group %d has duplicate protocol %q", group.ID, value)
			}
			seenProtocols[value] = struct{}{}
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
