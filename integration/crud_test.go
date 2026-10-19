package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rancher/apiserver-tests/pkg/server"
)

func TestWidgetCRUDLifecycle(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()
	const widgetName = "test-w1"
	const widgetURL = "/v1/widgets/" + widgetName

	doRequest := func(method, path string, payload map[string]string) (int, map[string]interface{}) {
		t.Helper()

		var body io.Reader
		if payload != nil {
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("Failed to marshal request payload: %v", err)
			}
			body = bytes.NewReader(data)
		}

		req, err := http.NewRequest(method, ts.URL+path, body)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request %s %s failed: %v", method, path, err)
		}
		defer resp.Body.Close()

		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		var output map[string]interface{}
		if len(responseBody) > 0 {
			if err := json.Unmarshal(responseBody, &output); err != nil {
				t.Fatalf("Failed to decode response from %s %s: %v; body: %s",
					method, path, err, responseBody)
			}
		}

		return resp.StatusCode, output
	}

	assertWidget := func(t *testing.T, output map[string]interface{}, expectedName, expectedProfile string) {
		t.Helper()

		if got := output["name"]; got != expectedName {
			t.Errorf("Expected name %q, got %v", expectedName, got)
		}
		if got := output["profile"]; got != expectedProfile {
			t.Errorf("Expected profile %q, got %v", expectedProfile, got)
		}
		if got := output["managedBy"]; got != testManagedBy {
			t.Errorf("Expected managedBy %q, got %v", testManagedBy, got)
		}
	}

	t.Run("Create valid widget", func(t *testing.T) {
		status, output := doRequest(http.MethodPost, "/v1/widgets", map[string]string{
			"name":    widgetName,
			"profile": "production",
		})

		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("Expected successful creation, got HTTP %d: %v", status, output)
		}
		assertWidget(t, output, widgetName, "production")
	})

	t.Run("Get existing widget", func(t *testing.T) {
		status, output := doRequest(http.MethodGet, widgetURL, nil)
		if status != http.StatusOK {
			t.Fatalf("Expected HTTP 200, got HTTP %d: %v", status, output)
		}
		assertWidget(t, output, widgetName, "production")
	})

	t.Run("Update widget profile", func(t *testing.T) {
		status, output := doRequest(http.MethodPut, widgetURL, map[string]string{
			"name":    widgetName,
			"profile": "staging",
		})
		if status != http.StatusOK {
			t.Fatalf("Expected HTTP 200 for update, got HTTP %d: %v", status, output)
		}
		assertWidget(t, output, widgetName, "staging")
	})

	t.Run("Verify updated widget persists", func(t *testing.T) {
		status, output := doRequest(http.MethodGet, widgetURL, nil)
		if status != http.StatusOK {
			t.Fatalf("Expected HTTP 200, got HTTP %d: %v", status, output)
		}
		assertWidget(t, output, widgetName, "staging")
	})

	t.Run("Update nonexistent widget", func(t *testing.T) {
		status, output := doRequest(http.MethodPut, "/v1/widgets/does-not-exist", map[string]string{
			"name":    "does-not-exist",
			"profile": "staging",
		})
		if status != http.StatusNotFound {
			t.Fatalf("Expected HTTP 404 for nonexistent widget update, got HTTP %d: %v", status, output)
		}
	})

	t.Run("Verify failed update did not create widget", func(t *testing.T) {
		status, output := doRequest(http.MethodGet, "/v1/widgets/does-not-exist", nil)
		if status != http.StatusNotFound {
			t.Fatalf("Expected HTTP 404, got HTTP %d: %v", status, output)
		}
	})

	t.Run("Delete widget", func(t *testing.T) {
		status, output := doRequest(http.MethodDelete, widgetURL, nil)
		if status != http.StatusOK {
			t.Fatalf("Expected HTTP 200 for deletion, got HTTP %d: %v", status, output)
		}

		// The API may return the deleted resource; if it does, verify its data.
		if output["name"] != nil {
			assertWidget(t, output, widgetName, "staging")
		}
	})

	t.Run("Verify widget deleted", func(t *testing.T) {
		status, output := doRequest(http.MethodGet, widgetURL, nil)
		if status != http.StatusNotFound {
			t.Fatalf("Expected HTTP 404 after deletion, got HTTP %d: %v", status, output)
		}
	})
}

