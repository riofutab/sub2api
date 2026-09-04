package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseCustomMenuItemsKeepsLegacyEmbedParamsEnabledByDefault(t *testing.T) {
	items := ParseCustomMenuItems(`[{"id":"legacy","label":"Legacy","url":"https://example.com","visibility":"user","sort_order":0}]`)
	if len(items) != 1 {
		t.Fatalf("expected one menu item, got %d", len(items))
	}
	if items[0].AppendEmbedParams != nil {
		t.Fatalf("expected legacy append_embed_params to remain unspecified, got %v", *items[0].AppendEmbedParams)
	}

	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal menu items: %v", err)
	}
	if strings.Contains(string(encoded), "append_embed_params") {
		t.Fatalf("legacy menu item should not be rewritten as disabled: %s", encoded)
	}
}

func TestParseCustomMenuItemsPreservesExplicitlyDisabledEmbedParams(t *testing.T) {
	items := ParseCustomMenuItems(`[{"id":"private","label":"Private","url":"https://example.com","visibility":"user","sort_order":0,"append_embed_params":false}]`)
	if len(items) != 1 {
		t.Fatalf("expected one menu item, got %d", len(items))
	}
	if items[0].AppendEmbedParams == nil || *items[0].AppendEmbedParams {
		t.Fatal("expected explicit append_embed_params=false to be preserved")
	}
}
