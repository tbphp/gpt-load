package control

import (
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/concurrency"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type concurrencyTarget struct {
	Scope string
	ID    uint
}

type ConcurrencyView struct {
	Scope                   string `json:"scope"`
	ID                      uint   `json:"id"`
	CurrentConcurrency      int64  `json:"current_concurrency"`
	MaxConcurrency          *int64 `json:"max_concurrency"`
	EffectiveMaxConcurrency int64  `json:"effective_max_concurrency"`
	Source                  string `json:"source"`
	Shared                  bool   `json:"shared"`
}

type ConcurrencyResponse struct {
	ObservedAtMS int64             `json:"observed_at_ms"`
	Items        []ConcurrencyView `json:"items"`
}

type ConcurrencyUpdateRequest struct {
	MaxConcurrency optionalField[int64] `json:"max_concurrency"`
}

func parseConcurrencyTarget(raw string) (concurrencyTarget, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return concurrencyTarget{}, app_errors.ErrBadRequest
	}
	id, err := parseCanonicalSafePlatformUint(parts[1])
	if err != nil {
		return concurrencyTarget{}, app_errors.ErrBadRequest
	}
	return concurrencyTarget{Scope: parts[0], ID: id}, nil
}

func deleteConcurrencyPolicies(tx *gorm.DB, subjects ...concurrency.Subject) error {
	if len(subjects) == 0 {
		return nil
	}
	values := make([]string, len(subjects))
	for index, subject := range subjects {
		values[index] = string(subject)
	}
	return tx.Where("subject IN ?", values).Delete(&models.ConcurrencyPolicy{}).Error
}

func deleteCredentialConcurrencyPolicies(tx *gorm.DB, credentialIDs []uint) error {
	subjects := make([]concurrency.Subject, len(credentialIDs))
	for index, credentialID := range credentialIDs {
		subjects[index] = concurrency.Credential(credentialID)
	}
	return deleteConcurrencyPolicies(tx, subjects...)
}

func validateConcurrencyOverride(value optionalField[int64]) error {
	if value.Set && !value.Null && (value.Value < 0 || value.Value > concurrency.MaximumLimit) {
		return app_errors.ErrValidation
	}
	return nil
}

func applyConcurrencyOverride(
	tx *gorm.DB,
	subject concurrency.Subject,
	value optionalField[int64],
) error {
	if !value.Set {
		return nil
	}
	if err := validateConcurrencyOverride(value); err != nil {
		return err
	}
	if value.Null {
		if err := tx.Where("subject = ?", string(subject)).Delete(&models.ConcurrencyPolicy{}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "subject"}},
		DoUpdates: clause.AssignmentColumns([]string{"max_concurrency"}),
	}).Create(&models.ConcurrencyPolicy{
		Subject: string(subject), MaxConcurrency: value.Value,
	}).Error; err != nil {
		return app_errors.ParseDBError(err)
	}
	return nil
}

func resolveConcurrencyTarget(snapshot *state.ConfigSnapshot, target concurrencyTarget) (concurrency.Subject, concurrency.Subject, bool, error) {
	if snapshot == nil {
		return "", "", false, app_errors.ErrInternalServer
	}
	switch target.Scope {
	case "global", "upstream", "default_group", "default_access_key", "default_credential":
		if target.ID == 0 {
			return concurrency.Subject(target.Scope), "", false, nil
		}
	case "group":
		if _, exists := snapshot.GroupCatalog[target.ID]; exists {
			return concurrency.Group(target.ID), concurrency.DefaultGroup, false, nil
		}
	case "access_key":
		if _, exists := snapshot.AccessKeysByID[target.ID]; exists {
			return concurrency.AccessKey(target.ID), concurrency.DefaultAccessKey, false, nil
		}
	case "credential":
		if view, exists := snapshot.CredentialConcurrency[target.ID]; exists {
			return view.Subject, concurrency.DefaultCredential, view.Shared, nil
		}
	}
	return "", "", false, app_errors.ErrResourceNotFound
}

