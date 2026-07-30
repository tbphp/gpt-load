package webui

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const pageRouteManifestVersion = 1

var (
	//go:embed page_routes.json
	embeddedPageRouteManifest []byte

	staticPageSegmentPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)
	parameterPageSegmentPattern = regexp.MustCompile(`^:[A-Za-z][A-Za-z0-9_]*$`)
)

type pageRoute struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type pageRouteManifest struct {
	Version int         `json:"version"`
	Routes  []pageRoute `json:"routes"`
}

func loadPageRoutes() ([]pageRoute, error) {
	return parsePageRouteManifest(embeddedPageRouteManifest)
}

func parsePageRouteManifest(data []byte) ([]pageRoute, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var manifest pageRouteManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode page route manifest: %w", err)
	}
	if err := ensurePageRouteManifestEOF(decoder); err != nil {
		return nil, err
	}
	if manifest.Version != pageRouteManifestVersion {
		return nil, fmt.Errorf(
			"page route manifest version = %d, want %d",
			manifest.Version,
			pageRouteManifestVersion,
		)
	}
	if len(manifest.Routes) == 0 {
		return nil, fmt.Errorf("page route manifest routes must not be empty")
	}

	names := make(map[string]struct{}, len(manifest.Routes))
	paths := make(map[string]struct{}, len(manifest.Routes))
	shapes := make(map[string]struct{}, len(manifest.Routes))
	routes := make([]pageRoute, len(manifest.Routes))
	for index, route := range manifest.Routes {
		if route.Name == "" || strings.TrimSpace(route.Name) != route.Name {
			return nil, fmt.Errorf("page route at index %d has invalid name", index)
		}
		if !validPageRoutePath(route.Path) {
			return nil, fmt.Errorf(
				"page route %q has unsupported shared path %q",
				route.Name,
				route.Path,
			)
		}
		if _, exists := names[route.Name]; exists {
			return nil, fmt.Errorf("duplicate page route name %q", route.Name)
		}
		if _, exists := paths[route.Path]; exists {
			return nil, fmt.Errorf("duplicate page route path %q", route.Path)
		}
		shape := pageRouteShape(route.Path)
		if _, exists := shapes[shape]; exists {
			return nil, fmt.Errorf("duplicate page route shape %q", shape)
		}
		names[route.Name] = struct{}{}
		paths[route.Path] = struct{}{}
		shapes[shape] = struct{}{}
		routes[index] = route
	}
	return routes, nil
}

func ensurePageRouteManifestEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing page route manifest data: %w", err)
	}
	return fmt.Errorf("page route manifest contains trailing JSON values")
}

func pageRouteShape(value string) string {
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		if parameterPageSegmentPattern.MatchString(segment) {
			segments[index] = ":"
		}
	}
	return strings.Join(segments, "/")
}

func validPageRoutePath(value string) bool {
	if value == "/" {
		return true
	}
	if !strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if parameterPageSegmentPattern.MatchString(segment) {
			continue
		}
		if !staticPageSegmentPattern.MatchString(segment) || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
