package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The web clip finder (mvd-web/src/player/ClipFinder.svelte) documents the
// endpoints it runs — their summary and description, each query param and
// each row field — from this spec. It reads a compact extract,
// mvd-web/src/lib/api-docs.json, generated here and pinned by this test so
// it moves with the spec:
//
//	go test ./mvd-api -run TestWebAPIDocs -args -update-webdocs
var updateWebDocs = flag.Bool("update-webdocs", false, "rewrite mvd-web/src/lib/api-docs.json from openapi.yaml")

const webDocsPath = "../mvd-web/src/lib/api-docs.json"

// The finder's sources (SOURCES in mvd-web/src/lib/clip-query.js) and the
// body lists their rows come from.
var webDocSources = []struct {
	name string
	rows []string
}{
	{"events", []string{"events"}},
	{"highlights", []string{"discharges", "quadbores", "telefrags", "airgibs"}},
	{"backpacks", []string{"backpacks"}},
	{"top-kills", []string{"kills"}},
	{"top-windows", []string{"windows"}},
	{"lives", []string{"lives"}},
}

type webDocParam struct {
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type webDocField struct {
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type webDocSource struct {
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Params      map[string]webDocParam `json:"params"`
	Fields      map[string]webDocField `json:"fields"`
}

type webSpec struct {
	root map[string]any
}

// resolve follows a "#/components/..." $ref.
func (s webSpec) resolve(node any) map[string]any {
	m, _ := node.(map[string]any)
	for m != nil {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		var cur any = s.root
		for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cm, _ := cur.(map[string]any)
			cur = cm[seg]
		}
		m, _ = cur.(map[string]any)
	}
	return m
}

func strList(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func trimDoc(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// fields walks a row schema — properties, allOf / anyOf / oneOf branches,
// $refs — into dotted paths, a list of objects by its first element
// ('victims.0.name') as clip-query.js fieldPaths names them.
func (s webSpec) fields(node any, path string, depth int, out map[string]webDocField) {
	m := s.resolve(node)
	if m == nil || depth > 5 {
		return
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		branches, _ := m[k].([]any)
		for _, b := range branches {
			s.fields(b, path, depth, out)
		}
	}
	typ, _ := m["type"].(string)
	if typ == "array" {
		items := s.resolve(m["items"])
		if items != nil && (items["properties"] != nil || items["allOf"] != nil) {
			s.fields(items, path+".0", depth+1, out)
			return
		}
	}
	props, _ := m["properties"].(map[string]any)
	if len(props) == 0 || depth > 4 {
		if path != "" {
			f := out[path]
			if f.Description == "" {
				f.Description = trimDoc(m["description"])
			}
			if f.Type == "" {
				f.Type = typ
			}
			if f.Enum == nil {
				f.Enum = strList(m["enum"])
			}
			out[path] = f
		}
		return
	}
	for k, v := range props {
		p := k
		if path != "" {
			p = path + "." + k
		}
		child := s.resolve(v)
		s.fields(child, p, depth+1, out)
		// A property's own description outranks what its schema says.
		if d := trimDoc(child["description"]); d != "" {
			if f, ok := out[p]; ok && f.Description == "" {
				f.Description = d
				out[p] = f
			}
		}
	}
}

func buildWebDocs(t *testing.T) []byte {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal(openapiSpec, &root); err != nil {
		t.Fatalf("yaml parse: %v", err)
	}
	s := webSpec{root}
	paths, _ := root["paths"].(map[string]any)
	out := map[string]webDocSource{}
	for _, src := range webDocSources {
		path, _ := paths["/v1/demos/{id}/"+src.name].(map[string]any)
		op, _ := path["get"].(map[string]any)
		if op == nil {
			t.Fatalf("openapi.yaml has no GET /v1/demos/{id}/%s", src.name)
		}
		doc := webDocSource{
			Summary:     trimDoc(op["summary"]),
			Description: trimDoc(op["description"]),
			Params:      map[string]webDocParam{},
			Fields:      map[string]webDocField{},
		}
		for _, p := range op["parameters"].([]any) {
			pm := s.resolve(p)
			if pm["in"] != "query" {
				continue
			}
			schema := s.resolve(pm["schema"])
			param := webDocParam{Description: trimDoc(pm["description"]), Default: schema["default"]}
			param.Type, _ = schema["type"].(string)
			param.Enum = strList(schema["enum"])
			if items := s.resolve(schema["items"]); items != nil {
				param.Enum = strList(items["enum"])
			}
			doc.Params[fmt.Sprint(pm["name"])] = param
		}
		body := s.resolve(s.resolve(s.resolve(s.resolve(s.resolve(op["responses"])["200"])["content"])["application/json"])["schema"])
		props, _ := body["properties"].(map[string]any)
		for _, list := range src.rows {
			l := s.resolve(props[list])
			if l == nil {
				t.Fatalf("/%s body has no %q list", src.name, list)
			}
			s.fields(l["items"], "", 0, doc.Fields)
		}
		out[src.name] = doc
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWebAPIDocs(t *testing.T) {
	got := buildWebDocs(t)
	if *updateWebDocs {
		if err := os.WriteFile(webDocsPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(webDocsPath)
	if err != nil {
		t.Fatalf("read %s: %v (generate it with -update-webdocs)", webDocsPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale against openapi.yaml; regenerate with:\n  go test ./mvd-api -run TestWebAPIDocs -args -update-webdocs", webDocsPath)
	}
}

// The finder offers every param it maps (SOURCES params) and the spec
// documents; a param it offers that the spec lacks is a typo or a rename.
func TestWebAPIDocsCoverFinderParams(t *testing.T) {
	src, err := os.ReadFile("../mvd-web/src/lib/clip-query.js")
	if err != nil {
		t.Skip("mvd-web not present")
	}
	var docs map[string]webDocSource
	if err := json.Unmarshal(buildWebDocs(t), &docs); err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for name, d := range docs {
		i := strings.Index(text, fmt.Sprintf("\n    %s: {", quoteKey(name)))
		if i < 0 {
			t.Errorf("clip-query.js SOURCES has no %q", name)
			continue
		}
		block := text[i:]
		start := strings.Index(block, "params: [")
		end := strings.Index(block[start:], "]")
		if start < 0 || end < 0 {
			t.Errorf("SOURCES %q: no params list", name)
			continue
		}
		var missing []string
		for _, p := range strings.Split(block[start+len("params: ["):start+end], ",") {
			p = strings.Trim(strings.TrimSpace(p), "'")
			if p == "" {
				continue
			}
			if _, ok := d.Params[p]; !ok {
				missing = append(missing, p)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("SOURCES %q offers params the spec does not document: %v", name, missing)
		}
	}
}

func quoteKey(name string) string {
	if strings.Contains(name, "-") {
		return "'" + name + "'"
	}
	return name
}
