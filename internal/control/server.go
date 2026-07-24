package control

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/response"
)

type Server struct {
	authDigest    [sha256.Size]byte
	service       *Service
	authFailures  *authFailureLimiter
	compareDigest func([]byte, []byte) int
}

const maxControlJSONBodyBytes int64 = 32 << 20

func NewServer(cfg *config.Config, service *Service) *Server {
	return &Server{
		authDigest:    sha256.Sum256([]byte(cfg.AuthKey)),
		service:       service,
		authFailures:  newAuthFailureLimiter(),
		compareDigest: subtle.ConstantTimeCompare,
	}
}

func (s *Server) RegisterRoutes(engine *gin.Engine) {
	api := engine.Group("/api")
	api.Use(i18n.Middleware(), s.authenticate())
	api.GET("/auth/session", s.handleAuthSession)
	api.GET("/health", s.handleRuntimeHealth)
	api.GET("/logs", s.handleListRequestLogs)
	api.POST("/route/inspect", s.handleRouteInspect)
	api.GET("/groups", s.handleListGroups)
	api.GET("/groups/:group_id", s.handleGetGroup)
	api.POST("/groups", s.handleCreateGroup)
	api.PUT("/groups/:group_id", s.handleUpdateGroup)
	api.PUT("/groups/:group_id/models", s.handleUpdateGroupModels)
	api.DELETE("/groups/:group_id", s.handleDeleteGroup)
	api.GET("/groups/:group_id/keys", s.handleListGroupKeys)
	api.PUT("/groups/:group_id/keys/:key_id", s.handleUpdateGroupKey)
	api.DELETE("/groups/:group_id/keys/:key_id", s.handleDeleteGroupKey)
	api.POST("/groups/:group_id/keys/import", s.handleImportGroupKeys)
	api.POST("/groups/:group_id/models/discover", s.handleDiscoverGroupModels)
	api.POST("/models/discover", s.handleDiscoverModels)
	api.POST("/access-keys", s.handleCreateAccessKey)
	api.GET("/access-keys", s.handleListAccessKeys)
	api.PUT("/access-keys/:id", s.handleUpdateAccessKey)
	api.DELETE("/access-keys/:id", s.handleDeleteAccessKey)
}

func (s *Server) handleListGroups(c *gin.Context) {
	groups, err := s.service.ListGroups(c.Request.Context())
	if err != nil {
		writeServiceError(c, "list_groups", err)
		return
	}
	response.SuccessI18n(c, "common.success", groups)
}