func TestDuplicateWidgetCreation(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	postWidget := func(name, profile string) (int, []byte) {
		t.Helper()

		payload, err := json.Marshal(map[string]string{
			"name":    name,
			"profile": profile,
		})
		if err != nil {
			t.Fatalf("Failed to marshal widget: %v", err)
		}

		resp, err := client.Post(
			ts.URL+"/v1/widgets",
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			t.Fatalf("Failed to create widget: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response: %v", err)
		}

		return resp.StatusCode, body
	}

	t.Run("create original widget", func(t *testing.T) {
		status, body := postWidget("duplicate-widget", "production")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("Expected successful creation, got HTTP %d: %s", status, body)
		}
	})

	t.Run("reject duplicate name", func(t *testing.T) {
		status, body := postWidget("duplicate-widget", "staging")
		if status < 400 {
			t.Fatalf("Expected duplicate creation to be rejected, got HTTP %d: %s", status, body)
		}
	})

	t.Run("preserve original widget", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/v1/widgets/duplicate-widget")
		if err != nil {
			t.Fatalf("Failed to retrieve widget: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read widget response: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected HTTP 200, got HTTP %d: %s", resp.StatusCode, body)
		}

		var widget map[string]interface{}
		if err := json.Unmarshal(body, &widget); err != nil {
			t.Fatalf("Failed to decode widget: %v; body: %s", err, body)
		}

		if got := widget["profile"]; got != "production" {
			t.Errorf("Expected original profile to remain production, got %v", got)
		}
	})
}

func TestConcurrentDuplicateWidgetCreation(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	const (
		goroutineCount = 10
		widgetName     = "concurrent-widget"
	)

	client := ts.Client()
	payload, err := json.Marshal(map[string]string{
		"name":    widgetName,
		"profile": "production",
	})
	if err != nil {
		t.Fatalf("Failed to marshal widget: %v", err)
	}

	start := make(chan struct{})
	statuses := make(chan int, goroutineCount)
	errs := make(chan error, goroutineCount)

	var wg sync.WaitGroup
	wg.Add(goroutineCount)

	for i := 0; i < goroutineCount; i++ {
		go func() {
			defer wg.Done()

			// Wait until all goroutines are ready to start.
			<-start

			req, err := http.NewRequest(
				http.MethodPost,
				ts.URL+"/v1/widgets",
				bytes.NewReader(payload),
			)
			if err != nil {
				errs <- err
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()

			statuses <- resp.StatusCode
		}()
	}

	// Release all requests together.
	close(start)
	wg.Wait()
	close(statuses)
	close(errs)

	for err := range errs {
		t.Errorf("Concurrent request failed: %v", err)
	}

	successes := 0
	conflicts := 0

	for status := range statuses {
		switch status {
		case http.StatusCreated, http.StatusOK:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("Unexpected HTTP status: %d", status)
		}
	}

	if successes != 1 {
		t.Errorf("Expected exactly 1 successful creation, got %d", successes)
	}
	if conflicts != goroutineCount-1 {
		t.Errorf(
			"Expected %d conflicts, got %d",
			goroutineCount-1,
			conflicts,
		)
	}

	// Verify the winning request left a valid resource.
	resp, err := client.Get(ts.URL + "/v1/widgets/" + widgetName)
	if err != nil {
		t.Fatalf("Failed to retrieve widget: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read widget response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf(
			"Expected HTTP 200, got HTTP %d: %s",
			resp.StatusCode,
			body,
		)
	}

	var widget map[string]interface{}
	if err := json.Unmarshal(body, &widget); err != nil {
		t.Fatalf("Failed to decode widget: %v; body: %s", err, body)
	}

	if got := widget["profile"]; got != "production" {
		t.Errorf("Expected profile production, got %v", got)
	}
}

func TestConcurrentWidgetUpdateAndDelete(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()
	widgetName := "concurrent-update-delete"

	// Create the widget before racing the update and delete.
	payload, err := json.Marshal(map[string]string{
		"name":    widgetName,
		"profile": "production",
	})
	if err != nil {
		t.Fatalf("Failed to marshal widget: %v", err)
	}

	resp, err := client.Post(
		ts.URL+"/v1/widgets",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("Failed to create widget: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("Failed to read create response: %v", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected successful creation, got HTTP %d: %s", resp.StatusCode, body)
	}

	start := make(chan struct{})
	results := make(chan struct {
		operation string
		status    int
		err       error
	}, 2)

	var wg sync.WaitGroup
	wg.Add(2)

	// Race an update against a delete.
	go func() {
		defer wg.Done()
		<-start

		updatePayload, err := json.Marshal(map[string]string{
			"name":    widgetName,
			"profile": "staging",
		})
		if err != nil {
			results <- struct {
				operation string
				status    int
				err       error
			}{"update", 0, err}
			return
		}

		req, err := http.NewRequest(
			http.MethodPut,
			ts.URL+"/v1/widgets/"+widgetName,
			bytes.NewReader(updatePayload),
		)
		if err != nil {
			results <- struct {
				operation string
				status    int
				err       error
			}{"update", 0, err}
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			results <- struct {
				operation string
				status    int
				err       error
			}{"update", 0, err}
			return
		}
		resp.Body.Close()

		results <- struct {
			operation string
			status    int
			err       error
		}{"update", resp.StatusCode, nil}
	}()

	go func() {
		defer wg.Done()
		<-start

		req, err := http.NewRequest(
			http.MethodDelete,
			ts.URL+"/v1/widgets/"+widgetName,
			nil,
		)
		if err != nil {
			results <- struct {
				operation string
				status    int
				err       error
			}{"delete", 0, err}
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			results <- struct {
				operation string
				status    int
				err       error
			}{"delete", 0, err}
			return
		}
		resp.Body.Close()

		results <- struct {
			operation string
			status    int
			err       error
		}{"delete", resp.StatusCode, nil}
	}()

	close(start)
	wg.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Errorf("%s request failed: %v", result.operation, result.err)
			continue
		}

		switch result.operation {
		case "update":
			if result.status != http.StatusOK &&
				result.status != http.StatusNotFound {
				t.Errorf(
					"Expected update to return HTTP 200 or 404, got HTTP %d",
					result.status,
				)
			}
		case "delete":
			if result.status != http.StatusOK &&
				result.status != http.StatusNoContent {
				t.Errorf(
					"Expected delete to succeed, got HTTP %d",
					result.status,
				)
			}
		}
	}

	// Delete must ultimately leave the resource absent, regardless of
	// whether the update or delete acquired the store lock first.
	resp, err = client.Get(ts.URL + "/v1/widgets/" + widgetName)
	if err != nil {
		t.Fatalf("Failed to check final widget state: %v", err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read final widget response: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf(
			"Expected deleted widget to return HTTP 404, got HTTP %d: %s",
			resp.StatusCode,
			body,
		)
	}
}

