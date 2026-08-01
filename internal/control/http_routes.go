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
			controlRoute("control.home", http.MethodGet, "/home", s.handleHome),
			controlRoute(
				"control.home.statistics",
				http.MethodGet,
				"/home/statistics",
				s.handleHomeStatistics,
			),
			controlRoute("control.health", http.MethodGet, "/health", s.handleRuntimeHealth),
			controlRoute("control.logs.list", http.MethodGet, "/logs", s.handleListRequestLogs),
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
			controlRoute(
				"control.model-prices.list",
				http.MethodGet,
				"/model-prices",
				s.handleListModelPrices,
			),
			controlRoute(
				"control.model-prices.upsert",
				http.MethodPut,
				"/model-prices",
				s.auditMutation(newMutationDescriptor(
					"model_price_upsert",
					"model_price",
					staticMutationLocator("model-price:unknown"),
				)),
				s.handleUpsertModelPrice,
			),
			controlRoute(
				"control.model-prices.reset",
				http.MethodDelete,
				"/model-prices",
				s.auditMutation(newMutationDescriptor(
					"model_price_reset",
					"model_price",
					staticMutationLocator("model-price:unknown"),
				)),
				s.handleResetModelPrice,
			),
			controlRoute("control.system.info", http.MethodGet, "/system/info", s.handleSystemInfo),
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
			controlRoute("control.groups.get", http.MethodGet, "/groups/:group_id", s.handleGetGroup),
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
				"control.group-keys.list",
				http.MethodGet,
				"/groups/:group_id/keys",
				s.handleListGroupKeys,
			),
			controlRoute(
				"control.group-keys.update",
				http.MethodPut,
				"/groups/:group_id/keys/:key_id",
				s.auditMutation(newMutationDescriptor(
					"group_key_update",
					"group_key",
					groupKeyMutationLocator,
				)),
				s.handleUpdateGroupKey,
			),
			controlRoute(
				"control.group-keys.delete",
				http.MethodDelete,
				"/groups/:group_id/keys/:key_id",
				s.auditMutation(newMutationDescriptor(
					"group_key_delete",
					"group_key",
					groupKeyMutationLocator,
				)),
				s.handleDeleteGroupKey,
			),
			controlRoute(
				"control.group-keys.import",
				http.MethodPost,
				"/groups/:group_id/keys/import",
				s.auditMutation(newMutationDescriptor(
					"group_key_import",
					"group_key",
					groupKeysMutationLocator,
				)),
				s.handleImportGroupKeys,
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
				s.handleListAccessKeys,
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
