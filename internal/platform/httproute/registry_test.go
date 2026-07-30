package httproute

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNewRegistryAcceptsOwnerAuthMatrixAndReturnsDefensiveRouteInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := respond(http.StatusNoContent, "")
	authenticate := func(c *gin.Context) {}
	modules := []Module{
		{
			Name:  "system",
			Owner: OwnerSystem,
			Auth:  AuthNone,
			Routes: []Route{{
				Name: "health", Methods: []string{http.MethodGet},
				Path: "/health", Handlers: gin.HandlersChain{handler},
			}},
		},
		{
			Name: "control", Owner: OwnerControl, Auth: AuthControl,
			Prefix: "/api", NamespacePrefixes: []string{"/api"},
			Authenticate: authenticate,
			Routes: []Route{{
				Name: "control-status", Methods: []string{http.MethodGet, http.MethodHead},
				Path: "/status", Handlers: gin.HandlersChain{handler},
			}},
		},
		{
			Name: "data", Owner: OwnerData, Auth: AuthAccessKey,
			NamespacePrefixes: []string{"/v1"}, Authenticate: authenticate,
			Routes: []Route{{
				Name: "messages", Methods: []string{http.MethodPost},
				Path: "/v1/messages", Handlers: gin.HandlersChain{handler},
			}},
		},
		{
			Name: "web", Owner: OwnerWeb, Auth: AuthNone,
			Routes: []Route{{
				Name: "home", Methods: []string{http.MethodGet},
				Path: "/", Handlers: gin.HandlersChain{handler},
			}},
		},
	}

	registry, err := NewRegistry(modules...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	modules[1].Routes[0].Methods[0] = http.MethodDelete

	got := registry.Routes()
	want := []RouteInfo{
		{
			ModuleName: "system", RouteName: "health",
			Owner: OwnerSystem, Auth: AuthNone,
			Methods: []string{http.MethodGet}, Path: "/health",
		},
		{
			ModuleName: "control", RouteName: "control-status",
			Owner: OwnerControl, Auth: AuthControl,
			Methods: []string{http.MethodGet, http.MethodHead}, Path: "/api/status",
		},
		{
			ModuleName: "data", RouteName: "messages",
			Owner: OwnerData, Auth: AuthAccessKey,
			Methods: []string{http.MethodPost}, Path: "/v1/messages",
		},
		{
			ModuleName: "web", RouteName: "home",
			Owner: OwnerWeb, Auth: AuthNone,
			Methods: []string{http.MethodGet}, Path: "/",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Routes() = %#v, want %#v", got, want)
	}

	got[0].Methods[0] = http.MethodPatch
	got[1].Path = "/mutated"
	got = registry.Routes()
	if got[0].Methods[0] != http.MethodGet || got[1].Path != "/api/status" {
		t.Fatalf("Routes() exposed registry state: %#v", got)
	}
}

func TestNewRegistryRejectsInvalidDefinitions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		modules func() []Module
	}{
		{
			name: "unknown owner",
			modules: func() []Module {
				module := validSystemModule()
				module.Owner = Owner("worker")
				return []Module{module}
			},
		},
		{
			name: "unknown auth policy",
			modules: func() []Module {
				module := validSystemModule()
				module.Auth = AuthPolicy("optional")
				return []Module{module}
			},
		},
		{
			name: "system requires none auth",
			modules: func() []Module {
				module := validSystemModule()
				module.Auth = AuthControl
				module.Authenticate = func(c *gin.Context) {}
				return []Module{module}
			},
		},
		{
			name: "web requires none auth",
			modules: func() []Module {
				module := validSystemModule()
				module.Owner = OwnerWeb
				module.Auth = AuthAccessKey
				module.Authenticate = func(c *gin.Context) {}
				return []Module{module}
			},
		},
		{
			name: "control requires control auth",
			modules: func() []Module {
				module := validAuthenticatedModule(OwnerControl, AuthAccessKey)
				return []Module{module}
			},
		},
		{
			name: "data requires access key auth",
			modules: func() []Module {
				module := validAuthenticatedModule(OwnerData, AuthControl)
				return []Module{module}
			},
		},
		{
			name: "authenticated module requires authenticator",
			modules: func() []Module {
				module := validAuthenticatedModule(OwnerControl, AuthControl)
				module.Authenticate = nil
				return []Module{module}
			},
		},
		{
			name: "none auth forbids authenticator",
			modules: func() []Module {
				module := validSystemModule()
				module.Authenticate = func(c *gin.Context) {}
				return []Module{module}
			},
		},
		{
			name: "empty module name",
			modules: func() []Module {
				module := validSystemModule()
				module.Name = ""
				return []Module{module}
			},
		},
		{
			name: "module name whitespace",
			modules: func() []Module {
				module := validSystemModule()
				module.Name = " system "
				return []Module{module}
			},
		},
		{
			name: "duplicate module name",
			modules: func() []Module {
				first := validSystemModule()
				second := validSystemModule()
				second.Routes[0].Name = "second-route"
				second.Routes[0].Path = "/second"
				return []Module{first, second}
			},
		},
		{
			name: "empty route name",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Name = ""
				return []Module{module}
			},
		},
		{
			name: "duplicate route name across modules",
			modules: func() []Module {
				first := validSystemModule()
				second := validSystemModule()
				second.Name = "second"
				second.Routes[0].Path = "/second"
				return []Module{first, second}
			},
		},
		{
			name: "route requires methods",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Methods = nil
				return []Module{module}
			},
		},
		{
			name: "method must be uppercase",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Methods = []string{"get"}
				return []Module{module}
			},
		},
		{
			name: "method must be HTTP token",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Methods = []string{"BAD METHOD"}
				return []Module{module}
			},
		},
		{
			name: "duplicate route method",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Methods = []string{http.MethodGet, http.MethodGet}
				return []Module{module}
			},
		},
		{
			name: "route path must be absolute",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Path = "health"
				return []Module{module}
			},
		},
		{
			name: "route path must not have trailing slash",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Path = "/health/"
				return []Module{module}
			},
		},
		{
			name: "route path must not contain dot segment",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Path = "/health/../ready"
				return []Module{module}
			},
		},
		{
			name: "route path must not contain empty segment",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Path = "/health//ready"
				return []Module{module}
			},
		},
		{
			name: "catch all must be final",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Path = "/files/*rest/more"
				return []Module{module}
			},
		},
		{
			name: "wildcard requires name",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Path = "/files/:"
				return []Module{module}
			},
		},
		{
			name: "route requires handler",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Handlers = nil
				return []Module{module}
			},
		},
		{
			name: "route handler must not be nil",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Handlers = gin.HandlersChain{nil}
				return []Module{module}
			},
		},
		{
			name: "prepare handler must not be nil",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes[0].Prepare = gin.HandlersChain{nil}
				return []Module{module}
			},
		},
		{
			name: "before auth handler must not be nil",
			modules: func() []Module {
				module := validSystemModule()
				module.BeforeAuth = gin.HandlersChain{nil}
				return []Module{module}
			},
		},
		{
			name: "prefix must be absolute",
			modules: func() []Module {
				module := validSystemModule()
				module.Prefix = "api"
				return []Module{module}
			},
		},
		{
			name: "prefix must not have trailing slash",
			modules: func() []Module {
				module := validSystemModule()
				module.Prefix = "/api/"
				return []Module{module}
			},
		},
		{
			name: "root prefix must be empty",
			modules: func() []Module {
				module := validSystemModule()
				module.Prefix = "/"
				return []Module{module}
			},
		},
		{
			name: "prefix must not normalize dot segments",
			modules: func() []Module {
				module := validSystemModule()
				module.Prefix = "/api/../control"
				return []Module{module}
			},
		},
		{
			name: "duplicate full method pattern",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes = append(module.Routes, Route{
					Name: "duplicate", Methods: []string{http.MethodGet},
					Path: "/health", Handlers: gin.HandlersChain{respond(http.StatusOK, "")},
				})
				return []Module{module}
			},
		},
		{
			name: "gin wildcard conflict",
			modules: func() []Module {
				module := validSystemModule()
				module.Routes = []Route{
					{
						Name: "files", Methods: []string{http.MethodGet},
						Path: "/files/*rest", Handlers: gin.HandlersChain{respond(http.StatusOK, "")},
					},
					{
						Name: "static-file", Methods: []string{http.MethodGet},
						Path: "/files/static", Handlers: gin.HandlersChain{respond(http.StatusOK, "")},
					},
				}
				return []Module{module}
			},
		},
		{
			name: "namespace must be absolute",
			modules: func() []Module {
				module := validSystemModule()
				module.NamespacePrefixes = []string{"api"}
				return []Module{module}
			},
		},
		{
			name: "duplicate namespace",
			modules: func() []Module {
				first := validSystemModule()
				first.NamespacePrefixes = []string{"/api"}
				second := validSystemModule()
				second.Name = "second"
				second.Routes[0].Name = "second-route"
				second.Routes[0].Path = "/second"
				second.NamespacePrefixes = []string{"/api"}
				return []Module{first, second}
			},
		},
		{
			name: "route must not cross a foreign namespace",
			modules: func() []Module {
				control := validAuthenticatedModule(OwnerControl, AuthControl)
				control.Prefix = "/api"
				control.NamespacePrefixes = []string{"/api"}
				control.Routes[0].Name = "control-health"

				web := validSystemModule()
				web.Name = "web"
				web.Owner = OwnerWeb
				web.Routes[0].Name = "web-debug"
				web.Routes[0].Path = "/api/debug"
				return []Module{control, web}
			},
		},
		{
			name: "dynamic route must not cover a foreign namespace",
			modules: func() []Module {
				control := validAuthenticatedModule(OwnerControl, AuthControl)
				control.Prefix = "/api"
				control.NamespacePrefixes = []string{"/api"}
				control.Routes[0].Name = "control-health"

				web := validSystemModule()
				web.Name = "web"
				web.Owner = OwnerWeb
				web.Routes[0].Name = "web-root-page"
				web.Routes[0].Path = "/:slug"
				return []Module{control, web}
			},
		},
		{
			name: "fallback requires name",
			modules: func() []Module {
				module := validSystemModule()
				module.Fallback = &Fallback{
					Match:   func(*http.Request) bool { return true },
					Handler: respond(http.StatusNotFound, "fallback"),
				}
				return []Module{module}
			},
		},
		{
			name: "fallback requires matcher",
			modules: func() []Module {
				module := validSystemModule()
				module.Fallback = &Fallback{
					Name: "fallback", Handler: respond(http.StatusNotFound, "fallback"),
				}
				return []Module{module}
			},
		},
		{
			name: "fallback requires handler",
			modules: func() []Module {
				module := validSystemModule()
				module.Fallback = &Fallback{
					Name: "fallback", Match: func(*http.Request) bool { return true },
				}
				return []Module{module}
			},
		},
		{
			name: "only one fallback",
			modules: func() []Module {
				first := validSystemModule()
				first.Fallback = validFallback("first")
				second := validSystemModule()
				second.Name = "second"
				second.Routes[0].Name = "second-route"
				second.Routes[0].Path = "/second"
				second.Fallback = validFallback("second")
				return []Module{first, second}
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewRegistry(testCase.modules()...); err == nil {
				t.Fatal("NewRegistry() error = nil, want validation error")
			}
		})
	}
}

