package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rancher/apiserver-tests/pkg/server"
	"github.com/rancher/apiserver/pkg/apierror"
	apiserver "github.com/rancher/apiserver/pkg/server"
	"github.com/rancher/apiserver/pkg/types"
	"github.com/rancher/wrangler/v3/pkg/schemas/validation"
)

type selectiveAccessControl struct {
	denyMethod string
	delegate   types.AccessControl
}

func newSelectiveAccessControl() *selectiveAccessControl {
	return &selectiveAccessControl{
		delegate: &apiserver.SchemaBasedAccess{},
	}
}

func (a *selectiveAccessControl) check(method string) error {
	if a.denyMethod == method {
		return apierror.NewAPIError(
			validation.PermissionDenied,
			"access denied by integration test",
		)
	}
	return nil
}

func (a *selectiveAccessControl) CanAction(
	req *types.APIRequest,
	schema *types.APISchema,
	name string,
) error {
	return a.delegate.CanAction(req, schema, name)
}

func (a *selectiveAccessControl) CanCreate(
	req *types.APIRequest,
	schema *types.APISchema,
) error {
	if err := a.check(http.MethodPost); err != nil {
		return err
	}
	return a.delegate.CanCreate(req, schema)
}

func (a *selectiveAccessControl) CanList(
	req *types.APIRequest,
	schema *types.APISchema,
) error {
	return a.delegate.CanList(req, schema)
}

func (a *selectiveAccessControl) CanGet(
	req *types.APIRequest,
	schema *types.APISchema,
) error {
	return a.delegate.CanGet(req, schema)
}

func (a *selectiveAccessControl) CanUpdate(
	req *types.APIRequest,
	obj types.APIObject,
	schema *types.APISchema,
) error {
	if err := a.check(http.MethodPut); err != nil {
		return err
	}
	return a.delegate.CanUpdate(req, obj, schema)
}

func (a *selectiveAccessControl) CanDelete(
	req *types.APIRequest,
	obj types.APIObject,
	schema *types.APISchema,
) error {
	if err := a.check(http.MethodDelete); err != nil {
		return err
	}
	return a.delegate.CanDelete(req, obj, schema)
}

func (a *selectiveAccessControl) CanWatch(
	req *types.APIRequest,
	schema *types.APISchema,
) error {
	return a.delegate.CanWatch(req, schema)
}

func (a *selectiveAccessControl) CanDo(
	req *types.APIRequest,
	resource, verb, namespace, name string,
) error {
	if err := a.check(verb); err != nil {
		return err
	}
	return a.delegate.CanDo(req, resource, verb, namespace, name)
}

func (a *selectiveAccessControl) CanPatch(
	req *types.APIRequest,
	obj types.APIObject,
	schema *types.APISchema,
) error {
	if err := a.check(http.MethodPatch); err != nil {
		return err
	}
	return a.delegate.CanPatch(req, obj, schema)
}

func TestAccessControlDeniesCollectionOperations(t *testing.T) {
	policy := newSelectiveAccessControl()
	policy.denyMethod = http.MethodPost

	handler, err := server.BuildAPIHandlerWithAccessControl(policy)
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	client := ts.Client()

	t.Run("list allowed when create is denied", func(t *testing.T) {
		resp, err := client.Get(ts.URL + testWidgetEndpoint)
		if err != nil {
			t.Fatalf("GET collection request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Expected HTTP 200, got HTTP %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("create denied without creating widget", func(t *testing.T) {
		const name = "access-denied-create-widget"

		resp := postJSON(t, client, ts.URL+testWidgetEndpoint, map[string]string{
			"name":    name,
			"profile": "production",
		})
		assertPermissionDenied(t, resp)

		// GET is allowed by this policy, so we can verify the resource
		// was not created as a side effect of the denied POST.
		assertWidgetNotFound(t, client, ts.URL, name)
	})
}

func TestAccessControlDeniesResourceMutations(t *testing.T) {
	policy := newSelectiveAccessControl()

	handler, err := server.BuildAPIHandlerWithAccessControl(policy)
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	client := ts.Client()
	const name = "access-denied-resource-widget"
	widgetURL := ts.URL + testWidgetEndpoint + "/" + name

	// Seed the widget while all operations are allowed.
	resp := postJSON(t, client, ts.URL+testWidgetEndpoint, map[string]string{
		"name":    name,
		"profile": "production",
	})
	body := readBody(t, resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("Failed to seed widget: HTTP %d: %s", resp.StatusCode, body)
	}

	t.Run("update denied without changing widget", func(t *testing.T) {
		policy.denyMethod = http.MethodPut

		resp, body := doJSONRequest(t, client, http.MethodPut, widgetURL, map[string]string{
			"name":    name,
			"profile": "staging",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Expected HTTP 403, got HTTP %d: %s", resp.StatusCode, body)
		}
		assertPermissionDeniedBody(t, body)

		// Clear the denial before checking the stored resource.
		policy.denyMethod = ""

		resp, err := client.Get(widgetURL)
		if err != nil {
			t.Fatalf("GET widget request failed: %v", err)
		}
		defer resp.Body.Close()

		got := readResponseWidget(t, resp)
		if got.Profile != "production" {
			t.Errorf("Expected profile to remain production after denied PUT, got %q", got.Profile)
		}
	})

	t.Run("delete denied without deleting widget", func(t *testing.T) {
		policy.denyMethod = http.MethodDelete

		resp, body := doJSONRequest(t, client, http.MethodDelete, widgetURL, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Expected HTTP 403, got HTTP %d: %s", resp.StatusCode, body)
		}
		assertPermissionDeniedBody(t, body)

		// Clear the denial before checking the stored resource.
		policy.denyMethod = ""

		resp, err := client.Get(widgetURL)
		if err != nil {
			t.Fatalf("GET widget request failed: %v", err)
		}
		defer resp.Body.Close()

		got := readResponseWidget(t, resp)
		if got.Name != name {
			t.Errorf("Expected widget %q to still exist, got %q", name, got.Name)
		}
	})
}

func assertPermissionDenied(t *testing.T, resp *http.Response) {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("Failed to read error response: %v", err)
	}

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected HTTP 403, got HTTP %d: %s", resp.StatusCode, body)
	}

	assertPermissionDeniedBody(t, body)
}

func assertPermissionDeniedBody(t *testing.T, body []byte) {
	t.Helper()

	var apiErr map[string]interface{}
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("Expected JSON error response: %v; body: %s", err, body)
	}

	if apiErr["code"] != "PermissionDenied" {
		t.Errorf("Expected error code PermissionDenied, got %#v", apiErr["code"])
	}
}

func doJSONRequest(
	t *testing.T,
	client *http.Client,
	method, url string,
	payload map[string]string,
) (*http.Response, []byte) {
	t.Helper()

	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("Failed to encode request: %v", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s request failed: %v", method, err)
	}

	responseBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Restore the response body so callers can use normal response helpers.
	resp.Body = io.NopCloser(bytes.NewReader(responseBody))

	return resp, responseBody
}

func readResponseWidget(t *testing.T, resp *http.Response) WidgetResponse {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read widget response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got HTTP %d: %s", resp.StatusCode, body)
	}

	var widget WidgetResponse
	if err := json.Unmarshal(body, &widget); err != nil {
		t.Fatalf("Failed to decode widget response: %v; body: %s", err, body)
	}

	return widget
}

type WidgetResponse struct {
	Name    string `json:"name"`
	Profile string `json:"profile"`
}
