package apigen

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type nested struct {
	Value string `json:"value"`
}

type sample struct {
	ID         string            `json:"id"`
	Count      int               `json:"count"`
	Ratio      float64           `json:"ratio,omitempty"`
	Active     bool              `json:"active,omitempty"`
	Labels     []string          `json:"labels"`
	Meta       map[string]string `json:"meta,omitempty"`
	Nested     nested            `json:"nested"`
	Ptr        *nested           `json:"ptr,omitempty"`
	When       time.Time         `json:"when"`
	Err        error             `json:"error,omitempty"`
	Anything   any               `json:"anything,omitempty"`
	Skipped    string            `json:"-"`
	unexported string
}

func TestSchemaOfStruct(t *testing.T) {
	t.Parallel()
	s := SchemaOf(sample{})

	require.Equal(t, "object", s.Type)
	require.ElementsMatch(t, []string{"id", "count", "labels", "nested", "when"}, s.Required)

	require.Equal(t, "string", s.Properties["id"].Type)
	require.Equal(t, "integer", s.Properties["count"].Type)
	require.Equal(t, "number", s.Properties["ratio"].Type)
	require.Equal(t, "boolean", s.Properties["active"].Type)
	require.Equal(t, "array", s.Properties["labels"].Type)
	require.Equal(t, "string", s.Properties["labels"].Items.Type)
	require.Equal(t, "object", s.Properties["meta"].Type)
	require.Equal(t, "string", s.Properties["meta"].AdditionalProperties.Type)
	require.Equal(t, "object", s.Properties["nested"].Type)
	require.Equal(t, "string", s.Properties["nested"].Properties["value"].Type)
	require.Equal(t, "object", s.Properties["ptr"].Type)
	require.Equal(t, "string", s.Properties["when"].Type)
	require.Equal(t, "date-time", s.Properties["when"].Format)
	require.Equal(t, "string", s.Properties["error"].Type)
	require.Equal(t, &Schema{}, s.Properties["anything"])

	require.NotContains(t, s.Properties, "Skipped")
	require.NotContains(t, s.Properties, "unexported")
}

type cyc struct {
	Name  string `json:"name"`
	Child *cyc   `json:"child,omitempty"`
}

func TestSchemaOfCycle(t *testing.T) {
	t.Parallel()
	s := SchemaOf(cyc{})
	require.Equal(t, "object", s.Type)
	require.Equal(t, "apigen.cyc", s.Properties["child"].Description)
}

type withTime struct {
	At time.Time `json:"at"`
}

func TestSchemaOfSamePackageExpansion(t *testing.T) {
	t.Parallel()
	// Types from this package expand even when reached through another.
	s := SchemaOf(withTime{})
	require.Equal(t, "string", s.Properties["at"].Type)
}

type embeds struct {
	nested
	Extra string `json:"extra"`
}

func TestSchemaOfEmbedded(t *testing.T) {
	t.Parallel()
	s := SchemaOf(embeds{})
	require.Contains(t, s.Properties, "value")
	require.Contains(t, s.Properties, "extra")
}

func TestBuilderCompileContract(t *testing.T) {
	t.Parallel()
	called := false
	h := func(http.ResponseWriter, *http.Request) { called = true }

	e := Get("/v1/things/{id}").
		Summary("Get thing").
		Description("Longer prose.").
		Tags("things").
		PathParam("id", "Thing ID").
		QueryParam("verbose", "Verbose output").
		RequiredQueryParam("mode", "Mode").
		Responds(sample{}).
		Fails(404, 500).
		Handle(h)

	require.Equal(t, http.MethodGet, e.Method())
	require.Equal(t, "/v1/things/{id}", e.Path())
	require.Equal(t, "Get thing", e.Summary())
	require.Equal(t, "Longer prose.", e.Description())
	require.Equal(t, []string{"things"}, e.Tags())
	require.Equal(t, []Param{
		{Name: "id", In: "path", Type: "string", Description: "Thing ID", Required: true},
		{Name: "verbose", In: "query", Type: "string", Description: "Verbose output"},
		{Name: "mode", In: "query", Type: "string", Description: "Mode", Required: true},
	}, e.Params())
	require.Equal(t, sample{}, e.Response())
	require.Nil(t, e.Request())
	require.Equal(t, []int{404, 500}, e.Errors())
	require.False(t, e.SSE())

	e.Handler()(nil, nil)
	require.True(t, called)
}

func TestBuilderVerbsAndSSE(t *testing.T) {
	t.Parallel()
	for method, build := range map[string]func(string) endpointBuilder{
		http.MethodGet:    Get,
		http.MethodPost:   Post,
		http.MethodPut:    Put,
		http.MethodDelete: Delete,
	} {
		e := build("/x").Handle(func(http.ResponseWriter, *http.Request) {})
		require.Equal(t, method, e.Method())
	}

	e := Get("/events").SSE().Handle(func(http.ResponseWriter, *http.Request) {})
	require.True(t, e.SSE())
}

