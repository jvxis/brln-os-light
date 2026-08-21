package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// Order 109a4760 produced "Unable to find a route to this destination" 75 times
// and the record kept only that sentence. It cannot distinguish a crashed
// resolver from a rejected input from a real probe that failed, and those need
// different answers - so the code and path are kept alongside it.
func TestMagmaAPIErrorKeepsCodeAndPath(t *testing.T) {
	body := `{"errors":[{"message":"Unable to find a route to this destination.",` +
		`"path":["sellerAcceptOrder"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	var envelope struct {
		Errors []struct {
			Message    string            `json:"message"`
			Path       []json.RawMessage `json:"path"`
			Extensions struct {
				Code json.RawMessage `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	item := envelope.Errors[0]
	apiErr := &magmaAPIError{
		Messages: []string{item.Message},
		Details: []magmaAPIErrorDetail{{
			Message: item.Message,
			Path:    magmaJoinJSONPath(item.Path),
			Code:    magmaUnquoteJSON(item.Extensions.Code),
		}},
	}

	// Error() stays the plain sentence: it is what reaches the UI and Telegram.
	if apiErr.Error() != "Unable to find a route to this destination." {
		t.Fatalf("the operator-facing message must not grow noise, got %q", apiErr.Error())
	}
	diag := apiErr.Diagnostic()
	for _, want := range []string{"code=INTERNAL_SERVER_ERROR", "path=sellerAcceptOrder"} {
		if !strings.Contains(diag, want) {
			t.Fatalf("diagnostic must carry %s, got %q", want, diag)
		}
	}
}

// An error with nothing but a message must still read as that message rather
// than as an empty diagnostic.
func TestMagmaAPIErrorWithoutExtensionsFallsBack(t *testing.T) {
	apiErr := &magmaAPIError{Messages: []string{"boom"}}
	if got := apiErr.Diagnostic(); got != "boom" {
		t.Fatalf("expected the message itself, got %q", got)
	}
}

// Path elements may be list indices, and a numeric code is legal. Decoding only
// JSON strings would silently drop both.
func TestMagmaJSONPathKeepsNonStringElements(t *testing.T) {
	path := []json.RawMessage{json.RawMessage(`"orders"`), json.RawMessage(`0`), json.RawMessage(`"id"`)}
	if got := magmaJoinJSONPath(path); got != "orders.0.id" {
		t.Fatalf("expected orders.0.id, got %q", got)
	}
	if got := magmaUnquoteJSON(json.RawMessage(`503`)); got != "503" {
		t.Fatalf("a numeric code must survive, got %q", got)
	}
	if got := magmaUnquoteJSON(json.RawMessage(`null`)); got != "" {
		t.Fatalf("null must read as absent, got %q", got)
	}
}
