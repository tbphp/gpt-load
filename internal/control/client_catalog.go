package control

import (
	"context"
	"reflect"
	"sort"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/clientcatalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
)

type ClientCatalogDTO struct {
	Models        []ClientModelProfileDTO `json:"models"`
	Selected      []string                `json:"selected"`
	Defaults      []string                `json:"defaults"`
	Budget        clientcatalog.Result    `json:"budget"`
	ClientVersion string                  `json:"client_version"`
}

type ClientCatalogRequest struct {
	KnownModels    []string                          `json:"known_models"`
	Models         []string                          `json:"models"`
	ResetDirectory bool                              `json:"reset_directory"`
	Profiles       []ClientModelProfileUpdateRequest `json:"profiles"`
}

func (s *Service) GetClientCatalog(ctx context.Context) (ClientCatalogDTO, error) {
	if err := ctx.Err(); err != nil {
		return ClientCatalogDTO{}, err
	}
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	return clientCatalogDTO(s.manager.Current())
}

func (s *Service) PreviewClientCatalog(ctx context.Context, request ClientCatalogRequest) (ClientCatalogDTO, error) {
	if err := ctx.Err(); err != nil {
		return ClientCatalogDTO{}, err
	}
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	draft, err := applyClientCatalogDraft(s.manager.Current(), request)
	if err != nil {
		return ClientCatalogDTO{}, err
	}
	return clientCatalogDTO(draft)
}

func (s *Service) UpdateClientCatalog(ctx context.Context, request ClientCatalogRequest) (ClientCatalogDTO, error) {
	snapshot, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		current := s.manager.Current()
		draft, err := applyClientCatalogDraft(current, request)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(draft.ClientModelOverrides))
		for name := range draft.ClientModelOverrides {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			overrides := draft.ClientModelOverrides[name]
			if reflect.DeepEqual(current.ClientModelOverrides[name], overrides) {
				continue
			}
			if err := saveClientModelOverrides(tx, name, overrides); err != nil {
				return err
			}
		}
		return nil
	}, nil)
	if err != nil {
		return ClientCatalogDTO{}, err
	}
	return clientCatalogDTO(snapshot)
}

func applyClientCatalogDraft(current *state.ConfigSnapshot, request ClientCatalogRequest) (*state.ConfigSnapshot, error) {
	if current == nil || (!request.ResetDirectory && request.Models == nil && len(request.Profiles) == 0 && request.KnownModels == nil) ||
		(request.ResetDirectory && len(request.Models) > 0) {
		return nil, app_errors.ErrValidation
	}
	draft := *current
	draft.ClientModelOverrides = make(map[string]catalog.ClientModelOverrides, len(current.ClientModelOverrides))
	for name, overrides := range current.ClientModelOverrides {
		draft.ClientModelOverrides[name] = overrides.Clone()
		if request.ResetDirectory {
			draft.ClientModelOverrides[name] = overrides.Metadata()
		}
	}
	candidates := clientcatalog.Candidates(current, state.AccessKeyView{})
	allowed := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		allowed[name] = struct{}{}
	}
	// 编辑过程中模型集合改变时拒绝旧草稿，避免把新模型误当成手动移出。
	if request.KnownModels != nil {
		if len(request.KnownModels) != len(candidates) {
			return nil, app_errors.ErrValidation
		}
		seen := make(map[string]struct{}, len(request.KnownModels))
		for _, name := range request.KnownModels {
			if _, exists := allowed[name]; !exists {
				return nil, app_errors.ErrValidation
			}
			if _, duplicate := seen[name]; duplicate {
				return nil, app_errors.ErrValidation
			}
			seen[name] = struct{}{}
		}
	}
	if !request.ResetDirectory && request.Models != nil {
		selected := make(map[string]int64, len(request.Models))
		for index, name := range request.Models {
			if _, exists := allowed[name]; !exists {
				return nil, app_errors.ErrValidation
			}
			if _, duplicate := selected[name]; duplicate {
				return nil, app_errors.ErrValidation
			}
			selected[name] = int64(index)
		}
		for _, name := range candidates {
			overrides := draft.ClientModelOverrides[name]
			order, enabled := selected[name]
			overrides.CatalogEnabled = nil
			if enabled != catalog.ClientModelCatalogEnabled(name, catalog.ClientModelOverrides{}) {
				overrides.CatalogEnabled = &enabled
			}
			overrides.CatalogOrder = nil
			if enabled {
				overrides.CatalogOrder = &order
			}
			draft.ClientModelOverrides[name] = overrides
		}
	}
	seenProfiles := make(map[string]struct{}, len(request.Profiles))
	for _, profile := range request.Profiles {
		if _, exists := allowed[profile.ClientModel]; !exists {
			return nil, app_errors.ErrValidation
		}
		if _, duplicate := seenProfiles[profile.ClientModel]; duplicate {
			return nil, app_errors.ErrValidation
		}
		seenProfiles[profile.ClientModel] = struct{}{}
		if profile.Overrides == nil || profile.Overrides.CatalogEnabled != nil || profile.Overrides.CatalogOrder != nil || profile.Overrides.Validate() != nil {
			return nil, app_errors.ErrValidation
		}
		overrides := profile.Overrides.Metadata()
		directory := draft.ClientModelOverrides[profile.ClientModel]
		overrides.CatalogEnabled, overrides.CatalogOrder = directory.CatalogEnabled, directory.CatalogOrder
		draft.ClientModelOverrides[profile.ClientModel] = overrides
	}
	return &draft, nil
}

func clientCatalogDTO(snapshot *state.ConfigSnapshot) (ClientCatalogDTO, error) {
	result := ClientCatalogDTO{
		Models: []ClientModelProfileDTO{}, Selected: clientcatalog.Selected(snapshot, state.AccessKeyView{}),
		Defaults: []string{}, ClientVersion: catalog.CodexModelCatalogVersion,
	}
	candidates := clientcatalog.Candidates(snapshot, state.AccessKeyView{})
	for _, name := range candidates {
		profile, err := clientModelProfile(snapshot, name)
		if err != nil {
			return ClientCatalogDTO{}, err
		}
		result.Models = append(result.Models, profile)
		if catalog.ClientModelCatalogEnabled(name, catalog.ClientModelOverrides{}) {
			result.Defaults = append(result.Defaults, name)
		}
	}
	catalog.SortClientModelCatalog(result.Defaults, nil)
	var err error
	result.Budget, err = clientcatalog.Build(snapshot, state.AccessKeyView{}, result.ClientVersion, clientcatalog.LimitBytes)
	return result, err
}
