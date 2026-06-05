package ppdmclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type kv struct{ k, v string }

// Mock is an in-memory Client that serves canned JSON bodies per path. Tests use it
// to drive collectors without a live server.
type Mock struct {
	name     string
	paths    map[string]string // exact-path match
	prefixes []kv              // path-prefix match (ignores query string)
}

// NewMock returns an empty Mock for the named server.
func NewMock(name string) *Mock { return &Mock{name: name, paths: map[string]string{}} }

// SetJSON registers a response body for an exact path.
func (m *Mock) SetJSON(path, body string) { m.paths[path] = body }

// SetJSONPrefix registers a body matched by path prefix (ignores the query string),
// so paginated calls like "/api/v2/assets?page=0&pageSize=500" resolve to one body.
func (m *Mock) SetJSONPrefix(prefix, body string) { m.prefixes = append(m.prefixes, kv{prefix, body}) }

func (m *Mock) Name() string { return m.name }

func (m *Mock) Get(_ context.Context, path string, out any) error {
	if body, ok := m.paths[path]; ok {
		return json.Unmarshal([]byte(body), out)
	}
	for _, p := range m.prefixes {
		if strings.HasPrefix(path, p.k) {
			return json.Unmarshal([]byte(p.v), out)
		}
	}
	return fmt.Errorf("mock: no response registered for %s", path)
}

func (m *Mock) Close() error { return nil }
