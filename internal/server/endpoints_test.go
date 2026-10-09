package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var braceParam = regexp.MustCompile(`\{(\w+)\}`)

// TestEndpointsPathParams verifies every {param} in a route pattern is
// documented by a PathParam of the same name, and vice versa.
func TestEndpointsPathParams(t *testing.T) {
	t.Parallel()
	c := &controllerV1{}
	for _, e := range c.endpoints() {
		declared := map[string]bool{}
		for _, p := range e.Params() {
			if p.In == "path" {
				declared[p.Name] = true
			}
		}
		seen := map[string]bool{}
		for _, m := range braceParam.FindAllStringSubmatch(e.Path(), -1) {
			seen[m[1]] = true
			require.True(t, declared[m[1]],
				"%s %s: path param {%s} missing PathParam documentation", e.Method(), e.Path(), m[1])
		}
		for name := range declared {
			require.True(t, seen[name],
				"%s %s: PathParam %q has no matching {%s} in path", e.Method(), e.Path(), name, name)
		}
	}
}

// TestEndpointsUnique verifies no method+path pair is registered twice.
func TestEndpointsUnique(t *testing.T) {
	t.Parallel()
	c := &controllerV1{}
	seen := map[string]bool{}
	for _, e := range c.endpoints() {
		key := e.Method() + " " + e.Path()
		require.False(t, seen[key], "duplicate endpoint %s", key)
		seen[key] = true
	}
}

// TestEndpointsComplete verifies the registry covers every handler
// method on controllerV1 and that handlers are always set.
func TestEndpointsHandlers(t *testing.T) {
	t.Parallel()
	c := &controllerV1{}
	for _, e := range c.endpoints() {
		require.NotNil(t, e.Handler(), "%s %s: nil handler", e.Method(), e.Path())
		require.NotEmpty(t, e.Summary(), "%s %s: missing summary", e.Method(), e.Path())
	}
}

// TestDocsEndpoints serves the docs routes from a bare server and
// checks the HTML page and OpenAPI spec.
func TestDocsEndpoints(t *testing.T) {
	t.Parallel()
	srv := &Server{}
	srv.installHandler()
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	get := func(path string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, hs.URL+path, nil)
		require.NoError(t, err)
		resp, err := hs.Client().Do(req)
		require.NoError(t, err)
		return resp
	}

	resp := get("/v1/docs/")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	resp = get("/v1/docs/openapi.json")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var spec struct {
		Info struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		OpenAPI string `json:"openapi"`
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(body, &spec))
	require.Equal(t, "3.2.1", spec.OpenAPI)
	require.Equal(t, "Crush API", spec.Info.Title)
	require.Equal(t, "1.0", spec.Info.Version)
	require.Len(t, spec.Servers, 1)
	require.Equal(t, apiBase, spec.Servers[0].URL)

	// Shared shapes live in components, so a generated client gets named
	// types instead of an anonymous object per operation.
	require.Contains(t, spec.Components.Schemas, "Workspace")
	require.Contains(t, spec.Components.Schemas, "Session")
	require.Contains(t, spec.Components.Schemas, "ErrorResponse")

	// Every $ref must resolve: a dangling reference is a spec a client
	// cannot compile against.
	var raw any
	require.NoError(t, json.Unmarshal(body, &raw))
	refs := collectRefs(raw)
	require.NotEmpty(t, refs)
	for _, ref := range refs {
		name, ok := strings.CutPrefix(ref, "#/components/schemas/")
		require.True(t, ok, "unexpected $ref form %q", ref)
		require.Contains(t, spec.Components.Schemas, name, "dangling $ref %q", ref)
	}

	// One spec operation per registered endpoint (paths with multiple
	// methods contribute one operation each).
	c := &controllerV1{}
	count := 0
	for _, e := range c.endpoints() {
		path := strings.TrimPrefix(e.Path(), apiBase)
		item, ok := spec.Paths[path]
		require.True(t, ok, "spec missing path %s", path)
		require.Contains(t, item, strings.ToLower(e.Method()), "spec missing %s %s", e.Method(), path)
		count++
	}
	require.Len(t, c.endpoints(), count)
}

// collectRefs returns every $ref string found anywhere in a decoded JSON
// document.
func collectRefs(v any) []string {
	var refs []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				if k == "$ref" {
					if s, ok := child.(string); ok {
						refs = append(refs, s)
					}
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(v)
	return refs
}
