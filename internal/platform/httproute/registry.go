// Package httproute provides declarative HTTP route registration and fallback dispatch.
package httproute

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Owner identifies the runtime surface that owns a route.
type Owner string

const (
	OwnerSystem  Owner = "system"
	OwnerControl Owner = "control"
	OwnerData    Owner = "data"
	OwnerWeb     Owner = "web"
)

// AuthPolicy identifies the authentication contract of a route module.
type AuthPolicy string

const (
	AuthNone      AuthPolicy = "none"
	AuthControl   AuthPolicy = "control"
	AuthAccessKey AuthPolicy = "access-key"

	ginMaximumHandlerChainLength = 63
)

// Route describes one semantic route, potentially available through multiple methods.
type Route struct {
	Name          string
	Methods       []string
	Path          string
	PathValidator func(*http.Request) bool
	Prepare       gin.HandlersChain
	Handlers      gin.HandlersChain
}

// Fallback describes the one optional application-wide fallback.
type Fallback struct {
	Name    string
	Match   func(*http.Request) bool
	Handler gin.HandlerFunc
}

// Module groups routes that share ownership, authentication, and fallback behavior.
type Module struct {
	Name              string
	Owner             Owner
	Auth              AuthPolicy
	Prefix            string
	NamespacePrefixes []string
	BeforeAuth        gin.HandlersChain
	Authenticate      gin.HandlerFunc
	Routes            []Route
	NotFound          gin.HandlerFunc
	MethodNotAllowed  gin.HandlerFunc
	Fallback          *Fallback
}

// RouteInfo is an immutable view of a registered route declaration.
type RouteInfo struct {
	ModuleName string
	RouteName  string
	Owner      Owner
	Auth       AuthPolicy
	Methods    []string
	Path       string
}

type compiledRoute struct {
	moduleIndex int
	routeIndex  int
	path        string
}

type namespaceBinding struct {
	moduleIndex int
	prefix      string
}

type runtimeRoute struct {
	moduleIndex int
	path        string
	methods     []string
	validator   func(*http.Request) bool
}

type routePattern struct {
	method string
	path   string
}

// Registry validates and binds a complete set of HTTP route modules.
type Registry struct {
	mu sync.Mutex

	modules    []Module
	routes     []compiledRoute
	routeInfos []RouteInfo
	namespaces []namespaceBinding
	fallback   *Fallback
	runtime    []runtimeRoute
	bound      bool
}

