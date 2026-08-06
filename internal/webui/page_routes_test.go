package webui

import (
	"strings"
	"testing"
)

func TestParsePageRouteManifestValidatesSharedRouteContract(t *testing.T) {
	valid := []byte(`{
		"version": 1,
		"routes": [
			{"name": "home", "path": "/"},
			{"name": "group-detail", "path": "/groups/:id"}
		]
	}`)
	routes, err := parsePageRouteManifest(valid)
	if err != nil {
		t.Fatalf("parsePageRouteManifest(valid) error = %v", err)
	}
	if len(routes) != 2 ||
		routes[0].Name != "home" ||
		routes[0].Path != "/" ||
		routes[1].Name != "group-detail" ||
		routes[1].Path != "/groups/:id" {
		t.Fatalf("routes = %#v, want literal shared route entries", routes)
	}

	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "unsupported version",
			manifest: `{"version":2,"routes":[{"name":"home","path":"/"}]}`,
			want:     "version",
		},
		{
			name:     "empty routes",
			manifest: `{"version":1,"routes":[]}`,
			want:     "routes",
		},
		{
			name: "duplicate name",
			manifest: `{"version":1,"routes":[
				{"name":"home","path":"/"},
				{"name":"home","path":"/other"}
			]}`,
			want: "duplicate",
		},
		{
			name: "duplicate path",
			manifest: `{"version":1,"routes":[
				{"name":"home","path":"/"},
				{"name":"other","path":"/"}
			]}`,
			want: "duplicate",
		},
		{
			name: "duplicate parameter shape",
			manifest: `{"version":1,"routes":[
				{"name":"group-by-id","path":"/groups/:id"},
				{"name":"group-by-slug","path":"/groups/:slug"}
			]}`,
			want: "shape",
		},
		{
			name:     "empty name",
			manifest: `{"version":1,"routes":[{"name":"","path":"/"}]}`,
			want:     "name",
		},
		{
			name:     "relative path",
			manifest: `{"version":1,"routes":[{"name":"home","path":"home"}]}`,
			want:     "path",
		},
		{
			name:     "wildcard path",
			manifest: `{"version":1,"routes":[{"name":"catch-all","path":"/*path"}]}`,
			want:     "path",
		},
		{
			name:     "partial parameter segment",
			manifest: `{"version":1,"routes":[{"name":"group","path":"/groups/prefix-:id"}]}`,
			want:     "path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePageRouteManifest([]byte(test.manifest))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("parsePageRouteManifest() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestEmbeddedPageRouteManifestContainsCurrentPages(t *testing.T) {
	routes, err := loadPageRoutes()
	if err != nil {
		t.Fatalf("loadPageRoutes() error = %v", err)
	}

	got := make(map[string]string, len(routes))
	for _, route := range routes {
		got[route.Name] = route.Path
	}
	want := map[string]string{
		"home":         "/",
		"login":        "/login",
		"import":       "/import",
		"groups":       "/groups",
		"group-detail": "/groups/:id",
		"access-keys":  "/access-keys",
		"monitor":      "/monitor",
		"models":       "/models",
		"settings":     "/settings",
		"model-prices": "/settings/model-prices",
	}
	if len(got) != len(want) {
		t.Fatalf("embedded routes = %#v, want %#v", got, want)
	}
	for name, path := range want {
		if got[name] != path {
			t.Fatalf("embedded route %q = %q, want %q", name, got[name], path)
		}
	}
}