func TestConcurrentWidgetUpdates(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()
	widgetName := "concurrent-updates"

	// Create the widget before starting concurrent updates.
	payload, err := json.Marshal(map[string]string{
		"name":    widgetName,
		"profile": "production",
	})
	if err != nil {
		t.Fatalf("Failed to marshal widget: %v", err)
	}

	resp, err := client.Post(
		ts.URL+"/v1/widgets",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("Failed to create widget: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("Failed to read create response: %v", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected successful creation, got HTTP %d: %s", resp.StatusCode, body)
	}

	profiles := []string{"staging", "production"}
	start := make(chan struct{})
	results := make(chan struct {
		profile string
		status  int
		err     error
	}, len(profiles))

	var wg sync.WaitGroup
	wg.Add(len(profiles))

	for _, profile := range profiles {
		profile := profile

		go func() {
			defer wg.Done()
			<-start

			updatePayload, err := json.Marshal(map[string]string{
				"name":    widgetName,
				"profile": profile,
			})
			if err != nil {
				results <- struct {
					profile string
					status  int
					err     error
				}{profile, 0, err}
				return
			}

			req, err := http.NewRequest(
				http.MethodPut,
				ts.URL+"/v1/widgets/"+widgetName,
				bytes.NewReader(updatePayload),
			)
			if err != nil {
				results <- struct {
					profile string
					status  int
					err     error
				}{profile, 0, err}
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				results <- struct {
					profile string
					status  int
					err     error
				}{profile, 0, err}
				return
			}
			resp.Body.Close()

			results <- struct {
				profile string
				status  int
				err     error
			}{profile, resp.StatusCode, nil}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Errorf("Update to profile %q failed: %v", result.profile, result.err)
			continue
		}

		if result.status != http.StatusOK {
			t.Errorf(
				"Expected update to profile %q to return HTTP 200, got HTTP %d",
				result.profile,
				result.status,
			)
		}
	}

	// Both updates should succeed. The final profile should match whichever
	// update was applied last, with no invalid or partially written state.
	resp, err = client.Get(ts.URL + "/v1/widgets/" + widgetName)
	if err != nil {
		t.Fatalf("Failed to get widget after concurrent updates: %v", err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read final widget response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 for final GET, got HTTP %d: %s", resp.StatusCode, body)
	}

	var widget map[string]interface{}
	if err := json.Unmarshal(body, &widget); err != nil {
		t.Fatalf("Failed to decode final widget response: %v", err)
	}

	profile, ok := widget["profile"].(string)
	if !ok {
		t.Fatalf("Expected final widget profile to be a string, got %#v", widget["profile"])
	}
	if profile != "production" && profile != "staging" {
		t.Errorf("Unexpected final profile %q", profile)
	}
	if widget["name"] != widgetName {
		t.Errorf("Expected widget name %q, got %#v", widgetName, widget["name"])
	}
	if widget["managedBy"] != testManagedBy {
		t.Errorf("Expected managedBy %q, got %#v", testManagedBy, widget["managedBy"])
	}
}

func TestWidgetCollection(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	expected := map[string]string{
		"collection-widget-a": "production",
		"collection-widget-b": "staging",
		"collection-widget-c": "production",
	}

	// Create several widgets to populate the collection.
	for name, profile := range expected {
		payload, err := json.Marshal(map[string]string{
			"name":    name,
			"profile": profile,
		})
		if err != nil {
			t.Fatalf("Failed to marshal widget %q: %v", name, err)
		}

		resp, err := client.Post(
			ts.URL+"/v1/widgets",
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			t.Fatalf("Failed to create widget %q: %v", name, err)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatalf("Failed to read create response for %q: %v", name, readErr)
		}

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf(
				"Failed to create widget %q: HTTP %d: %s",
				name,
				resp.StatusCode,
				body,
			)
		}
	}

	// Retrieve the collection.
	resp, err := client.Get(ts.URL + "/v1/widgets")
	if err != nil {
		t.Fatalf("Failed to list widgets: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read collection response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 for collection GET, got HTTP %d: %s", resp.StatusCode, body)
	}

	var collection struct {
		Data []map[string]interface{} `json:"data"`
	}

	if err := json.Unmarshal(body, &collection); err != nil {
		t.Fatalf("Failed to decode collection response: %v\nBody: %s", err, body)
	}

	if len(collection.Data) != len(expected) {
		t.Fatalf(
			"Expected %d widgets in collection, got %d: %s",
			len(expected),
			len(collection.Data),
			body,
		)
	}

	// Index the returned resources by name to validate each one and
	// detect duplicate entries.
	actual := make(map[string]map[string]interface{}, len(collection.Data))

	for _, widget := range collection.Data {
		name, ok := widget["name"].(string)
		if !ok || name == "" {
			t.Errorf("Expected each collection item to have a non-empty name, got %#v", widget["name"])
			continue
		}

		if _, exists := actual[name]; exists {
			t.Errorf("Collection contains duplicate widget %q", name)
			continue
		}

		actual[name] = widget
	}

	for name, expectedProfile := range expected {
		widget, exists := actual[name]
		if !exists {
			t.Errorf("Collection is missing widget %q", name)
			continue
		}

		if widget["profile"] != expectedProfile {
			t.Errorf(
				"Widget %q: expected profile %q, got %#v",
				name,
				expectedProfile,
				widget["profile"],
			)
		}

		if widget["managedBy"] != testManagedBy {
			t.Errorf(
				"Widget %q: expected managedBy %q, got %#v",
				name,
				testManagedBy,
				widget["managedBy"],
			)
		}
	}
}