// NewRegistry validates modules without modifying a Gin engine.
func NewRegistry(modules ...Module) (*Registry, error) {
	registry := &Registry{
		modules: make([]Module, len(modules)),
	}
	moduleNames := make(map[string]struct{}, len(modules))
	routeNames := make(map[string]struct{})
	methodPaths := make(map[string]string)
	namespaceOwners := make(map[string]string)
	patterns := make([]routePattern, 0)

	for moduleIndex := range modules {
		module := cloneModule(modules[moduleIndex])
		if err := validateName("module", module.Name); err != nil {
			return nil, err
		}
		if _, exists := moduleNames[module.Name]; exists {
			return nil, fmt.Errorf("duplicate module name %q", module.Name)
		}
		moduleNames[module.Name] = struct{}{}
		if err := validateOwnerAuth(module); err != nil {
			return nil, fmt.Errorf("module %q: %w", module.Name, err)
		}
		if err := validatePrefix(module.Prefix); err != nil {
			return nil, fmt.Errorf("module %q prefix: %w", module.Name, err)
		}
		if err := validateHandlers("before-auth", module.BeforeAuth, true); err != nil {
			return nil, fmt.Errorf("module %q: %w", module.Name, err)
		}

		for _, prefix := range module.NamespacePrefixes {
			if err := validateStaticPrefix(prefix, true); err != nil {
				return nil, fmt.Errorf(
					"module %q namespace prefix %q: %w",
					module.Name,
					prefix,
					err,
				)
			}
			if owner, exists := namespaceOwners[prefix]; exists {
				return nil, fmt.Errorf(
					"duplicate namespace prefix %q in modules %q and %q",
					prefix,
					owner,
					module.Name,
				)
			}
			namespaceOwners[prefix] = module.Name
			registry.namespaces = append(registry.namespaces, namespaceBinding{
				moduleIndex: moduleIndex,
				prefix:      prefix,
			})
		}

		if module.Fallback != nil {
			if err := validateFallback(*module.Fallback); err != nil {
				return nil, fmt.Errorf("module %q: %w", module.Name, err)
			}
			if registry.fallback != nil {
				return nil, fmt.Errorf("multiple global fallbacks are not allowed")
			}
			fallbackCopy := *module.Fallback
			registry.fallback = &fallbackCopy
		}

		for routeIndex := range module.Routes {
			route := module.Routes[routeIndex]
			if err := validateName("route", route.Name); err != nil {
				return nil, fmt.Errorf("module %q: %w", module.Name, err)
			}
			if _, exists := routeNames[route.Name]; exists {
				return nil, fmt.Errorf("duplicate route name %q", route.Name)
			}
			routeNames[route.Name] = struct{}{}
			if err := validateRoute(route); err != nil {
				return nil, fmt.Errorf(
					"module %q route %q: %w",
					module.Name,
					route.Name,
					err,
				)
			}

			fullPath := module.Prefix + route.Path
			if err := validateRoutePattern(fullPath); err != nil {
				return nil, fmt.Errorf(
					"module %q route %q full path %q: %w",
					module.Name,
					route.Name,
					fullPath,
					err,
				)
			}
			for _, method := range route.Methods {
				key := method + "\x00" + fullPath
				if existing, exists := methodPaths[key]; exists {
					return nil, fmt.Errorf(
						"duplicate route pattern %s %s in routes %q and %q",
						method,
						fullPath,
						existing,
						route.Name,
					)
				}
				methodPaths[key] = route.Name
				patterns = append(patterns, routePattern{
					method: method,
					path:   fullPath,
				})
			}
			registry.routes = append(registry.routes, compiledRoute{
				moduleIndex: moduleIndex,
				routeIndex:  routeIndex,
				path:        fullPath,
			})
			registry.routeInfos = append(registry.routeInfos, RouteInfo{
				ModuleName: module.Name,
				RouteName:  route.Name,
				Owner:      module.Owner,
				Auth:       module.Auth,
				Methods:    append([]string(nil), route.Methods...),
				Path:       fullPath,
			})
		}
		registry.modules[moduleIndex] = module
	}

	sort.SliceStable(registry.namespaces, func(left, right int) bool {
		return len(registry.namespaces[left].prefix) > len(registry.namespaces[right].prefix)
	})
	if err := validateNamespaceOwnership(
		registry.modules,
		registry.routes,
		registry.namespaces,
	); err != nil {
		return nil, err
	}
	if err := validateGinPatterns(patterns); err != nil {
		return nil, err
	}
	return registry, nil
}

// Routes returns defensive route metadata in declaration order.
func (registry *Registry) Routes() []RouteInfo {
	if registry == nil {
		return nil
	}
	result := make([]RouteInfo, len(registry.routeInfos))
	for index, route := range registry.routeInfos {
		result[index] = route
		result[index].Methods = append([]string(nil), route.Methods...)
	}
	return result
}

