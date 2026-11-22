package ppdm

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// conformanceRows lists every collector decode struct and the vendored spec + endpoint
// whose response item it must conform to (ADR-0010, ADR-0016).
var conformanceRows = []struct {
	typ        reflect.Type
	spec, path string
}{
	{reflect.TypeOf(activity{}), "21201-20.3.0.json", "/api/v2/activities"},
	{reflect.TypeOf(asset{}), "21201-20.3.0.json", "/api/v2/assets"},
	{reflect.TypeOf(mtree{}), "21201-20.3.0.json", "/api/v2/datadomain-mtrees"},
	{reflect.TypeOf(storageSystem{}), "21201-20.3.0.json", "/api/v2/storage-systems"},
	{reflect.TypeOf(alert{}), "21201-20.3.0.json", "/api/v2/alerts"},
	{reflect.TypeOf(healthResult{}), "21202-20.3.0.json", "/api/v3/health-results"},
}

var (
	specMu    sync.Mutex
	specCache = map[string]map[string]any{}
)

func loadSpec(t *testing.T, file string) map[string]any {
	t.Helper()
	specMu.Lock()
	defer specMu.Unlock()
	if s, ok := specCache[file]; ok {
		return s
	}
	b, err := os.ReadFile("../../docs/swagger/" + file)
	if err != nil {
		t.Fatalf("read spec %s: %v", file, err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("parse spec %s: %v", file, err)
	}
	specCache[file] = s
	return s
}

// itemSchema returns the resolved schema of one content[] item of the GET list response.
func itemSchema(t *testing.T, spec map[string]any, path string) map[string]any {
	t.Helper()
	path, _, _ = strings.Cut(path, "?")
	var node any = spec
	for _, k := range []string{"paths", path, "get", "responses", "200", "content"} {
		m, _ := node.(map[string]any)
		node = m[k]
	}
	content, _ := node.(map[string]any)
	for _, media := range content {
		m, _ := media.(map[string]any)
		page := resolve(spec, asMap(m["schema"]))
		props := asMap(page["properties"])
		items := asMap(asMap(props["content"])["items"])
		if len(items) == 0 {
			t.Fatalf("%s: no content.items in list response", path)
		}
		return resolve(spec, items)
	}
	t.Fatalf("%s: no 200 response content in spec", path)
	return nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// resolve follows a $ref and merges the properties of every allOf member.
func resolve(spec, node map[string]any) map[string]any {
	if ref, ok := node["$ref"].(string); ok {
		var cur any = spec
		for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cur = asMap(cur)[seg]
		}
		return resolve(spec, asMap(cur))
	}
	out := map[string]any{}
	props := map[string]any{}
	for k, v := range node {
		if k != "allOf" {
			out[k] = v
		}
	}
	for k, v := range asMap(node["properties"]) {
		props[k] = v
	}
	if all, ok := node["allOf"].([]any); ok {
		for _, a := range all {
			r := resolve(spec, asMap(a))
			for k, v := range asMap(r["properties"]) {
				props[k] = v
			}
			if _, has := out["type"]; !has && r["type"] != nil {
				out["type"] = r["type"]
			}
		}
	}
	if len(props) > 0 {
		out["properties"] = props
	}
	return out
}

// checkStruct reports every json-tagged field of typ that is absent from schema or whose
// JSON kind conflicts with the spec. oneOf/anyOf nodes are skipped (lenient).
func checkStruct(spec, schema map[string]any, typ reflect.Type, at string) []string {
	schema = resolve(spec, schema)
	if schema["oneOf"] != nil || schema["anyOf"] != nil {
		return nil
	}
	props := asMap(schema["properties"])
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		p, ok := props[name].(map[string]any)
		if !ok {
			out = append(out, at+"."+name+": not in spec")
			continue
		}
		out = append(out, checkValue(spec, p, f.Type, at+"."+name)...)
	}
	return out
}

func checkValue(spec, node map[string]any, typ reflect.Type, at string) []string {
	node = resolve(spec, node)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if node["oneOf"] != nil || node["anyOf"] != nil {
		return nil
	}
	jt, _ := node["type"].(string)
	if jt == "" && node["properties"] != nil {
		jt = "object"
	}
	mismatch := func(got string) []string { return []string{at + ": Go " + got + " vs spec type " + jt} }
	switch typ.Kind() {
	case reflect.Struct:
		if jt != "" && jt != "object" {
			return mismatch("struct")
		}
		return checkStruct(spec, node, typ, at)
	case reflect.Slice:
		if jt != "" && jt != "array" {
			return mismatch("slice")
		}
		if typ.Elem().Kind() == reflect.Struct {
			return checkValue(spec, asMap(node["items"]), typ.Elem(), at+"[]")
		}
	case reflect.String:
		if jt != "" && jt != "string" {
			return mismatch("string")
		}
	case reflect.Bool:
		if jt != "" && jt != "boolean" {
			return mismatch("bool")
		}
	case reflect.Float32, reflect.Float64, reflect.Int, reflect.Int32, reflect.Int64:
		if jt != "" && jt != "number" && jt != "integer" {
			return mismatch("number")
		}
	}
	return nil
}

func TestCollectorStructsMatchSpec(t *testing.T) {
	for _, row := range conformanceRows {
		t.Run(row.typ.Name(), func(t *testing.T) {
			spec := loadSpec(t, row.spec)
			for _, p := range checkStruct(spec, itemSchema(t, spec, row.path), row.typ, row.typ.Name()) {
				t.Error(p)
			}
		})
	}
}

func TestCheckStructDetects(t *testing.T) {
	spec := map[string]any{"components": map[string]any{"schemas": map[string]any{
		"Base": map[string]any{"properties": map[string]any{"name": map[string]any{"type": "string"}}},
	}}}
	schema := map[string]any{"allOf": []any{
		map[string]any{"$ref": "#/components/schemas/Base"},
		map[string]any{"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
			"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}},
	}}

	type ok struct {
		Name  string   `json:"name"`
		Count float64  `json:"count"`
		Tags  []string `json:"tags"`
	}
	type ghost struct {
		Name  string `json:"name"`
		Ghost string `json:"ghost"`
	}
	type badKind struct {
		Tags string `json:"tags"`
	}

	if p := checkStruct(spec, schema, reflect.TypeOf(ok{}), "ok"); len(p) != 0 {
		t.Errorf("ok struct: unexpected problems %v", p)
	}
	if p := checkStruct(spec, schema, reflect.TypeOf(ghost{}), "ghost"); len(p) != 1 || !strings.Contains(p[0], "ghost") {
		t.Errorf("ghost: want one problem naming ghost, got %v", p)
	}
	if p := checkStruct(spec, schema, reflect.TypeOf(badKind{}), "bad"); len(p) != 1 || !strings.Contains(p[0], "tags") {
		t.Errorf("badKind: want one problem naming tags, got %v", p)
	}
}

// TestConformanceBitesOnRealSpec guards against a vacuous pass: a bogus field and a
// wrong kind must both be reported against the real 20.3.0 asset schema.
func TestConformanceBitesOnRealSpec(t *testing.T) {
	spec := loadSpec(t, "21201-20.3.0.json")
	schema := itemSchema(t, spec, "/api/v2/assets?filter=x")
	type bogus struct {
		Name  string `json:"name"`
		Ghost string `json:"noSuchField"`
		Count bool   `json:"name2"`
	}
	if p := checkStruct(spec, schema, reflect.TypeOf(bogus{}), "bogus"); len(p) != 2 {
		t.Errorf("want 2 problems (noSuchField, name2), got %v", p)
	}
}
