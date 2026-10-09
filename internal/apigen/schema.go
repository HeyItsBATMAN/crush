package apigen

import (
	"reflect"
	"strings"
	"time"
)

// Schema is a JSON-Schema-shaped description of a Go value, derived by
// reflection. It supports the subset of JSON Schema used by the docs
// page and the OpenAPI document.
type Schema struct {
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Description          string             `json:"description,omitempty"`
	Ref                  string             `json:"$ref,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
}

var (
	timeType  = reflect.TypeFor[time.Time]()
	errorType = reflect.TypeFor[error]()
)

// SchemaOf derives a [Schema] from the type of v, honoring encoding/json
// tags: field names come from json tags, and fields marked omitempty are
// not required. The result is fully inlined, which is what the docs page
// wants; callers that prefer shared components use a [schemaBuilder]
// carrying a [componentRegistry].
//
// Structs from the same package as v's type are expanded recursively;
// structs from other packages (and true type cycles) are rendered as
// free-form objects described by their type name, which keeps schemas
// for wide external types honest without inventing shape.
func SchemaOf(v any) *Schema {
	return newSchemaBuilder(nil).schemaOfValue(v)
}

// schemaBuilder derives schemas for one value. When refs is non-nil,
// named structs are hoisted into the shared registry and replaced by a
// $ref, so a schema is emitted once instead of once per operation.
type schemaBuilder struct {
	// pkg bounds struct expansion to the package of the value's named
	// root type. It is set once per value in [schemaBuilder.schemaOfValue].
	pkg string
	// expanding guards against type cycles: a struct already on the
	// expansion stack is not re-entered.
	expanding map[reflect.Type]bool
	// refs, when non-nil, hoists named structs into shared components.
	refs *componentRegistry
}

func newSchemaBuilder(refs *componentRegistry) *schemaBuilder {
	return &schemaBuilder{expanding: map[reflect.Type]bool{}, refs: refs}
}

func (b *schemaBuilder) schemaOfValue(v any) *Schema {
	t := reflect.TypeOf(v)
	if t == nil {
		return &Schema{}
	}
	b.pkg = rootPkg(t)
	return b.schemaOf(t)
}

// rootPkg returns the package path of the named type reachable from t by
// descending pointers, slices, arrays, and maps. It is what bounds struct
// expansion to one package, so a value like []proto.Workspace expands
// proto's structs rather than treating the slice's empty package path as
// "nothing expands here".
func rootPkg(t reflect.Type) string {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
		default:
			return t.PkgPath()
		}
	}
}

func (b *schemaBuilder) schemaOf(t reflect.Type) *Schema {
	switch t {
	case timeType:
		return &Schema{Type: "string", Format: "date-time"}
	case errorType:
		return &Schema{Type: "string"}
	}

	switch t.Kind() {
	case reflect.Pointer:
		return b.schemaOf(t.Elem())
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &Schema{Type: "number"}
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Slice, reflect.Array:
		return &Schema{Type: "array", Items: b.schemaOf(t.Elem())}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return &Schema{Type: "object"}
		}
		return &Schema{Type: "object", AdditionalProperties: b.schemaOf(t.Elem())}
	case reflect.Struct:
		return b.structSchema(t)
	default:
		// Interfaces (including any) and anything else: free-form.
		return &Schema{}
	}
}

func (b *schemaBuilder) structSchema(t reflect.Type) *Schema {
	if t.PkgPath() != b.pkg {
		return &Schema{Type: "object", Description: t.String()}
	}

	if b.refs != nil {
		if name := b.refs.nameFor(t); name != "" {
			ref := &Schema{Ref: "#/components/schemas/" + name}
			// A type already on the stack is referenced, not
			// re-entered: that breaks cycles, and a name whose body is
			// already recorded is emitted once.
			if b.expanding[t] || b.refs.has(name) {
				return ref
			}
			b.expanding[t] = true
			b.refs.set(name, b.structBody(t))
			delete(b.expanding, t)
			return ref
		}
		// The name is taken by an unrelated type; inline this one rather
		// than mangle a name the components section would then lie about.
	}

	if b.expanding[t] {
		return &Schema{Type: "object", Description: t.String()}
	}
	b.expanding[t] = true
	defer delete(b.expanding, t)
	return b.structBody(t)
}

func (b *schemaBuilder) structBody(t reflect.Type) *Schema {
	s := &Schema{Type: "object"}
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			// Embedded structs contribute their fields, per
			// encoding/json semantics, even when the embedded type
			// itself is unexported. They are always inlined: a $ref to
			// the embedded type would drop the fields it contributes.
			emb := b.embeddedBody(f.Type)
			for k, v := range emb.Properties {
				if s.Properties == nil {
					s.Properties = map[string]*Schema{}
				}
				s.Properties[k] = v
			}
			s.Required = append(s.Required, emb.Required...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if s.Properties == nil {
			s.Properties = map[string]*Schema{}
		}
		s.Properties[name] = b.schemaOf(f.Type)
		if !strings.Contains(opts, "omitempty") {
			s.Required = append(s.Required, name)
		}
	}
	return s
}

// embeddedBody returns the fields an embedded field contributes, inlined
// even when hoisting is on. Non-struct embedded fields contribute
// nothing, matching the free-form rendering they get on their own.
func (b *schemaBuilder) embeddedBody(t reflect.Type) *Schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.PkgPath() != b.pkg || b.expanding[t] {
		return &Schema{}
	}
	b.expanding[t] = true
	defer delete(b.expanding, t)
	return b.structBody(t)
}

// componentRegistry collects the named schemas hoisted out of endpoint
// bodies so the document references one copy instead of inlining a copy
// into every operation.
type componentRegistry struct {
	byType map[reflect.Type]string
	byName map[string]reflect.Type
	body   map[string]*Schema
}

func newComponentRegistry() *componentRegistry {
	return &componentRegistry{
		byType: map[reflect.Type]string{},
		byName: map[string]reflect.Type{},
		body:   map[string]*Schema{},
	}
}

// schemaOf derives the schema for one endpoint body, hoisting its named
// structs into the shared registry.
func (r *componentRegistry) schemaOf(v any) *Schema {
	return newSchemaBuilder(r).schemaOfValue(v)
}

// nameFor returns the component name for t, assigning one on first use.
// It returns "" when t's natural name is already taken by an unrelated
// type, which tells the caller to inline t instead of hoisting it.
func (r *componentRegistry) nameFor(t reflect.Type) string {
	if name, ok := r.byType[t]; ok {
		return name
	}
	name := t.Name()
	if other, taken := r.byName[name]; taken && other != t {
		// Qualify with the package's last path segment. A collision that
		// survives that is rare enough to inline rather than mangle.
		qualified := lastSegment(t.PkgPath()) + "." + name
		if other, taken := r.byName[qualified]; taken && other != t {
			return ""
		}
		name = qualified
	}
	r.byType[t] = name
	r.byName[name] = t
	return name
}

// has reports whether the body of name has been recorded.
func (r *componentRegistry) has(name string) bool {
	_, ok := r.body[name]
	return ok
}

// set records the body of a hoisted component, once.
func (r *componentRegistry) set(name string, body *Schema) {
	if _, ok := r.body[name]; !ok {
		r.body[name] = body
	}
}

// schemas returns the collected component schemas.
func (r *componentRegistry) schemas() map[string]*Schema {
	return r.body
}

// errorRef returns the $ref to the shared error schema, registering it
// once so every error response does not carry its own copy.
func (r *componentRegistry) errorRef() *Schema {
	const name = "ErrorResponse"
	r.set(name, errorSchema)
	return &Schema{Ref: "#/components/schemas/" + name}
}

// lastSegment returns the final element of a package path, e.g. "proto"
// for "github.com/charmbracelet/crush/internal/proto".
func lastSegment(pkg string) string {
	if i := strings.LastIndexByte(pkg, '/'); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}