func (s *Server) handleGetGroup(c *gin.Context) {
	id, ok := groupID(c, "get_group")
	if !ok {
		return
	}
	result, err := s.service.GetGroup(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, "get_group", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleCreateGroup(c *gin.Context) {
	var request GroupCreateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "create_group", mapControlJSONError(err))
		return
	}
	result, err := s.service.CreateGroup(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "create_group", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateGroup(c *gin.Context) {
	id, ok := groupID(c, "update_group")
	if !ok {
		return
	}
	var request GroupUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_group", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateGroup(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "update_group", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateGroupModels(c *gin.Context) {
	id, ok := groupID(c, "update_group_models")
	if !ok {
		return
	}
	var request GroupModelsUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_group_models", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateGroupModels(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "update_group_models", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDeleteGroup(c *gin.Context) {
	id, ok := groupID(c, "delete_group")
	if !ok {
		return
	}
	if err := s.service.DeleteGroup(c.Request.Context(), id); err != nil {
		writeServiceError(c, "delete_group", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func (s *Server) handleListGroupKeys(c *gin.Context) {
	id, ok := groupID(c, "list_group_keys")
	if !ok {
		return
	}
	result, err := s.service.ListGroupKeys(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, "list_group_keys", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateGroupKey(c *gin.Context) {
	groupID, ok := groupID(c, "update_group_key")
	if !ok {
		return
	}
	keyID, ok := keyID(c, "update_group_key")
	if !ok {
		return
	}
	var request UpstreamKeyUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_group_key", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateGroupKey(
		c.Request.Context(),
		groupID,
		keyID,
		request,
	)
	if err != nil {
		writeServiceError(c, "update_group_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDeleteGroupKey(c *gin.Context) {
	groupID, ok := groupID(c, "delete_group_key")
	if !ok {
		return
	}
	keyID, ok := keyID(c, "delete_group_key")
	if !ok {
		return
	}
	if err := s.service.DeleteGroupKey(
		c.Request.Context(),
		groupID,
		keyID,
	); err != nil {
		writeServiceError(c, "delete_group_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func (s *Server) handleImportGroupKeys(c *gin.Context) {
	id, ok := groupID(c, "import_group_keys")
	if !ok {
		return
	}
	var request GroupKeyImportRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "import_group_keys", mapControlJSONError(err))
		return
	}
	result, err := s.service.ImportGroupKeys(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "import_group_keys", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDiscoverGroupModels(c *gin.Context) {
	id, ok := groupID(c, "discover_group_models")
	if !ok {
		return
	}
	if err := bindOptionalEmptyJSONObject(c); err != nil {
		writeServiceError(c, "discover_group_models", mapControlJSONError(err))
		return
	}
	result, err := s.service.DiscoverGroupModels(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, "discover_group_models", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDiscoverModels(c *gin.Context) {
	var request ModelDiscoveryRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeDiscoveryError(c, mapControlJSONError(err))
		return
	}
	result, err := s.service.DiscoverModels(c.Request.Context(), request)
	if err != nil {
		writeDiscoveryError(c, err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleCreateAccessKey(c *gin.Context) {
	var request AccessKeyCreateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "create_access_key", mapControlJSONError(err))
		return
	}
	result, err := s.service.CreateAccessKey(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "create_access_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleListAccessKeys(c *gin.Context) {
	result, err := s.service.ListAccessKeys(c.Request.Context())
	if err != nil {
		writeServiceError(c, "list_access_keys", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateAccessKey(c *gin.Context) {
	id, ok := accessKeyID(c)
	if !ok {
		return
	}
	var request AccessKeyUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_access_key", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateAccessKey(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "update_access_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDeleteAccessKey(c *gin.Context) {
	id, ok := accessKeyID(c)
	if !ok {
		return
	}
	if err := s.service.DeleteAccessKey(c.Request.Context(), id); err != nil {
		writeServiceError(c, "delete_access_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func bindStrictJSON(c *gin.Context, target any) error {
	decoder, err := newControlJSONDecoder(c)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if len(raw) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return fmt.Errorf("request body must be a JSON object")
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode JSON request: multiple values")
		}
		return err
	}

	strictDecoder := json.NewDecoder(bytes.NewReader(raw))
	strictDecoder.UseNumber()
	strictDecoder.DisallowUnknownFields()
	if err := strictDecoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func bindOptionalEmptyJSONObject(c *gin.Context) error {
	decoder, err := newControlJSONDecoder(c)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if object == nil || len(object) != 0 {
		return fmt.Errorf("request body must be an empty JSON object")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode JSON request: multiple values")
		}
		return err
	}
	return nil
}

func newControlJSONDecoder(c *gin.Context) (*json.Decoder, error) {
	if c.Request.ContentLength > maxControlJSONBodyBytes {
		return nil, &http.MaxBytesError{Limit: maxControlJSONBodyBytes}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxControlJSONBodyBytes)
	return json.NewDecoder(c.Request.Body), nil
}

func mapControlJSONError(err error) *app_errors.APIError {
	if errors.Is(err, app_errors.ErrValidation) {
		return app_errors.ErrValidation
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return app_errors.ErrRequestTooLarge
	}
	return app_errors.ErrInvalidJSON
}

func accessKeyID(c *gin.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		writeServiceError(c, "access_key_id", app_errors.ErrBadRequest)
		return 0, false
	}
	return uint(parsed), true
}

func groupID(c *gin.Context, operation string) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("group_id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		writeServiceError(c, operation, app_errors.ErrBadRequest)
		return 0, false
	}
	return uint(parsed), true
}

func keyID(c *gin.Context, operation string) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("key_id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		writeServiceError(c, operation, app_errors.ErrBadRequest)
		return 0, false
	}
	return uint(parsed), true
}

func writeServiceError(c *gin.Context, operation string, err error) {
	writeServiceErrorResponse(c, operation, err)
}

func writeDiscoveryError(c *gin.Context, err error) {
	writeServiceErrorResponse(c, "discover_models", err)
}

func writeServiceErrorResponse(
	c *gin.Context,
	operation string,
	err error,
) {
	var apiErr *app_errors.APIError
	if errors.As(err, &apiErr) {
		if apiErr.HTTPStatus >= http.StatusInternalServerError {
			logServiceError(operation, err, apiErr.Code)
		}
		response.ErrorI18nFromAPIError(
			c,
			apiErr,
			serviceErrorMessageID(operation, err, apiErr),
		)
		return
	}

	logServiceError(operation, err, app_errors.ErrInternalServer.Code)
	response.ErrorI18nFromAPIError(c, app_errors.ErrInternalServer, "internal_error")
}

func serviceErrorMessageID(
	operation string,
	err error,
	apiErr *app_errors.APIError,
) string {
	switch apiErr.Code {
	case app_errors.ErrRequestTooLarge.Code:
		return "request_too_large"
	case app_errors.ErrBadRequest.Code, app_errors.ErrInvalidJSON.Code, app_errors.ErrValidation.Code:
		return "bad_request"
	case app_errors.ErrResourceNotFound.Code:
		var resourceErr *controlResourceNotFoundError
		if errors.As(err, &resourceErr) {
			if resourceErr.resource == "group" {
				return "group.not_found"
			}
			return "key.not_found"
		}
		switch operation {
		case "list_groups", "get_group", "update_group", "delete_group",
			"update_group_models", "import_group_keys",
			"discover_group_models", "list_group_keys":
			return "group.not_found"
		default:
			return "key.not_found"
		}
	case app_errors.ErrNoActiveUpstreamKey.Code:
		return "group.no_active_upstream_key"
	case app_errors.ErrDuplicateResource.Code:
		if operation == "create_group" || operation == "update_group" {
			return "group.name_exists"
		}
		return "bad_request"
	case app_errors.ErrUpstreamURLChangeConfirmationRequired.Code:
		return "group.upstream_url_change_confirmation_required"
	case app_errors.ErrGroupInUse.Code:
		return "group.in_use"
	case app_errors.ErrUpstreamURLConflict.Code:
		return "group.upstream_url_conflict"
	case app_errors.ErrBadGateway.Code:
		return "bad_gateway"
	default:
		return "internal_error"
	}
}

func logServiceError(operation string, err error, code string) {
	fields := logrus.Fields{
		"operation":  operation,
		"error_code": code,
		"error_type": fmt.Sprintf("%T", err),
	}
	var operationErr *controlOperationError
	if errors.As(err, &operationErr) {
		if operationErr.stage != "" {
			fields["stage"] = operationErr.stage
		}
		if operationErr.mismatchKind != "" {
			fields["mismatch_kind"] = operationErr.mismatchKind
		}
		if operationErr.groupID != 0 {
			fields["group_id"] = operationErr.groupID
		}
		if operationErr.keyID != 0 {
			fields["key_id"] = operationErr.keyID
		}
	}
	logrus.WithFields(fields).Error("Control operation failed")
}
