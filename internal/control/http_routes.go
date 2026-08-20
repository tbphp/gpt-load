package control

import (
	"net/http"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/response"
)

const (
	controlRouteNotFoundMessageID    = "route.not_found"
	controlMethodNotAllowedMessageID = "route.method_not_allowed"
)

var (
	controlRouteNotFoundError = &app_errors.APIError{
		HTTPStatus: http.StatusNotFound,
		Code:       "ROUTE_NOT_FOUND",
		Message:    controlRouteNotFoundMessageID,
	}
	controlMethodNotAllowedError = &app_errors.APIError{
		HTTPStatus: http.StatusMethodNotAllowed,
		Code:       "METHOD_NOT_ALLOWED",
		Message:    controlMethodNotAllowedMessageID,
	}
)

// HTTPModule declares the complete authenticated control-plane HTTP surface.
func (s *Server) HTTPModule() httproute.Module {
	return httproute.Module{
		Name:              "control",
		Owner:             httproute.OwnerControl,
		Auth:              httproute.AuthControl,
		Prefix:            "/api",
		NamespacePrefixes: []string{"/api"},
		BeforeAuth:        gin.HandlersChain{i18n.Middleware()},
		Authenticate:      s.authenticate(),
		NotFound:          controlRouteNotFound,
		MethodNotAllowed:  controlMethodNotAllowed,
		Routes: []httproute.Route{
			controlRoute("control.auth.session", http.MethodGet, "/auth/session", s.handleAuthSession),
			controlRoute(
				"control.credential-stages.authorize",
				http.MethodPost,
				"/credential-stages/authorizations",
				s.auditMutation(newMutationDescriptor("credential_stage_authorize", "credential_stage", staticMutationLocator("new"))),
				s.handleBeginCredentialAuthorization,
			),
			controlRoute(
				"control.credential-stages.import",
				http.MethodPost,
				"/credential-stages/import",
				s.auditMutation(newMutationDescriptor("credential_stage_import", "credential_stage", staticMutationLocator("new"))),
				s.handleImportCredentialStage,
			),
			controlRoute("control.credential-stages.get", http.MethodGet, "/credential-stages/:stage_id", s.handleGetCredentialStage),
			controlRoute(
				"control.credential-stages.oauth-callback",
				http.MethodPost,
				"/credential-stages/:stage_id/oauth-callback",
				s.auditMutation(newMutationDescriptor("credential_stage_oauth_callback", "credential_stage", credentialStageMutationLocator)),
				s.handleCredentialAuthorizationCallback,
			),
			controlRoute(
				"control.credential-stages.device-poll",
				http.MethodPost,
				"/credential-stages/:stage_id/device-poll",
				s.auditMutation(newMutationDescriptor("credential_stage_device_poll", "credential_stage", credentialStageMutationLocator)),
				s.handlePollCredentialDeviceAuthorization,
			),
			controlRoute(
				"control.credential-stages.cancel",
				http.MethodDelete,
				"/credential-stages/:stage_id",
				s.auditMutation(newMutationDescriptor("credential_stage_cancel", "credential_stage", credentialStageMutationLocator)),
				s.handleCancelCredentialStage,
			),
			controlRoute("control.channels.list", http.MethodGet, "/channels", s.handleListChannels),
			controlRoute("control.models.list", http.MethodGet, "/models", s.handleListProjectModels),
			controlRoute(
				"control.model-prices.detail",
				http.MethodGet,
				"/model-prices/:id",
				s.handleGetUpstreamModelDetail,
			),
			controlRoute(
				"control.model-prices.list",
				http.MethodGet,
				"/model-prices",
				s.handleListModelPrices,
			),
			controlRoute(
				"control.model-prices.sync",
				http.MethodPost,
				"/model-prices/sync",
				s.auditMutation(newMutationDescriptor(
					"model_prices_sync",
					"model_price",
					staticMutationLocator("model-prices:catalog"),
				)),
				s.handleSyncModelPrices,
			),
			controlRoute(
				"control.model-prices.update",
				http.MethodPut,
				"/model-prices/:id",
				s.auditMutation(newMutationDescriptor(
					"model_price_update",
					"model_price",
					modelPriceMutationLocator,
				)),
				s.handleUpdateModelPrice,
			),
			controlRoute(
				"control.model-prices.reset",
				http.MethodPost,
				"/model-prices/:id/reset",
				s.auditMutation(newMutationDescriptor(
					"model_price_reset",
					"model_price",
					modelPriceMutationLocator,
				)),
				s.handleResetModelPrice,
			),
			controlRoute(
				"control.model-prices.delete",
				http.MethodDelete,
				"/model-prices/:id",
				s.auditMutation(newMutationDescriptor(
					"model_price_delete",
					"model_price",
					modelPriceMutationLocator,
				)),
				s.handleDeleteModelPrice,
			),
			controlRoute("control.home", http.MethodGet, "/home", s.handleHome),
			controlRoute(
				"control.home.statistics",
				http.MethodGet,
				"/home/statistics",
				s.handleHomeStatistics,
			),
			controlRoute("control.health", http.MethodGet, "/health", s.handleRuntimeHealth),
			controlRoute("control.logs.list", http.MethodGet, "/logs", s.handleListRequestLogs),
			controlRoute("control.logs.get", http.MethodGet, "/logs/:request_id", s.handleGetRequestLog),
			controlRoute("control.usage", http.MethodGet, "/usage", s.handleUsage),
			controlRoute("control.route.inspect", http.MethodPost, "/route/inspect", s.handleRouteInspect),
			controlRoute("control.settings.get", http.MethodGet, "/settings", s.handleGetSettings),
			controlRoute(
				"control.settings.update",
				http.MethodPut,
				"/settings",
				s.auditMutation(newMutationDescriptor(
					"settings_update",
					"settings",
					staticMutationLocator("settings:global"),
				)),
				s.handleUpdateSettings,
			),
			controlRoute("control.system.info", http.MethodGet, "/system/info", s.handleSystemInfo),
			controlRoute("control.system.update", http.MethodGet, "/system/update", s.handleSystemUpdate),
			controlRoute(
				"control.groups.list",
				http.MethodGet,
				"/groups",
				s.handleListGroupCollection,
			),
			controlRoute(
				"control.groups.options",
				http.MethodGet,
				"/groups/options",
				s.handleListGroupOptions,
			),
			controlRoute("control.groups.get", http.MethodGet, "/groups/:group_id", s.handleGetGroupSummary),
			controlRoute(
				"control.groups.settings.get",
				http.MethodGet,
				"/groups/:group_id/settings",
				s.handleGetGroupSettings,
			),
			controlRoute(
				"control.groups.create",
				http.MethodPost,
				"/groups",
				s.auditMutation(newMutationDescriptor(
					"group_create",
					"group",
					staticMutationLocator("new"),
				)),
				s.handleCreateGroup,
			),
			controlRoute(
				"control.groups.settings.update",
				http.MethodPut,
				"/groups/:group_id/settings",
				s.auditMutation(newMutationDescriptor(
					"group_settings_update",
					"group",
					groupMutationLocator,
				)),
				s.handleUpdateGroupSettings,
			),
			controlRoute(
				"control.groups.retired-update",
				http.MethodPut,
				"/groups/:group_id",
				controlRouteNotFound,
			),
			controlRoute(
				"control.groups.models.get",
				http.MethodGet,
				"/groups/:group_id/models",
				s.handleGetGroupModels,
			),
			controlRoute(
				"control.groups.models.update",
				http.MethodPut,
				"/groups/:group_id/models",
				s.auditMutation(newMutationDescriptor(
					"group_update_models",
					"group",
					groupMutationLocator,
				)),
				s.handleUpdateGroupModels,
			),
			controlRoute(
				"control.groups.delete",
				http.MethodDelete,
				"/groups/:group_id",
				s.auditMutation(newMutationDescriptor(
					"group_delete",
					"group",
					groupMutationLocator,
				)),
				s.handleDeleteGroup,
			),
			controlRoute(
				"control.group-credentials.list",
				http.MethodGet,
				"/groups/:group_id/credentials",
				s.handleListGroupCredentials,
			),
			controlRoute(
				"control.group-credentials.detail",
				http.MethodGet,
				"/groups/:group_id/credentials/:credential_id",
				s.handleGetGroupCredential,
			),
			controlRoute(
				"control.group-credentials.observation-refresh",
				http.MethodPost,
				"/groups/:group_id/credentials/:credential_id/observation-refresh",
				s.auditMutation(newMutationDescriptor(
					"group_credential_observation_refresh",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleRefreshGroupCredentialObservation,
			),
			controlRoute(
				"control.group-credentials.refresh",
				http.MethodPost,
				"/groups/:group_id/credentials/:credential_id/refresh",
				s.auditMutation(newMutationDescriptor(
					"group_credential_refresh",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleRefreshGroupCredential,
			),
			controlRoute(
				"control.group-credentials.reset-credit-consume",
				http.MethodPost,
				"/groups/:group_id/credentials/:credential_id/reset-credits/consume",
				s.auditMutation(newMutationDescriptor(
					"group_credential_reset_credit_consume",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleConsumeGroupCredentialResetCredit,
			),
			controlRoute(
				"control.group-credentials.reveal",
				http.MethodPost,
				"/groups/:group_id/credentials/:credential_id/reveal",
				s.auditMutation(newMutationDescriptor(
					"group_credential_reveal",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleRevealGroupCredential,
			),
			controlRoute(
				"control.group-credentials.download",
				http.MethodPost,
				"/groups/:group_id/credentials/:credential_id/download",
				s.auditMutation(newMutationDescriptor(
					"group_credential_download",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleDownloadGroupCredential,
			),
			controlRoute(
				"control.group-credentials.update",
				http.MethodPut,
				"/groups/:group_id/credentials/:credential_id",
				s.auditMutation(newMutationDescriptor(
					"group_credential_update",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleUpdateGroupCredential,
			),
			controlRoute(
				"control.group-credentials.restore",
				http.MethodPost,
				"/groups/:group_id/credentials/:credential_id/restore",
				s.auditMutation(newMutationDescriptor(
					"group_credential_restore",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleRestoreGroupCredential,
			),
			controlRoute(
				"control.group-credentials.batch",
				http.MethodPost,
				"/groups/:group_id/credentials/batch",
				s.auditMutation(newMutationDescriptor(
					"group_credentials_batch",
					"group_credential",
					groupCredentialsMutationLocator,
				)),
				s.handleBatchGroupCredentials,
			),
			controlRoute(
				"control.group-credentials.delete",
				http.MethodDelete,
				"/groups/:group_id/credentials/:credential_id",
				s.auditMutation(newMutationDescriptor(
					"group_credential_delete",
					"group_credential",
					groupCredentialMutationLocator,
				)),
				s.handleDeleteGroupCredential,
			),
			controlRoute(
				"control.group-credentials.import",
				http.MethodPost,
				"/groups/:group_id/credentials/import",
				s.auditMutation(newMutationDescriptor(
					"group_credential_import",
					"group_credential",
					groupCredentialsMutationLocator,
				)),
				s.handleImportGroupCredentials,
			),
			controlRoute(
				"control.group-credentials.connect",
				http.MethodPost,
				"/groups/:group_id/credentials/connect",
				s.auditMutation(newMutationDescriptor("group_credential_connect", "group_credential", groupCredentialsMutationLocator)),
				s.handleConnectGroupCredentials,
			),
			controlRoute(
				"control.group-models.discover",
				http.MethodPost,
				"/groups/:group_id/models/discover",
				s.handleDiscoverGroupModels,
			),
			controlRoute(
				"control.models.discover",
				http.MethodPost,
				"/models/discover",
				s.handleDiscoverModels,
			),
			controlRoute(
				"control.access-keys.create",
				http.MethodPost,
				"/access-keys",
				s.auditMutation(newMutationDescriptor(
					"access_key_create",
					"access_key",
					staticMutationLocator("new"),
				)),
				s.handleCreateAccessKey,
			),
			controlRoute(
				"control.access-keys.options",
				http.MethodGet,
				"/access-keys/options",
				s.handleListAccessKeyOptions,
			),
			controlRoute(
				"control.access-keys.reveal",
				http.MethodPost,
				"/access-keys/:id/reveal",
				s.auditMutation(newMutationDescriptor(
					"access_key_reveal",
					"access_key",
					accessKeyMutationLocator,
				)),
				s.handleRevealAccessKey,
			),
			controlRoute(
				"control.access-keys.list",
				http.MethodGet,
				"/access-keys",
				s.handleListAccessKeyCollection,
			),
			controlRoute(
				"control.access-keys.update",
				http.MethodPut,
				"/access-keys/:id",
				s.auditMutation(newMutationDescriptor(
					"access_key_update",
					"access_key",
					accessKeyMutationLocator,
				)),
				s.handleUpdateAccessKey,
			),
			controlRoute(
				"control.access-keys.delete",
				http.MethodDelete,
				"/access-keys/:id",
				s.auditMutation(newMutationDescriptor(
					"access_key_delete",
					"access_key",
					accessKeyMutationLocator,
				)),
				s.handleDeleteAccessKey,
			),
		},
	}
}

func controlRoute(
	name string,
	method string,
	path string,
	handlers ...gin.HandlerFunc,
) httproute.Route {
	return httproute.Route{
		Name:     name,
		Methods:  []string{method},
		Path:     path,
		Handlers: gin.HandlersChain(handlers),
	}
}

func controlRouteNotFound(c *gin.Context) {
	i18n.AttachRequestLanguage(c)
	response.ErrorI18nFromAPIError(
		c,
		controlRouteNotFoundError,
		controlRouteNotFoundMessageID,
	)
}

func controlMethodNotAllowed(c *gin.Context) {
	i18n.AttachRequestLanguage(c)
	response.ErrorI18nFromAPIError(
		c,
		controlMethodNotAllowedError,
		controlMethodNotAllowedMessageID,
	)
}
