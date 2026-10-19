package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rancher/apiserver-tests/pkg/server"
)

const (
	testManagedBy      = "suse-rancher-apiserver-playground"
	testWidgetEndpoint = "/v1/widgets"
)

func TestRESTValidationAndFormatting(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	client := ts.Client()

	t.Run("reject invalid profile", func(t *testing.T) {
		const name = "broken-widget"

		payload := map[string]string{
			"name":    name,
			"profile": "unsupported-tier",
		}

		resp := postJSON(t, client, ts.URL+testWidgetEndpoint, payload)
		body := readBody(t, resp)

		if resp.StatusCode != http.StatusUnprocessableEntity &&
			resp.StatusCode != http.StatusBadRequest {
			t.Fatalf(
				"Expected validation error (400 or 422), got %d: %s",
				resp.StatusCode,
				body,
			)
		}

		if len(bytes.TrimSpace(body)) == 0 {
			t.Fatal("Expected a non-empty validation error response")
		}

		var apiErr map[string]interface{}
		if err := json.Unmarshal(body, &apiErr); err != nil {
			t.Fatalf("Expected JSON validation error response: %v", err)
		}

		if apiErr["type"] != "error" {
			t.Errorf("Expected error response type %q, got %v", "error", apiErr["type"])
		}

		message, ok := apiErr["message"].(string)
		if !ok || message == "" {
			t.Errorf("Expected a descriptive validation error message, got: %v", apiErr["message"])
		} else if !strings.Contains(message, "profile") {
			t.Errorf("Expected validation message to mention profile, got %q", message)
		}

		t.Logf("Server correctly rejected invalid payload: %s", body)

		assertWidgetNotFound(t, client, ts.URL, name)
	})

	t.Run("reject missing profile", func(t *testing.T) {
		const name = "missing-profile-widget"

		payload := map[string]string{
			"name": name,
		}

		resp := postJSON(t, client, ts.URL+testWidgetEndpoint, payload)
		body := readBody(t, resp)

		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf(
				"Expected a 4xx response for missing profile, got %d: %s",
				resp.StatusCode,
				body,
			)
		}

		assertWidgetNotFound(t, client, ts.URL, name)
	})

	t.Run("reject malformed JSON", func(t *testing.T) {
		const name = "malformed-widget"

		req, err := http.NewRequest(
			http.MethodPost,
			ts.URL+testWidgetEndpoint,
			bytes.NewBufferString(`{"name": "malformed-widget", "profile": `),
		)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to execute malformed JSON request: %v", err)
		}

		body := readBody(t, resp)

		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf(
				"Expected a 4xx response for malformed JSON, got %d: %s",
				resp.StatusCode,
				body,
			)
		}

		assertWidgetNotFound(t, client, ts.URL, name)
	})

	t.Run("accept production profile and format response", func(t *testing.T) {
		assertValidWidgetResponse(t, client, ts.URL, "production-widget", "production")
	})

	t.Run("accept staging profile and format response", func(t *testing.T) {
		assertValidWidgetResponse(t, client, ts.URL, "staging-widget", "staging")
	})
}

// assertValidWidgetResponse verifies creation, response formatting, and retrieval.
func assertValidWidgetResponse(
	t *testing.T,
	client *http.Client,
	baseURL string,
	name string,
	profile string,
) {
	t.Helper()

	payload := map[string]string{
		"name":    name,
		"profile": profile,
	}

	resp := postJSON(t, client, baseURL+testWidgetEndpoint, payload)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusCreated {
		t.Fatalf(
			"Expected successful creation (200 or 201), got %d: %s",
			resp.StatusCode,
			body,
		)
	}

	var output map[string]interface{}
	if err := json.Unmarshal(body, &output); err != nil {
		t.Fatalf("Failed to decode creation response: %v", err)
	}

	assertWidgetFields(t, output, name, profile)
	assertWidgetFormatting(t, output)

	// Verify that the created resource can be retrieved.
	getResp, err := client.Get(
		fmt.Sprintf("%s%s/%s", baseURL, testWidgetEndpoint, name),
	)
	if err != nil {
		t.Fatalf("Failed to retrieve widget %q: %v", name, err)
	}

	getBody := readBody(t, getResp)

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf(
			"Expected GET status 200 for widget %q, got %d: %s",
			name,
			getResp.StatusCode,
			getBody,
		)
	}

	var retrieved map[string]interface{}
	if err := json.Unmarshal(getBody, &retrieved); err != nil {
		t.Fatalf("Failed to decode retrieved widget: %v", err)
	}

	assertWidgetFields(t, retrieved, name, profile)
}

// assertWidgetFormatting verifies the custom formatter's response fields.
func assertWidgetFormatting(t *testing.T, output map[string]interface{}) {
	t.Helper()

	if got := output["managedBy"]; got != testManagedBy {
		t.Errorf("Expected managedBy %q, got %v", testManagedBy, got)
	}

	links, ok := output["links"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected response links object, got %v", output["links"])
	}

	reconcile, ok := links["reconcile"].(string)
	if !ok || reconcile == "" {
		t.Fatalf("Expected non-empty reconcile link, got %v", links["reconcile"])
	}

	if !strings.HasSuffix(reconcile, "/reconcile") {
		t.Errorf("Expected reconcile link to end with /reconcile, got %q", reconcile)
	}
}