// Bind atomically preflights and then binds this registry to one Gin engine.
func (registry *Registry) Bind(engine *gin.Engine) error {
	if registry == nil {
		return fmt.Errorf("HTTP route registry is nil")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if engine == nil {
		return fmt.Errorf("Gin engine is nil")
	}
	if registry.bound {
		return fmt.Errorf("HTTP route registry is already bound")
	}
	if len(engine.Handlers)+1 >= ginMaximumHandlerChainLength {
		return fmt.Errorf(
			"global HTTP fallback has too many handlers after engine middleware: %d",
			len(engine.Handlers)+1,
		)
	}

	existingRoutes := engine.Routes()
	if len(existingRoutes) != 0 {
		return fmt.Errorf(
			"HTTP route registry requires an empty Gin engine, found %d existing routes",
			len(existingRoutes),
		)
	}
	patterns := make([]routePattern, 0, len(registry.routeInfos))
	runtimeRoutes := make([]runtimeRoute, 0, len(registry.routes))
	for _, route := range registry.routes {
		module := registry.modules[route.moduleIndex]
		declaration := module.Routes[route.routeIndex]
		for _, method := range declaration.Methods {
			patterns = append(patterns, routePattern{method: method, path: route.path})
		}
		runtimeRoutes = append(runtimeRoutes, runtimeRoute{
			moduleIndex: route.moduleIndex,
			path:        route.path,
			methods:     append([]string(nil), declaration.Methods...),
			validator:   declaration.PathValidator,
		})
	}
	if err := validateGinPatterns(patterns); err != nil {
		return fmt.Errorf("preflight Gin engine routes: %w", err)
	}

	ownershipGuards := make([]gin.HandlerFunc, len(registry.routes))
	for routeIndex, route := range registry.routes {
		module := registry.modules[route.moduleIndex]
		declaration := module.Routes[route.routeIndex]
		ownershipGuards[routeIndex] = registry.staticPathOwnershipGuard(
			route.path,
			runtimeRoutes,
		)
		chainLength := len(engine.Handlers) + routeChainLength(
			module,
			declaration,
			ownershipGuards[routeIndex] != nil,
		)
		if chainLength >= ginMaximumHandlerChainLength {
			return fmt.Errorf(
				"module %q route %q has too many handlers after engine middleware: %d",
				module.Name,
				declaration.Name,
				chainLength,
			)
		}
	}

	for routeIndex, route := range registry.routes {
		module := registry.modules[route.moduleIndex]
		declaration := module.Routes[route.routeIndex]
		handlers := registry.routeHandlers(
			route.moduleIndex,
			declaration,
			ownershipGuards[routeIndex],
		)
		for _, method := range declaration.Methods {
			engine.Handle(method, route.path, handlers...)
		}
	}
	registry.runtime = runtimeRoutes
	engine.HandleMethodNotAllowed = true
	engine.NoRoute(registry.handleNoRoute)
	engine.NoMethod(registry.handleNoMethod)
	registry.bound = true
	return nil
}

func (registry *Registry) routeHandlers(
	moduleIndex int,
	route Route,
	ownershipGuard gin.HandlerFunc,
) gin.HandlersChain {
	module := registry.modules[moduleIndex]
	length := routeChainLength(module, route, ownershipGuard != nil)
	handlers := make(gin.HandlersChain, 0, length)
	if ownershipGuard != nil {
		handlers = append(handlers, ownershipGuard)
	}
	if route.PathValidator != nil {
		validator := route.PathValidator
		notFound := module.NotFound
		handlers = append(handlers, func(c *gin.Context) {
			if validator(c.Request) {
				return
			}
			invokeTerminal(c, http.StatusNotFound, notFound)
		})
	}
	handlers = append(handlers, module.BeforeAuth...)
	handlers = append(handlers, route.Prepare...)
	if module.Authenticate != nil {
		handlers = append(handlers, module.Authenticate)
	}
	handlers = append(handlers, route.Handlers...)
	return handlers
}

func routeChainLength(
	module Module,
	route Route,
	hasOwnershipGuard bool,
) int {
	length := len(module.BeforeAuth) + len(route.Prepare) + len(route.Handlers)
	if hasOwnershipGuard {
		length++
	}
	if route.PathValidator != nil {
		length++
	}
	if module.Authenticate != nil {
		length++
	}
	return length
}

func (registry *Registry) staticPathOwnershipGuard(
	matchedPattern string,
	routes []runtimeRoute,
) gin.HandlerFunc {
	owners := make(map[string][]int)
	for routeIndex, route := range routes {
		if route.path == matchedPattern ||
			!isStaticRoutePattern(route.path) ||
			!matchPattern(matchedPattern, route.path) {
			continue
		}
		owners[route.path] = append(owners[route.path], routeIndex)
	}
	if len(owners) == 0 {
		return nil
	}

	return func(c *gin.Context) {
		ownerRoutes, exists := owners[c.Request.URL.Path]
		if !exists {
			return
		}
		allowed := make(map[string]struct{})
		ownerModuleIndex := -1
		for _, routeIndex := range ownerRoutes {
			route := routes[routeIndex]
			if route.validator != nil && !route.validator(c.Request) {
				continue
			}
			if ownerModuleIndex < 0 {
				ownerModuleIndex = route.moduleIndex
			}
			for _, method := range route.methods {
				allowed[method] = struct{}{}
			}
		}
		if len(allowed) == 0 {
			return
		}
		if _, exists := allowed[c.Request.Method]; exists {
			return
		}

		methods := make([]string, 0, len(allowed))
		for method := range allowed {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		c.Header("Allow", strings.Join(methods, ", "))
		var handler gin.HandlerFunc
		if ownerModuleIndex >= 0 {
			handler = registry.modules[ownerModuleIndex].MethodNotAllowed
		}
		invokeTerminal(c, http.StatusMethodNotAllowed, handler)
	}
}

func isStaticRoutePattern(pattern string) bool {
	for _, segment := range strings.Split(strings.TrimPrefix(pattern, "/"), "/") {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			return false
		}
	}
	return true
}

func (registry *Registry) handleNoRoute(c *gin.Context) {
	for _, namespace := range registry.namespaces {
		if !namespaceMatches(namespace.prefix, c.Request.URL.Path) {
			continue
		}
		invokeTerminal(
			c,
			http.StatusNotFound,
			registry.modules[namespace.moduleIndex].NotFound,
		)
		return
	}
	if registry.fallback != nil && registry.fallback.Match(c.Request) {
		invokeTerminal(c, http.StatusNotFound, registry.fallback.Handler)
		return
	}
	invokeTerminal(c, http.StatusNotFound, nil)
}

func (registry *Registry) handleNoMethod(c *gin.Context) {
	ginAllowed := make(map[string]struct{})
	for _, method := range strings.Split(c.Writer.Header().Get("Allow"), ",") {
		method = strings.TrimSpace(method)
		if method != "" {
			ginAllowed[method] = struct{}{}
		}
	}

	validByMethod := make(map[string]int)
	bestByMethod := make(map[string]int, len(ginAllowed))
	bestSemantic := -1
	bestRaw := -1
	for routeIndex, route := range registry.runtime {
		if !matchPattern(route.path, c.Request.URL.Path) {
			continue
		}
		if moreSpecific(
			route.path,
			registry.runtime,
			bestRaw,
		) {
			bestRaw = routeIndex
		}
		for _, method := range route.methods {
			if _, exists := ginAllowed[method]; !exists {
				continue
			}
			currentIndex, exists := bestByMethod[method]
			if !exists || moreSpecific(route.path, registry.runtime, currentIndex) {
				bestByMethod[method] = routeIndex
			}
		}
	}
	for method, routeIndex := range bestByMethod {
		route := registry.runtime[routeIndex]
		if route.validator != nil && !route.validator(c.Request) {
			continue
		}
		validByMethod[method] = routeIndex
		if moreSpecific(route.path, registry.runtime, bestSemantic) {
			bestSemantic = routeIndex
		}
	}
	allowed := make(map[string]struct{}, len(validByMethod))
	for method, routeIndex := range validByMethod {
		if bestSemantic >= 0 && moreSpecific(
			registry.runtime[bestSemantic].path,
			registry.runtime,
			routeIndex,
		) {
			continue
		}
		allowed[method] = struct{}{}
	}

	if len(allowed) == 0 {
		c.Header("Allow", "")
		if bestRaw >= 0 {
			moduleIndex := registry.runtime[bestRaw].moduleIndex
			invokeTerminal(
				c,
				http.StatusNotFound,
				registry.modules[moduleIndex].NotFound,
			)
			return
		}
		registry.handleNoRoute(c)
		return
	}

	methods := make([]string, 0, len(allowed))
	for method := range allowed {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	c.Header("Allow", strings.Join(methods, ", "))

	var handler gin.HandlerFunc
	if bestSemantic >= 0 {
		moduleIndex := registry.runtime[bestSemantic].moduleIndex
		if moduleIndex >= 0 {
			handler = registry.modules[moduleIndex].MethodNotAllowed
		}
	}
	invokeTerminal(c, http.StatusMethodNotAllowed, handler)
}

func invokeTerminal(c *gin.Context, status int, handler gin.HandlerFunc) {
	c.Abort()
	c.Status(status)
	if handler != nil {
		handler(c)
	}
}

func cloneModule(module Module) Module {
	result := module
	result.NamespacePrefixes = append([]string(nil), module.NamespacePrefixes...)
	result.BeforeAuth = append(gin.HandlersChain(nil), module.BeforeAuth...)
	result.Routes = make([]Route, len(module.Routes))
	for index, route := range module.Routes {
		result.Routes[index] = route
		result.Routes[index].Methods = append([]string(nil), route.Methods...)
		result.Routes[index].Prepare = append(gin.HandlersChain(nil), route.Prepare...)
		result.Routes[index].Handlers = append(gin.HandlersChain(nil), route.Handlers...)
	}
	if module.Fallback != nil {
		fallback := *module.Fallback
		result.Fallback = &fallback
	}
	return result
}

func validateNamespaceOwnership(
	modules []Module,
	routes []compiledRoute,
	namespaces []namespaceBinding,
) error {
	for _, route := range routes {
		for _, namespace := range namespaces {
			if namespace.moduleIndex == route.moduleIndex ||
				!patternIntersectsNamespace(route.path, namespace.prefix) {
				continue
			}
			if routeIsInsideLongerOwnedNamespace(
				route,
				namespace,
				namespaces,
			) {
				continue
			}
			return fmt.Errorf(
				"module %q route %q pattern %q intersects namespace %q owned by module %q",
				modules[route.moduleIndex].Name,
				modules[route.moduleIndex].Routes[route.routeIndex].Name,
				route.path,
				namespace.prefix,
				modules[namespace.moduleIndex].Name,
			)
		}
	}
	return nil
}

func routeIsInsideLongerOwnedNamespace(
	route compiledRoute,
	foreign namespaceBinding,
	namespaces []namespaceBinding,
) bool {
	for _, candidate := range namespaces {
		if candidate.moduleIndex != route.moduleIndex ||
			len(candidate.prefix) <= len(foreign.prefix) ||
			!namespaceMatches(foreign.prefix, candidate.prefix) {
			continue
		}
		if namespaceContainsPattern(candidate.prefix, route.path) {
			return true
		}
	}
	return false
}

func patternIntersectsNamespace(pattern, prefix string) bool {
	if prefix == "/" {
		return true
	}
	patternSegments := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	prefixSegments := strings.Split(strings.TrimPrefix(prefix, "/"), "/")
	for index, segment := range patternSegments {
		if strings.HasPrefix(segment, "*") {
			return true
		}
		if index >= len(prefixSegments) {
			return true
		}
		if strings.HasPrefix(segment, ":") {
			continue
		}
		if segment != prefixSegments[index] {
			return false
		}
	}
	return len(patternSegments) == len(prefixSegments)
}

func namespaceContainsPattern(prefix, pattern string) bool {
	if prefix == "/" {
		return true
	}
	prefixSegments := strings.Split(strings.TrimPrefix(prefix, "/"), "/")
	patternSegments := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	if len(patternSegments) < len(prefixSegments) {
		return false
	}
	for index, segment := range prefixSegments {
		if patternSegments[index] != segment {
			return false
		}
	}
	return true
}

func validateOwnerAuth(module Module) error {
	var required AuthPolicy
	switch module.Owner {
	case OwnerSystem, OwnerWeb:
		required = AuthNone
	case OwnerControl:
		required = AuthControl
	case OwnerData:
		required = AuthAccessKey
	default:
		return fmt.Errorf("invalid owner %q", module.Owner)
	}
	if module.Auth != required {
		return fmt.Errorf(
			"owner %q requires auth policy %q, got %q",
			module.Owner,
			required,
			module.Auth,
		)
	}
	if module.Auth == AuthNone && module.Authenticate != nil {
		return fmt.Errorf("auth policy %q forbids an authenticator", module.Auth)
	}
	if module.Auth != AuthNone && module.Authenticate == nil {
		return fmt.Errorf("auth policy %q requires an authenticator", module.Auth)
	}
	return nil
}

func validateRoute(route Route) error {
	if len(route.Methods) == 0 {
		return fmt.Errorf("at least one method is required")
	}
	methods := make(map[string]struct{}, len(route.Methods))
	for _, method := range route.Methods {
		if !validMethod(method) {
			return fmt.Errorf("invalid HTTP method %q", method)
		}
		if method != strings.ToUpper(method) {
			return fmt.Errorf("HTTP method %q must be uppercase", method)
		}
		if _, exists := methods[method]; exists {
			return fmt.Errorf("duplicate HTTP method %q", method)
		}
		methods[method] = struct{}{}
	}
	if err := validateRoutePattern(route.Path); err != nil {
		return err
	}
	if err := validateHandlers("prepare", route.Prepare, true); err != nil {
		return err
	}
	if err := validateHandlers("route", route.Handlers, false); err != nil {
		return err
	}
	return nil
}

func validateFallback(fallback Fallback) error {
	if err := validateName("fallback", fallback.Name); err != nil {
		return err
	}
	if fallback.Match == nil {
		return fmt.Errorf("fallback %q requires a matcher", fallback.Name)
	}
	if fallback.Handler == nil {
		return fmt.Errorf("fallback %q requires a handler", fallback.Name)
	}
	return nil
}

func validateName(kind, name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("%s name must be non-empty without surrounding whitespace", kind)
	}
	return nil
}

func validateHandlers(kind string, handlers gin.HandlersChain, optional bool) error {
	if !optional && len(handlers) == 0 {
		return fmt.Errorf("%s handler chain is empty", kind)
	}
	for index, handler := range handlers {
		if handler == nil {
			return fmt.Errorf("%s handler %d is nil", kind, index)
		}
	}
	return nil
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if prefix == "/" {
		return fmt.Errorf("root prefix must be represented by an empty string")
	}
	return validateStaticPrefix(prefix, false)
}

func validateStaticPrefix(prefix string, allowRoot bool) error {
	if prefix == "" || !strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("must be an absolute path")
	}
	if prefix == "/" {
		if allowRoot {
			return nil
		}
		return fmt.Errorf("root prefix is not allowed")
	}
	if strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("must not have a trailing slash")
	}
	if strings.ContainsAny(prefix, "?#\\:*") {
		return fmt.Errorf("must be a static path without query, fragment, or wildcard")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(prefix, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("contains a non-canonical path segment")
		}
	}
	return nil
}