func TestNewRegistryAllowsRouteInsideExplicitNestedNamespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	control := validAuthenticatedModule(OwnerControl, AuthControl)
	control.Prefix = "/api"
	control.NamespacePrefixes = []string{"/api"}
	control.Routes[0].Name = "control-health"

	web := validSystemModule()
	web.Name = "web"
	web.Owner = OwnerWeb
	web.NamespacePrefixes = []string{"/api/public"}
	web.Routes[0].Name = "web-public-page"
	web.Routes[0].Path = "/api/public/:slug"

	if _, err := NewRegistry(control, web); err != nil {
		t.Fatalf("NewRegistry() nested namespace error = %v", err)
	}
}

func TestBindRunsRouteChainInRequiredOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var order []string
	appendStep := func(step string) gin.HandlerFunc {
		return func(c *gin.Context) {
			order = append(order, step)
		}
	}
	module := Module{
		Name: "control", Owner: OwnerControl, Auth: AuthControl,
		BeforeAuth:   gin.HandlersChain{appendStep("before-auth")},
		Authenticate: appendStep("authenticate"),
		Routes: []Route{{
			Name: "resource", Methods: []string{http.MethodGet}, Path: "/resource",
			PathValidator: func(*http.Request) bool {
				order = append(order, "validator")
				return true
			},
			Prepare:  gin.HandlersChain{appendStep("prepare")},
			Handlers: gin.HandlersChain{appendStep("handler"), respond(http.StatusNoContent, "")},
		}},
	}
	registry := mustRegistry(t, module)
	engine := gin.New()
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))

	want := []string{"validator", "before-auth", "prepare", "authenticate", "handler"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("handler order = %#v, want %#v", order, want)
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
}

func TestBindRejectsInvalidSemanticPathBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authCalls := 0
	beforeAuthCalls := 0
	prepareCalls := 0
	handlerCalls := 0
	module := Module{
		Name: "data", Owner: OwnerData, Auth: AuthAccessKey,
		BeforeAuth: gin.HandlersChain{func(c *gin.Context) {
			beforeAuthCalls++
		}},
		Authenticate: func(c *gin.Context) {
			authCalls++
		},
		NotFound: respond(http.StatusNotFound, "data not found"),
		Routes: []Route{{
			Name: "gemini-generate", Methods: []string{http.MethodPost},
			Path: "/v1beta/models/:model_action",
			PathValidator: func(request *http.Request) bool {
				return strings.HasSuffix(request.URL.Path, ":generateContent")
			},
			Prepare: gin.HandlersChain{func(c *gin.Context) {
				prepareCalls++
			}},
			Handlers: gin.HandlersChain{func(c *gin.Context) {
				handlerCalls++
				c.Status(http.StatusNoContent)
			}},
		}},
	}
	registry := mustRegistry(t, module)
	engine := gin.New()
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/v1beta/models/missing-action", nil),
	)

	if recorder.Code != http.StatusNotFound || recorder.Body.String() != "data not found" {
		t.Fatalf("response = %d %q, want module 404", recorder.Code, recorder.Body.String())
	}
	if authCalls != 0 || beforeAuthCalls != 0 || prepareCalls != 0 || handlerCalls != 0 {
		t.Fatalf(
			"invalid path ran route chain: before=%d prepare=%d auth=%d handler=%d",
			beforeAuthCalls, prepareCalls, authCalls, handlerCalls,
		)
	}
}