// postJSON sends a JSON POST request.
// The caller must close the response body.
func postJSON(
	t *testing.T,
	client *http.Client,
	url string,
	payload interface{},
) *http.Response {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Failed to marshal JSON payload: %v", err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		url,
		bytes.NewReader(data),
	)
	if err != nil {
		t.Fatalf("Failed to create POST request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to execute POST request: %v", err)
	}

	return resp
}

// readBody reads and closes the response body.
func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read HTTP response body: %v", err)
	}

	return body
}

// assertWidgetFields verifies the expected resource fields.
func assertWidgetFields(
	t *testing.T,
	output map[string]interface{},
	expectedName string,
	expectedProfile string,
) {
	t.Helper()

	if got := output["name"]; got != expectedName {
		t.Errorf("Expected name %q, got %v", expectedName, got)
	}

	if got := output["profile"]; got != expectedProfile {
		t.Errorf("Expected profile %q, got %v", expectedProfile, got)
	}
}

// assertWidgetNotFound verifies that a rejected create did not persist a resource.
func assertWidgetNotFound(
	t *testing.T,
	client *http.Client,
	baseURL string,
	name string,
) {
	t.Helper()

	resp, err := client.Get(
		fmt.Sprintf("%s%s/%s", baseURL, testWidgetEndpoint, name),
	)
	if err != nil {
		t.Fatalf("Failed to check whether widget %q exists: %v", name, err)
	}

	body := readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf(
			"Expected widget %q to be absent (404), got %d: %s",
			name,
			resp.StatusCode,
			body,
		)
	}
}

func TestPUTRejectsInvalidProfile(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()
	widgetName := "put-validation-widget"
	widgetURL := ts.URL + "/v1/widgets/" + widgetName

	doRequest := func(method, url string, payload map[string]interface{}) (*http.Response, []byte) {
		t.Helper()

		var body io.Reader
		if payload != nil {
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("Failed to marshal request payload: %v", err)
			}
			body = bytes.NewReader(data)
		}

		req, err := http.NewRequest(method, url, body)
		if err != nil {
			t.Fatalf("Failed to create %s request: %v", method, err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s failed: %v", method, url, err)
		}
		defer resp.Body.Close()

		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		return resp, responseBody
	}

	t.Run("create valid widget", func(t *testing.T) {
		resp, body := doRequest(http.MethodPost, ts.URL+"/v1/widgets", map[string]interface{}{
			"name":    widgetName,
			"profile": "production",
		})

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected successful creation, got HTTP %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("reject invalid profile update", func(t *testing.T) {
		resp, body := doRequest(http.MethodPut, widgetURL, map[string]interface{}{
			"name":    widgetName,
			"profile": "development",
		})

		if resp.StatusCode < 400 {
			t.Errorf(
				"Expected PUT with invalid profile to be rejected, got HTTP %d: %s",
				resp.StatusCode,
				body,
			)
		}
	})

	t.Run("preserve original profile", func(t *testing.T) {
		resp, body := doRequest(http.MethodGet, widgetURL, nil)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected GET to return HTTP 200, got HTTP %d: %s", resp.StatusCode, body)
		}

		var output map[string]interface{}
		if err := json.Unmarshal(body, &output); err != nil {
			t.Fatalf("Failed to decode GET response: %v; body: %s", err, body)
		}

		if got := output["profile"]; got != "production" {
			t.Errorf("Expected profile to remain %q after rejected update, got %v", "production", got)
		}
	})
}

func TestPUTRejectsMalformedJSONWithoutMutation(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()
	const widgetName = "malformed-put-widget"
	widgetURL := ts.URL + testWidgetEndpoint + "/" + widgetName

	// Create a known-good resource.
	resp := postJSON(t, client, ts.URL+testWidgetEndpoint, map[string]string{
		"name":    widgetName,
		"profile": "production",
	})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("failed to create initial widget: HTTP %d: %s", resp.StatusCode, body)
	}

	// Submit malformed JSON to the update endpoint.
	req, err := http.NewRequest(
		http.MethodPut,
		widgetURL,
		bytes.NewBufferString(`{"name":"malformed-put-widget","profile":`),
	)
	if err != nil {
		t.Fatalf("failed to create PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to execute malformed PUT request: %v", err)
	}
	body = readBody(t, resp)

	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("expected a 4xx response for malformed JSON, got HTTP %d: %s", resp.StatusCode, body)
	}

	// Verify the rejected update preserved the original resource.
	resp, err = client.Get(widgetURL)
	if err != nil {
		t.Fatalf("failed to retrieve widget after rejected update: %v", err)
	}
	body = readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 when retrieving original widget, got HTTP %d: %s", resp.StatusCode, body)
	}

	var widget map[string]interface{}
	if err := json.Unmarshal(body, &widget); err != nil {
		t.Fatalf("failed to decode retrieved widget: %v; body: %s", err, body)
	}

	if widget["name"] != widgetName {
		t.Errorf("expected name %q, got %#v", widgetName, widget["name"])
	}
	if widget["profile"] != "production" {
		t.Errorf("expected profile to remain %q, got %#v", "production", widget["profile"])
	}
	if widget["managedBy"] != testManagedBy {
		t.Errorf("expected managedBy %q, got %#v", testManagedBy, widget["managedBy"])
	}
}