func validateRoutePattern(pattern string) error {
	if pattern == "" || !strings.HasPrefix(pattern, "/") {
		return fmt.Errorf("path must be absolute")
	}
	if pattern != "/" && strings.HasSuffix(pattern, "/") {
		return fmt.Errorf("path must not have a trailing slash")
	}
	if strings.ContainsAny(pattern, "?#\\") {
		return fmt.Errorf("path contains query, fragment, or backslash")
	}
	if pattern == "/" {
		return nil
	}

	wildcardNames := make(map[string]struct{})
	segments := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("path contains a non-canonical segment")
		}
		if segment[0] != ':' && segment[0] != '*' {
			if strings.ContainsAny(segment, ":*") {
				return fmt.Errorf("wildcard must occupy its complete path segment")
			}
			continue
		}
		name := segment[1:]
		if !validWildcardName(name) {
			return fmt.Errorf("invalid wildcard name %q", name)
		}
		if _, exists := wildcardNames[name]; exists {
			return fmt.Errorf("duplicate wildcard name %q", name)
		}
		wildcardNames[name] = struct{}{}
		if segment[0] == '*' && index != len(segments)-1 {
			return fmt.Errorf("catch-all wildcard must be the final segment")
		}
	}
	return nil
}

func validWildcardName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' ||
			character == '-' {
			continue
		}
		return false
	}
	return true
}