func TestUnsupportedMethodDoesNotMutateWidget(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()
	widgetName := "unsupported-method-widget"

	// Create a known-good resource.
	payload, err := json.Marshal(map[string]string{
		"name":    widgetName,
		"profile": "production",
	})
	if err != nil {
		t.Fatalf("Failed to marshal widget: %v", err)
	}

	resp, err := client.Post(
		ts.URL+"/v1/widgets",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("Failed to create widget: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("Failed to read create response: %v", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected successful creation, got HTTP %d: %s", resp.StatusCode, body)
	}

	// PATCH isn't registered as an allowed resource method.
	patchPayload := []byte(`{"name":"unsupported-method-widget","profile":"staging"}`)
	req, err := http.NewRequest(
		http.MethodPatch,
		ts.URL+"/v1/widgets/"+widgetName,
		bytes.NewReader(patchPayload),
	)
	if err != nil {
		t.Fatalf("Failed to build PATCH request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("PATCH request failed: %v", err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("Failed to read PATCH response: %v", err)
	}

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"Expected PATCH to return HTTP 403, got HTTP %d: %s",
			resp.StatusCode,
			body,
		)
	}

	var apiErr map[string]interface{}
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("Failed to decode PATCH error response: %v", err)
	}

	if apiErr["code"] != "PermissionDenied" {
		t.Errorf("Expected error code PermissionDenied, got %#v", apiErr["code"])
	}

	if apiErr["message"] != "Method PATCH not supported" {
		t.Errorf("Unexpected error message: %#v", apiErr["message"])
	}

	// Verify the rejected request didn't change the stored resource.
	resp, err = client.Get(ts.URL + "/v1/widgets/" + widgetName)
	if err != nil {
		t.Fatalf("Failed to retrieve widget: %v", err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read widget response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected GET to return HTTP 200, got HTTP %d: %s", resp.StatusCode, body)
	}

	var widget map[string]interface{}
	if err := json.Unmarshal(body, &widget); err != nil {
		t.Fatalf("Failed to decode widget response: %v", err)
	}

	if widget["profile"] != "production" {
		t.Errorf("Expected profile to remain production, got %#v", widget["profile"])
	}
	if widget["name"] != widgetName {
		t.Errorf("Expected widget name %q, got %#v", widgetName, widget["name"])
	}
	if widget["managedBy"] != testManagedBy {
		t.Errorf("Expected managedBy %q, got %#v", testManagedBy, widget["managedBy"])
	}
}