func TestSpecJSON(t *testing.T) {
	t.Parallel()
	info := Info{Title: "Test API", Version: "1.0", Description: "Desc."}
	endpoints := []Endpoint{
		Post("/v1/things").
			Summary("Create thing").
			Accepts(sample{}).
			Responds(sample{}).
			Fails(400).
			Handle(func(http.ResponseWriter, *http.Request) {}),
		Get("/v1/things/{id}/events").
			Summary("Stream").
			PathParam("id", "Thing ID").
			SSE().
			Handle(func(http.ResponseWriter, *http.Request) {}),
	}

	data, err := SpecJSON(info, "/v1", endpoints)
	require.NoError(t, err)
	text := string(data)

	require.Contains(t, text, `"openapi": "3.2.1"`)
	require.Contains(t, text, `"title": "Test API"`)
	require.Contains(t, text, `"url": "/v1"`)
	require.Contains(t, text, `"$ref": "#/components/schemas/sample"`)
	require.Contains(t, text, `"$ref": "#/components/schemas/ErrorResponse"`)
	require.Contains(t, text, `"/things"`)
	require.Contains(t, text, `"/things/{id}/events"`)
	require.Contains(t, text, `"text/event-stream"`)
	require.Contains(t, text, `"application/json"`)
	require.NotContains(t, text, "/v1/things\"") // base prefix stripped
}

// TestSchemaOfCompositeRoot guards the package the expansion is bounded
// to when the value's root type is a slice or map rather than a struct:
// []nested must expand nested, not collapse it to an opaque object.
func TestSchemaOfCompositeRoot(t *testing.T) {
	t.Parallel()

	arr := SchemaOf([]nested{})
	require.Equal(t, "array", arr.Type)
	require.Equal(t, "object", arr.Items.Type)
	require.Equal(t, "string", arr.Items.Properties["value"].Type)

	m := SchemaOf(map[string]nested{})
	require.Equal(t, "object", m.Type)
	require.Equal(t, "object", m.AdditionalProperties.Type)
	require.Equal(t, "string", m.AdditionalProperties.Properties["value"].Type)
}

// TestSpecJSONHoistsComponents checks that a shared shape is emitted once
// into components.schemas and referenced, and that the array case
// references the same component rather than inlining a copy.
func TestSpecJSONHoistsComponents(t *testing.T) {
	t.Parallel()
	endpoints := []Endpoint{
		Post("/v1/things").
			Summary("Create thing").
			Accepts(sample{}).
			Responds([]sample{}).
			Fails(400).
			Handle(func(http.ResponseWriter, *http.Request) {}),
	}

	data, err := SpecJSON(Info{Title: "T", Version: "1"}, "/v1", endpoints)
	require.NoError(t, err)

	var doc struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Components struct {
			Schemas map[string]Schema `json:"schemas"`
		} `json:"components"`
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema Schema `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
			Responses map[string]struct {
				Content map[string]struct {
					Schema Schema `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	require.Equal(t, "/v1", doc.Servers[0].URL)
	require.Contains(t, doc.Components.Schemas, "sample")
	require.Contains(t, doc.Components.Schemas, "nested")
	require.Contains(t, doc.Components.Schemas, "ErrorResponse")

	op := doc.Paths["/things"]["post"]
	require.Equal(t, "#/components/schemas/sample",
		op.RequestBody.Content["application/json"].Schema.Ref)

	ok := op.Responses["200"].Content["application/json"].Schema
	require.Equal(t, "array", ok.Type)
	require.Equal(t, "#/components/schemas/sample", ok.Items.Ref)
	require.Equal(t, "#/components/schemas/ErrorResponse",
		op.Responses["400"].Content["application/json"].Schema.Ref)

	// The sample body is emitted once, as a component, not inlined into
	// the request and the response.
	require.Equal(t, 1, strings.Count(string(data), `"sample":`))
}

// TestComponentRegistryNameCollision checks that two unrelated types with
// the same name do not silently share a component: the second is
// qualified with its package's last path segment.
func TestComponentRegistryNameCollision(t *testing.T) {
	t.Parallel()
	reg := newComponentRegistry()

	require.Equal(t, "Month", reg.nameFor(reflect.TypeFor[Month]()))
	require.Equal(t, "time.Month", reg.nameFor(reflect.TypeFor[time.Month]()))
}

// Month exists only to collide with time.Month in the test above.
type Month struct{ X int }

func TestRenderDocs(t *testing.T) {
	t.Parallel()
	info := Info{Title: "Test API", Version: "1.0", Description: "Desc."}
	endpoints := []Endpoint{
		Get("/v1/things/{id}").
			Summary("Get thing").
			Tags("things").
			PathParam("id", "Thing ID").
			Responds(sample{}).
			Fails(404).
			Handle(func(http.ResponseWriter, *http.Request) {}),
	}

	var sb strings.Builder
	require.NoError(t, RenderDocs(&sb, info, "/v1", endpoints))
	html := sb.String()

	require.Contains(t, html, "Test API")
	require.Contains(t, html, "/v1/things/{id}")
	require.Contains(t, html, "Get thing")
	require.Contains(t, html, "Thing ID")
	require.Contains(t, html, "count")
	require.Contains(t, html, "openapi.json")
}

func TestErrorSchemaShape(t *testing.T) {
	t.Parallel()
	// The shared error schema must stay aligned with the error JSON
	// handlers actually write: {"error": "..."}.
	data, err := json.Marshal(errorSchema)
	require.NoError(t, err)
	require.Contains(t, string(data), `"error"`)
}
