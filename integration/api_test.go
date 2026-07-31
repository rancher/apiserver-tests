package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rancher/apiserver-tests/pkg/server"
)

func TestRESTValidationAndFormatting(t *testing.T) {
	handler, _ := server.BuildAPIHandler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Validate that input normalizer correctly blocks invalid data fields
	badPayload, _ := json.Marshal(map[string]string{"name": "broken-widget", "profile": "unsupported-tier"})
	resp, _ := http.Post(ts.URL+"/v1/widgets", "application/json", bytes.NewReader(badPayload))

	if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected validation error (422 or 400), got status code: %d", resp.StatusCode)
	}

	var errData map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&errData)
	t.Logf("Server correctly rejected data payload: %v", errData)

	// Post valid asset configurations
	goodPayload, _ := json.Marshal(map[string]string{"name": "valid-widget", "profile": "production"})
	resp, _ = http.Post(ts.URL+"/v1/widgets", "application/json", bytes.NewReader(goodPayload))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected successful payload execution, got status code: %d", resp.StatusCode)
	}

	// Ensure custom formatters inject operational HATEOAS extensions cleanly
	var outputData map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&outputData)

	links, ok := outputData["links"].(map[string]interface{})
	if !ok || links["reconcile"] == "" {
		t.Errorf("Formatter failed to decorate payload context with 'reconcile' directive links. Got: %v", outputData)
	}
}