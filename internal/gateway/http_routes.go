package gateway

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/state"
)

const dataPlaneRequestContextKey = "gpt-load.data-plane.request-context"

type dataPlaneRequestContext struct {
	requestStarted  time.Time
	selectedRoute   route
	snapshot        *state.ConfigSnapshot
	accessKey       state.AccessKeyView
	authenticated   bool
	locallyRejected bool
}

// HTTPModule declares the complete data-plane HTTP surface.
func (handler *Handler) HTTPModule() httproute.Module {
	catalog := dataPlaneEndpointCatalog()
	routes := make([]httproute.Route, 0, len(catalog))
	for _, endpoint := range catalog {
		selectedEndpoint := endpoint
		routes = append(routes, httproute.Route{
			Name:          selectedEndpoint.name,
			Methods:       append([]string(nil), selectedEndpoint.methods...),
			Path:          selectedEndpoint.path,
			PathValidator: selectedEndpoint.pathValidator,
			Prepare: gin.HandlersChain{
				handler.prepareDataPlaneRequest(selectedEndpoint),
			},
			Handlers: gin.HandlersChain{handler.Handle},
		})
	}

	module := httproute.Module{
		Name:              "data",
		Owner:             httproute.OwnerData,
		Auth:              httproute.AuthAccessKey,
		NamespacePrefixes: []string{"/v1", "/v1beta"},
		Authenticate:      handler.authenticateDataPlaneRequest,
		Routes:            routes,
		NotFound:          handler.dataPlaneRouteNotFound,
		MethodNotAllowed:  handler.dataPlaneMethodNotAllowed,
	}
	if handler != nil && handler.lifecycle != nil {
		module.BeforeAuth = gin.HandlersChain{handler.bindDataPlaneRequest}
	}
	return module
}

func (handler *Handler) bindDataPlaneRequest(ginContext *gin.Context) {
	if handler == nil || handler.lifecycle == nil || ginContext == nil || ginContext.Request == nil {
		return
	}
	requestContext, release, accepted := handler.lifecycle.BindData(ginContext.Request)
	if !accepted {
		ginContext.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	ginContext.Request = ginContext.Request.WithContext(requestContext)
	defer release()
	ginContext.Next()
}

func (handler *Handler) prepareDataPlaneRequest(
	endpoint dataPlaneEndpoint,
) gin.HandlerFunc {
	return func(ginContext *gin.Context) {
		if ginContext == nil || ginContext.Request == nil {
			return
		}
		requestNow := time.Now
		if handler != nil && handler.requestNow != nil {
			requestNow = handler.requestNow
		}
		ginContext.Set(dataPlaneRequestContextKey, &dataPlaneRequestContext{
			requestStarted: requestNow(),
			selectedRoute:  endpoint.resolve(ginContext.Request),
			locallyRejected: endpoint.rejectAfterAuth != nil &&
				endpoint.rejectAfterAuth(ginContext.Request),
		})
	}
}

func (handler *Handler) authenticateDataPlaneRequest(ginContext *gin.Context) {
	requestContext, ok := dataPlaneRequestContextFrom(ginContext)
	if !ok || handler == nil || handler.manager == nil {
		handler.dataPlaneRouteNotFound(ginContext)
		ginContext.Abort()
		return
	}

	snapshot := handler.manager.Current()
	initializeDebugHeaders(ginContext.Writer.Header())
	accessKey, authenticated := authenticate(
		ginContext.Request,
		snapshot,
		handler.encryption,
	)
	if !authenticated {
		handler.logDataPlaneAuthFailed(ginContext.Request)
		_ = handler.writeReason(ginContext, reasonInvalidAccessKey)
		ginContext.Abort()
		return
	}

	requestContext.snapshot = snapshot
	requestContext.accessKey = accessKey
	requestContext.authenticated = true
}

func dataPlaneRequestContextFrom(
	ginContext *gin.Context,
) (*dataPlaneRequestContext, bool) {
	if ginContext == nil {
		return nil, false
	}
	value, exists := ginContext.Get(dataPlaneRequestContextKey)
	if !exists {
		return nil, false
	}
	requestContext, ok := value.(*dataPlaneRequestContext)
	return requestContext, ok && requestContext != nil
}

func (handler *Handler) dataPlaneRouteNotFound(ginContext *gin.Context) {
	if handler == nil || ginContext == nil {
		return
	}
	_ = handler.writeReason(ginContext, reasonEndpointNotFound)
}

func (handler *Handler) dataPlaneMethodNotAllowed(ginContext *gin.Context) {
	if handler == nil || ginContext == nil {
		return
	}
	_ = handler.writeReason(ginContext, reasonMethodNotAllowed)
}