func TestNoMethodRecomputesSortedAllowWithoutAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authCalls := 0
	module := Module{
		Name: "control", Owner: OwnerControl, Auth: AuthControl,
		Authenticate: func(c *gin.Context) {
			authCalls++
		},
		MethodNotAllowed: respond(http.StatusMethodNotAllowed, "control method not allowed"),
		Routes: []Route{{
			Name: "resource", Methods: []string{http.MethodPost, http.MethodGet},
			Path: "/resource", Handlers: gin.HandlersChain{respond(http.StatusNoContent, "")},
		}},
	}
	registry := mustRegistry(t, module)
	engine := gin.New()
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/resource", nil))

	if recorder.Code != http.StatusMethodNotAllowed ||
		recorder.Body.String() != "control method not allowed" {
		t.Fatalf("response = %d %q, want module 405", recorder.Code, recorder.Body.String())
	}
	if allow := recorder.Header().Get("Allow"); allow != "GET, POST" {
		t.Fatalf("Allow = %q, want %q", allow, "GET, POST")
	}
	if authCalls != 0 {
		t.Fatalf("auth calls = %d, want 0", authCalls)
	}
}

func TestNoMethodTurnsSemanticallyInvalidPatternMatchInto404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authCalls := 0
	methodNotAllowedCalls := 0
	module := Module{
		Name: "data", Owner: OwnerData, Auth: AuthAccessKey,
		Authenticate: func(c *gin.Context) {
			authCalls++
		},
		NotFound: respond(http.StatusNotFound, "invalid data route"),
		MethodNotAllowed: func(c *gin.Context) {
			methodNotAllowedCalls++
			c.Status(http.StatusMethodNotAllowed)
		},
		Routes: []Route{{
			Name: "gemini-generate", Methods: []string{http.MethodPost},
			Path: "/v1beta/models/:model_action",
			PathValidator: func(request *http.Request) bool {
				return strings.HasSuffix(request.URL.Path, ":generateContent")
			},
			Handlers: gin.HandlersChain{respond(http.StatusNoContent, "")},
		}},
	}
	registry := mustRegistry(t, module)
	engine := gin.New()
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/v1beta/models/missing-action", nil),
	)

	if recorder.Code != http.StatusNotFound || recorder.Body.String() != "invalid data route" {
		t.Fatalf("response = %d %q, want semantic 404", recorder.Code, recorder.Body.String())
	}
	if allow := recorder.Header().Get("Allow"); allow != "" {
		t.Fatalf("Allow = %q, want empty", allow)
	}
	if _, exists := recorder.Header()["Allow"]; exists {
		t.Fatalf("Allow header remains present: %#v", recorder.Header()["Allow"])
	}
	if authCalls != 0 || methodNotAllowedCalls != 0 {
		t.Fatalf(
			"semantic 404 ran forbidden handlers: auth=%d method-not-allowed=%d",
			authCalls,
			methodNotAllowedCalls,
		)
	}
}

