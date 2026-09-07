package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsTrailingValues(t *testing.T) {
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"label":"one"}{"label":"two"}`))
	var destination struct {
		Label string `json:"label"`
	}
	if err := decodeJSON(request, &destination); err == nil || !strings.Contains(err.Error(), "trailing content") {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestDecodeJSONBoundsRequestBodies(t *testing.T) {
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"label":"`+strings.Repeat("x", 1<<20)+`"}`))
	var destination struct {
		Label string `json:"label"`
	}
	if err := decodeJSON(request, &destination); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized JSON error = %v", err)
	}
}
