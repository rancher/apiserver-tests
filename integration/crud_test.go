package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rancher/apiserver-tests/pkg/server"
)

func TestWidgetCRUDLifecycle(t *testing.T) {
	handler, _ := server.BuildAPIHandler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	tests := []struct {
		name           string
		method         string
		path           string
		body           interface{}
		expectedStatus int
	}{
		{
			name:           "Create valid widget",
			method:         "POST",
			path:           "/v1/widgets",
			body:           map[string]string{"name": "test-w1", "profile": "production"},
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "Get existing widget",
			method:         "GET",
			path:           "/v1/widgets/test-w1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Delete widget",
			method:         "DELETE",
			path:           "/v1/widgets/test-w1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Verify widget deleted",
			method:         "GET",
			path:           "/v1/widgets/test-w1",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != nil {
				data, _ := json.Marshal(tc.body)
				req, _ = http.NewRequest(tc.method, ts.URL+tc.path, bytes.NewBuffer(data))
			} else {
				req, _ = http.NewRequest(tc.method, ts.URL+tc.path, nil)
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to execute request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.expectedStatus {
				t.Errorf("Expected status %d, got %d", tc.expectedStatus, resp.StatusCode)
			}
		})
	}
}