func validMethod(method string) bool {
	if method == "" {
		return false
	}
	for index := 0; index < len(method); index++ {
		character := method[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validateGinPatterns(patterns []routePattern) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Gin route pattern conflict: %v", recovered)
		}
	}()
	engine := gin.New()
	handler := func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	}
	for _, pattern := range patterns {
		engine.Handle(pattern.method, pattern.path, handler)
	}
	return nil
}

func matchPattern(pattern, requestPath string) bool {
	if pattern == "/" {
		return requestPath == "/"
	}
	if requestPath == "/" || !strings.HasPrefix(requestPath, "/") {
		return false
	}
	patternSegments := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pathSegments := strings.Split(strings.TrimPrefix(requestPath, "/"), "/")
	pathIndex := 0
	for patternIndex, segment := range patternSegments {
		if segment[0] == '*' {
			return patternIndex == len(patternSegments)-1 && pathIndex < len(pathSegments)
		}
		if pathIndex >= len(pathSegments) {
			return false
		}
		if segment[0] == ':' {
			if pathSegments[pathIndex] == "" {
				return false
			}
		} else if segment != pathSegments[pathIndex] {
			return false
		}
		pathIndex++
	}
	return pathIndex == len(pathSegments)
}

func namespaceMatches(prefix, requestPath string) bool {
	if prefix == "/" {
		return strings.HasPrefix(requestPath, "/")
	}
	return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}

func moreSpecific(
	path string,
	routes []runtimeRoute,
	currentIndex int,
) bool {
	if currentIndex < 0 {
		return true
	}
	currentSegments := strings.Split(strings.TrimPrefix(routes[currentIndex].path, "/"), "/")
	candidateSegments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	sharedLength := min(len(candidateSegments), len(currentSegments))
	for index := range sharedLength {
		candidateRank := routeSegmentSpecificity(candidateSegments[index])
		currentRank := routeSegmentSpecificity(currentSegments[index])
		if candidateRank != currentRank {
			return candidateRank > currentRank
		}
	}
	return len(candidateSegments) > len(currentSegments)
}

func routeSegmentSpecificity(segment string) int {
	switch {
	case strings.HasPrefix(segment, "*"):
		return 0
	case strings.HasPrefix(segment, ":"):
		return 1
	default:
		return 2
	}
}