func (s *Service) readConcurrency(targets []concurrencyTarget) (ConcurrencyResponse, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	snapshot := s.manager.Current()
	result := ConcurrencyResponse{ObservedAtMS: s.now().UnixMilli(), Items: make([]ConcurrencyView, 0, len(targets))}
	subjects := make([]concurrency.Subject, 0, len(targets))
	for _, target := range targets {
		subject, fallback, shared, err := resolveConcurrencyTarget(snapshot, target)
		if err != nil {
			return ConcurrencyResponse{}, err
		}
		view := ConcurrencyView{Scope: target.Scope, ID: target.ID, Source: "default", Shared: shared, EffectiveMaxConcurrency: snapshot.ConcurrencyLimit(subject, fallback)}
		if limit, exists := snapshot.ConcurrencyPolicies[subject]; exists {
			view.MaxConcurrency = &limit
			view.Source = "override"
		}
		result.Items = append(result.Items, view)
		subjects = append(subjects, subject)
	}
	counts := s.manager.ObserveConcurrency(subjects)
	for index, subject := range subjects {
		result.Items[index].CurrentConcurrency = counts[subject]
	}
	return result, nil
}

func (s *Service) updateConcurrency(ctx context.Context, target concurrencyTarget, request ConcurrencyUpdateRequest) error {
	if target.Scope == "upstream" || !request.MaxConcurrency.Set {
		return app_errors.ErrValidation
	}
	if err := validateConcurrencyOverride(request.MaxConcurrency); err != nil {
		return app_errors.ErrValidation
	}
	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		subject, _, _, err := resolveConcurrencyTarget(s.manager.Current(), target)
		if err != nil {
			return err
		}
		return applyConcurrencyOverride(tx, subject, request.MaxConcurrency)
	}, nil)
	return err
}

func (s *Server) handleGetConcurrency(c *gin.Context) {
	query := c.Request.URL.Query()
	values := query["targets"]
	if len(query) != 1 || len(values) != 1 || len(values[0]) > 4096 {
		writeServiceError(c, "concurrency", app_errors.ErrBadRequest)
		return
	}
	parts := strings.Split(values[0], ",")
	if len(parts) > 100 {
		writeServiceError(c, "concurrency", app_errors.ErrBadRequest)
		return
	}
	principal, ok := currentControlPrincipal(c)
	if !ok {
		writeServiceError(c, "concurrency", app_errors.ErrUnauthorized)
		return
	}
	targets := make([]concurrencyTarget, 0, len(parts))
	for _, part := range parts {
		target, err := parseConcurrencyTarget(part)
		if err != nil {
			writeServiceError(c, "concurrency", err)
			return
		}
		if principal.Type == controlPrincipalAccessKey && (target.Scope != "access_key" || target.ID != principal.AccessKeyID) {
			writeServiceError(c, "concurrency", app_errors.ErrForbidden)
			return
		}
		targets = append(targets, target)
	}
	result, err := s.service.readConcurrency(targets)
	if err != nil {
		writeServiceError(c, "concurrency", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateConcurrency(c *gin.Context) {
	target, err := parseConcurrencyTarget(c.Param("scope") + ":" + c.Param("id"))
	if err != nil {
		writeServiceError(c, "concurrency", err)
		return
	}
	var request ConcurrencyUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "concurrency", app_errors.ErrBadRequest)
		return
	}
	if err := s.service.updateConcurrency(c.Request.Context(), target, request); err != nil {
		writeServiceError(c, "concurrency", err)
		return
	}
	result, err := s.service.readConcurrency([]concurrencyTarget{target})
	if err != nil {
		writeServiceError(c, "concurrency", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func concurrencyMutationLocator(c *gin.Context) string {
	target, err := parseConcurrencyTarget(c.Param("scope") + ":" + c.Param("id"))
	if err != nil {
		return "unknown"
	}
	switch target.Scope {
	case "global", "default_group", "default_access_key", "default_credential":
		if target.ID == 0 {
			return target.Scope
		}
	case "group", "access_key", "credential":
		if target.ID > 0 {
			return target.Scope + ":" + strconv.FormatUint(uint64(target.ID), 10)
		}
	}
	return "unknown"
}
