package ppdmclient

import (
	"context"
	"testing"
)

func TestMockClientServesRegisteredPath(t *testing.T) {
	m := NewMock("ppdm01")
	m.SetJSON("/api/v2/activities", `{"content":[{"id":"a1"}],"page":{"totalPages":1}}`)

	var out struct {
		Content []struct {
			ID string `json:"id"`
		} `json:"content"`
	}
	if err := m.Get(context.Background(), "/api/v2/activities", &out); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if len(out.Content) != 1 || out.Content[0].ID != "a1" {
		t.Fatalf("decoded = %+v, want one activity a1", out.Content)
	}
}

func TestMockClientPrefixMatchIgnoresQuery(t *testing.T) {
	m := NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/assets", `{"content":[{"id":"x"}],"page":{"totalPages":1}}`)
	var out map[string]any
	if err := m.Get(context.Background(), "/api/v2/assets?page=0&pageSize=500", &out); err != nil {
		t.Fatalf("prefix Get error: %v", err)
	}
}

func TestMockClientUnknownPathErrors(t *testing.T) {
	m := NewMock("ppdm01")
	var out map[string]any
	if err := m.Get(context.Background(), "/nope", &out); err == nil {
		t.Fatal("expected error for unregistered path")
	}
}
