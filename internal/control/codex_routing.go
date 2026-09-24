package control

import (
	"context"
	"errors"
	"sort"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/codexrouting"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/subscription/providers/codex"
)

type codexRoutingProbeRequest struct {
	CredentialID uint   `json:"credential_id"`
	Model        string `json:"model"`
}

type codexRoutingClearRequest struct {
	CredentialID uint `json:"credential_id"`
	Confirm      bool `json:"confirm"`
}

type codexRoutingEventsResponse struct {
	Items []codexrouting.Event `json:"items"`
}

func (s *Service) SetCodexRouting(store *codexrouting.Store, keeper *codexrouting.Keeper) {
	if s == nil {
		return
	}
	s.codexRouting = store
	s.codexProbe = keeper
}

func (s *Service) CodexAccounts() []codexrouting.AccountRef {
	if s == nil || s.manager == nil || s.registrySnapshot == nil {
		return nil
	}
	snapshot := s.manager.Current()
	if snapshot == nil {
		return nil
	}
	refs := make([]codexrouting.AccountRef, 0)
	for _, view := range s.registrySnapshot() {
		group, ok := snapshot.Groups[view.GroupID]
		if !ok || group.ChannelID != channel.Codex {
			continue
		}
		refs = append(refs, codexrouting.AccountRef{
			CredentialID: view.ID,
			GroupID:      view.GroupID,
			GroupName:    group.Name,
		})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].CredentialID < refs[j].CredentialID })
	return refs
}

func (s *Service) CodexToken(ctx context.Context, credentialID uint) (string, string, error) {
	if s == nil {
		return "", "", app_errors.ErrInternalServer
	}
	var groupID uint
	for _, account := range s.CodexAccounts() {
		if account.CredentialID == credentialID {
			groupID = account.GroupID
			break
		}
	}
	if groupID == 0 || credentialID == 0 {
		return "", "", app_errors.ErrResourceNotFound
	}
	group, row, _, err := s.loadObservationTarget(ctx, groupID, credentialID)
	if err != nil {
		return "", "", err
	}
	prepared, err := s.prepareStoredSubscriptionCredential(ctx, group, row)
	if err != nil {
		return "", "", err
	}
	canonical := prepared.Canonical()
	defer clear(canonical)
	credential, err := codex.ParseCredentialJSON(canonical)
	if err != nil {
		return "", "", app_errors.ErrValidation
	}
	if credential.AccessToken == "" {
		return "", "", app_errors.ErrValidation
	}
	return credential.AccessToken, credential.AccountID, nil
}

func (s *Service) CodexRoutingStatus() codexrouting.Status {
	if s == nil || s.codexRouting == nil {
		return codexrouting.Status{Credentials: []codexrouting.CredentialStatus{}}
	}
	return s.codexRouting.Status(s.CodexAccounts())
}

func (s *Service) CodexRoutingEvents() []codexrouting.Event {
	if s == nil || s.codexRouting == nil {
		return []codexrouting.Event{}
	}
	return s.codexRouting.Events()
}

func (s *Service) ProbeCodexRouting(ctx context.Context, credentialID uint, model string) error {
	if s == nil || s.codexProbe == nil {
		return app_errors.ErrValidation
	}
	err := s.codexProbe.ProbeOne(ctx, credentialID, model)
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, codexrouting.ErrDisabled),
		errors.Is(err, codexrouting.ErrProbeProxyMissing),
		errors.Is(err, codexrouting.ErrAlreadyProbing):
		return app_errors.ErrValidation
	case errors.Is(err, app_errors.ErrResourceNotFound), errors.Is(err, app_errors.ErrValidation):
		return err
	default:
		return err
	}
}

func (s *Service) ClearCodexRouting(credentialID uint, confirm bool) error {
	if s == nil || s.codexRouting == nil {
		return app_errors.ErrValidation
	}
	if !confirm || credentialID == 0 {
		return app_errors.ErrValidation
	}
	s.codexRouting.Clear(credentialID)
	return nil
}

func (s *Server) handleCodexRoutingStatus(c *gin.Context) {
	if s == nil || s.service == nil {
		writeServiceError(c, "codex_routing_status", app_errors.ErrInternalServer)
		return
	}
	response.SuccessI18n(c, "common.success", s.service.CodexRoutingStatus())
}

func (s *Server) handleCodexRoutingEvents(c *gin.Context) {
	if s == nil || s.service == nil {
		writeServiceError(c, "codex_routing_events", app_errors.ErrInternalServer)
		return
	}
	response.SuccessI18n(c, "common.success", codexRoutingEventsResponse{Items: s.service.CodexRoutingEvents()})
}

func (s *Server) handleCodexRoutingProbe(c *gin.Context) {
	var body codexRoutingProbeRequest
	if err := bindStrictJSON(c, &body); err != nil {
		writeServiceError(c, "codex_routing_probe", mapControlJSONError(err))
		return
	}
	if body.CredentialID == 0 {
		writeServiceError(c, "codex_routing_probe", app_errors.ErrValidation)
		return
	}
	if err := s.service.ProbeCodexRouting(c.Request.Context(), body.CredentialID, body.Model); err != nil {
		writeServiceError(c, "codex_routing_probe", err)
		return
	}
	response.SuccessI18n(c, "common.success", s.service.CodexRoutingStatus())
}

func (s *Server) handleCodexRoutingClear(c *gin.Context) {
	var body codexRoutingClearRequest
	if err := bindStrictJSON(c, &body); err != nil {
		writeServiceError(c, "codex_routing_clear", mapControlJSONError(err))
		return
	}
	if err := s.service.ClearCodexRouting(body.CredentialID, body.Confirm); err != nil {
		writeServiceError(c, "codex_routing_clear", err)
		return
	}
	response.SuccessI18n(c, "common.success", s.service.CodexRoutingStatus())
}