func TestEmptyWidgetCollection(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + testWidgetEndpoint)
	if err != nil {
		t.Fatalf("Failed to list widgets: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read collection response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got HTTP %d: %s", resp.StatusCode, body)
	}

	var collection struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &collection); err != nil {
		t.Fatalf("Failed to decode collection response: %v; body: %s", err, body)
	}

	if len(collection.Data) != 0 {
		t.Errorf("Expected an empty collection, got %d widgets: %s",
			len(collection.Data), body)
	}
}

func TestWidgetCollectionIgnoresUnknownQueryParameters(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	expectedNames := map[string]bool{
		"query-widget-a": false,
		"query-widget-b": false,
	}

	for name := range expectedNames {
		resp := postJSON(t, client, ts.URL+testWidgetEndpoint, map[string]string{
			"name":    name,
			"profile": "production",
		})
		body := readBody(t, resp)

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("Failed to create widget %q: HTTP %d: %s",
				name, resp.StatusCode, body)
		}
	}

	resp, err := client.Get(ts.URL + testWidgetEndpoint + "?unrecognized=ignored")
	if err != nil {
		t.Fatalf("Failed to list widgets: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read collection response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got HTTP %d: %s", resp.StatusCode, body)
	}

	var collection struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &collection); err != nil {
		t.Fatalf("Failed to decode collection response: %v; body: %s", err, body)
	}

	if len(collection.Data) != len(expectedNames) {
		t.Fatalf("Expected %d widgets, got %d: %s",
			len(expectedNames), len(collection.Data), body)
	}

	for _, widget := range collection.Data {
		if _, exists := expectedNames[widget.Name]; !exists {
			t.Errorf("Unexpected widget in collection: %q", widget.Name)
			continue
		}
		if expectedNames[widget.Name] {
			t.Errorf("Duplicate widget in collection: %q", widget.Name)
			continue
		}
		expectedNames[widget.Name] = true
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("Collection is missing widget %q", name)
		}
	}
}

func TestGetNonexistentWidget(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + testWidgetEndpoint + "/does-not-exist")
	if err != nil {
		t.Fatalf("failed to GET nonexistent widget: %v", err)
	}

	body := readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected HTTP 404, got HTTP %d: %s", resp.StatusCode, body)
	}

	var apiErr map[string]interface{}
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("expected JSON error response: %v; body: %s", err, body)
	}

	if apiErr["type"] != "error" {
		t.Errorf("expected error response type %q, got %#v", "error", apiErr["type"])
	}
}