func TestNoMethodAppliesValidatorsAfterSelectingGinPatternPerMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := Module{
		Name: "system", Owner: OwnerSystem, Auth: AuthNone,
		NotFound:         respond(http.StatusNotFound, "route not found"),
		MethodNotAllowed: respond(http.StatusMethodNotAllowed, "method not allowed"),
		Routes: []Route{
			{
				Name: "item-by-id", Methods: []string{http.MethodGet, http.MethodPost},
				Path: "/items/:id/very-long-static-segment",
				PathValidator: func(*http.Request) bool {
					return true
				},
				Handlers: gin.HandlersChain{respond(http.StatusNoContent, "")},
			},
			{
				Name: "special-item", Methods: []string{http.MethodGet},
				Path: "/items/special/:tail",
				PathValidator: func(*http.Request) bool {
					return false
				},
				Handlers: gin.HandlersChain{respond(http.StatusNoContent, "")},
			},
		},
	}
	registry := mustRegistry(t, module)
	engine := gin.New()
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodPut,
			"/items/special/very-long-static-segment",
			nil,
		),
	)

	if recorder.Code != http.StatusMethodNotAllowed ||
		recorder.Body.String() != "method not allowed" {
		t.Fatalf("response = %d %q, want module 405", recorder.Code, recorder.Body.String())
	}
	if allow := recorder.Header().Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", allow)
	}
}

func TestNoMethodMatcherSupportsStaticParamAndTerminalCatchAllPatterns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		pattern string
		target  string
	}{
		{name: "static", pattern: "/health", target: "/health"},
		{name: "param", pattern: "/groups/:id", target: "/groups/42"},
		{name: "catch all", pattern: "/responses/*resource", target: "/responses/resp-1/input_items"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			module := validSystemModule()
			module.Routes[0].Path = testCase.pattern
			registry := mustRegistry(t, module)
			engine := gin.New()
			if err := registry.Bind(engine); err != nil {
				t.Fatalf("Bind() error = %v", err)
			}

			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodPost, testCase.target, nil),
			)
			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", recorder.Code)
			}
			if allow := recorder.Header().Get("Allow"); allow != http.MethodGet {
				t.Fatalf("Allow = %q, want GET", allow)
			}
		})
	}
}

func TestNoRouteUsesLongestBoundaryNamespaceBeforeGlobalFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authCalls := 0
	fallbackCalls := 0
	modules := []Module{
		{
			Name: "control", Owner: OwnerControl, Auth: AuthControl,
			NamespacePrefixes: []string{"/api"},
			Authenticate: func(c *gin.Context) {
				authCalls++
			},
			NotFound: respond(http.StatusNotFound, "control not found"),
		},
		{
			Name: "internal", Owner: OwnerWeb, Auth: AuthNone,
			NamespacePrefixes: []string{"/internal"},
			NotFound:          respond(http.StatusNotFound, "internal not found"),
		},
		{
			Name: "internal-deep", Owner: OwnerWeb, Auth: AuthNone,
			NamespacePrefixes: []string{"/internal/deep"},
			NotFound:          respond(http.StatusNotFound, "deep not found"),
		},
		{
			Name: "web", Owner: OwnerWeb, Auth: AuthNone,
			Fallback: &Fallback{
				Name: "html",
				Match: func(request *http.Request) bool {
					return request.Method == http.MethodGet &&
						strings.Contains(request.Header.Get("Accept"), "text/html")
				},
				Handler: func(c *gin.Context) {
					fallbackCalls++
					c.String(http.StatusNotFound, "spa index")
				},
			},
		},
	}
	registry := mustRegistry(t, modules...)
	engine := gin.New()
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	assertResponse := func(method, target, accept string, wantStatus int, wantBody string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, target, nil)
		if accept != "" {
			request.Header.Set("Accept", accept)
		}
		engine.ServeHTTP(recorder, request)
		if recorder.Code != wantStatus || recorder.Body.String() != wantBody {
			t.Fatalf(
				"%s %s = %d %q, want %d %q",
				method, target, recorder.Code, recorder.Body.String(), wantStatus, wantBody,
			)
		}
	}

	assertResponse(http.MethodGet, "/api/unknown", "text/html", http.StatusNotFound, "control not found")
	if authCalls != 0 || fallbackCalls != 0 {
		t.Fatalf("namespace 404 ran auth/fallback: auth=%d fallback=%d", authCalls, fallbackCalls)
	}
	assertResponse(http.MethodGet, "/internal/deep/path", "text/html", http.StatusNotFound, "deep not found")
	assertResponse(http.MethodGet, "/apiary", "text/html", http.StatusNotFound, "spa index")
	assertResponse(http.MethodGet, "/unknown", "", http.StatusNotFound, "404 page not found")
	if fallbackCalls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallbackCalls)
	}
}

func TestBindRejectsExistingEngineRoutesBeforeMutationAndCanRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := mustRegistry(t, validSystemModule())

	engineWithLegacyRoute := gin.New()
	engineWithLegacyRoute.GET("/legacy/", respond(http.StatusOK, "existing"))
	beforeRoutes := engineWithLegacyRoute.Routes()
	if err := registry.Bind(engineWithLegacyRoute); err == nil {
		t.Fatal("Bind() existing route error = nil")
	}
	if engineWithLegacyRoute.HandleMethodNotAllowed {
		t.Fatal("failed Bind() changed HandleMethodNotAllowed")
	}
	afterRoutes := engineWithLegacyRoute.Routes()
	if len(afterRoutes) != len(beforeRoutes) {
		t.Fatalf(
			"failed Bind() route count = %d, want %d",
			len(afterRoutes),
			len(beforeRoutes),
		)
	}
	for index := range beforeRoutes {
		if afterRoutes[index].Method != beforeRoutes[index].Method ||
			afterRoutes[index].Path != beforeRoutes[index].Path ||
			afterRoutes[index].Handler != beforeRoutes[index].Handler {
			t.Fatalf(
				"failed Bind() mutated route %d: got %#v want %#v",
				index,
				afterRoutes[index],
				beforeRoutes[index],
			)
		}
	}

	cleanEngine := gin.New()
	if err := registry.Bind(cleanEngine); err != nil {
		t.Fatalf("Bind() retry error = %v", err)
	}
}

func TestBindCanOnlySucceedOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := mustRegistry(t, validSystemModule())
	engine := gin.New()
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("first Bind() error = %v", err)
	}
	routeCount := len(engine.Routes())
	if err := registry.Bind(engine); err == nil {
		t.Fatal("second Bind() error = nil")
	}
	if got := len(engine.Routes()); got != routeCount {
		t.Fatalf("second Bind() route count = %d, want %d", got, routeCount)
	}
}

func TestBindRejectsNilEngineWithoutConsumingRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := mustRegistry(t, validSystemModule())
	if err := registry.Bind(nil); err == nil {
		t.Fatal("Bind(nil) error = nil")
	}
	if err := registry.Bind(gin.New()); err != nil {
		t.Fatalf("Bind() after nil error = %v", err)
	}
}

func TestBindRejectsFallbackHandlerOverflowWithoutMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := mustRegistry(t, Module{
		Name:  "system",
		Owner: OwnerSystem,
		Auth:  AuthNone,
	})
	engine := gin.New()
	for range 62 {
		engine.Use(func(*gin.Context) {})
	}

	var (
		bindErr   error
		recovered any
	)
	func() {
		defer func() {
			recovered = recover()
		}()
		bindErr = registry.Bind(engine)
	}()
	if recovered != nil {
		t.Fatalf("Bind() panic = %v, want validation error", recovered)
	}
	if bindErr == nil {
		t.Fatal("Bind() error = nil, want handler overflow error")
	}
	if engine.HandleMethodNotAllowed {
		t.Fatal("Bind() mutated HandleMethodNotAllowed before returning error")
	}
	if err := registry.Bind(gin.New()); err != nil {
		t.Fatalf("Bind() after overflow error = %v", err)
	}
}

func validSystemModule() Module {
	return Module{
		Name: "system", Owner: OwnerSystem, Auth: AuthNone,
		Routes: []Route{{
			Name: "health", Methods: []string{http.MethodGet},
			Path: "/health", Handlers: gin.HandlersChain{respond(http.StatusOK, "ok")},
		}},
	}
}

func validAuthenticatedModule(owner Owner, auth AuthPolicy) Module {
	module := validSystemModule()
	module.Name = string(owner)
	module.Owner = owner
	module.Auth = auth
	module.Authenticate = func(c *gin.Context) {}
	return module
}

func validFallback(name string) *Fallback {
	return &Fallback{
		Name: name,
		Match: func(*http.Request) bool {
			return true
		},
		Handler: respond(http.StatusNotFound, name),
	}
}

func mustRegistry(t *testing.T, modules ...Module) *Registry {
	t.Helper()
	registry, err := NewRegistry(modules...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func respond(status int, body string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if body == "" {
			c.Status(status)
			return
		}
		c.String(status, body)
	}
}
