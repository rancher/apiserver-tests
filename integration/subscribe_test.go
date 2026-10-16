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
	"time"

	"github.com/gorilla/websocket"
	"github.com/rancher/apiserver-tests/pkg/server"
)

func TestStreamingEventSubscription(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/subscribe"

	ws, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("Failed to connect to WebSocket: %v (HTTP status %d)", err, response.StatusCode)
		}
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	readEvent := func(timeout time.Duration) (map[string]interface{}, error) {
		if err := ws.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return nil, fmt.Errorf("set WebSocket read deadline: %w", err)
		}

		_, msg, err := ws.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read WebSocket message: %w", err)
		}

		var event map[string]interface{}
		if err := json.Unmarshal(msg, &event); err != nil {
			return nil, fmt.Errorf("decode WebSocket event %q: %w", msg, err)
		}

		return event, nil
	}

	// Register the widget subscription.
	subPayload, err := json.Marshal(map[string]string{
		"resourceType": "widget",
	})
	if err != nil {
		t.Fatalf("Failed to encode subscription payload: %v", err)
	}

	if err := ws.WriteMessage(websocket.TextMessage, subPayload); err != nil {
		t.Fatalf("Failed to register subscription: %v", err)
	}

	// Wait for the server's initial synchronization signal.
	// This replaces the arbitrary sleep previously used to guess
	// when the subscription was ready.
	syncDeadline := time.Now().Add(5 * time.Second)
	syncReceived := false

	for time.Now().Before(syncDeadline) {
		remaining := time.Until(syncDeadline)
		event, err := readEvent(remaining)
		if err != nil {
			t.Fatalf("Failed waiting for subscription synchronization: %v", err)
		}

		if event["name"] == "resource.start" {
			syncReceived = true
			break
		}
	}

	if !syncReceived {
		t.Fatal("Timed out waiting for resource.start subscription event")
	}

	// Trigger a real storage mutation through the HTTP API.
	mutationPayload, err := json.Marshal(map[string]string{
		"name":    "stream-widget",
		"profile": "production",
	})
	if err != nil {
		t.Fatalf("Failed to encode widget payload: %v", err)
	}

	resp, err := http.Post(
		ts.URL+"/v1/widgets",
		"application/json",
		bytes.NewReader(mutationPayload),
	)
	if err != nil {
		t.Fatalf("Failed to create widget: %v", err)
	}

	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if readErr != nil {
		t.Fatalf("Failed to read widget creation response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("Failed to close widget creation response: %v", closeErr)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("Widget creation failed: HTTP %d: %s", resp.StatusCode, body)
	}

	// ReadMessage is bounded by the deadline, so a missing event
	// fails instead of hanging the test indefinitely.
	eventDeadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(eventDeadline) {
		event, err := readEvent(time.Until(eventDeadline))
		if err != nil {
			t.Fatalf("Failed waiting for widget change event: %v", err)
		}

		if event["name"] != "change" {
			continue
		}

		t.Logf("Received WebSocket change event: %v", event)
		return
	}

	t.Fatal("Timed out waiting for the widget change event")
}

func TestMultipleSubscribersReceiveWidgetCreation(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("Failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	widgetName := "multi-subscriber-widget"

	type subscriber struct {
		conn      *websocket.Conn
		readEvent func(time.Duration) (map[string]interface{}, error)
	}

	var subscribers []subscriber

	// Connect two independent subscribers and wait for each to synchronize.
	for i := 0; i < 2; i++ {
		wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/subscribe"

		ws, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			if response != nil {
				t.Fatalf(
					"Subscriber %d failed to connect: %v (HTTP %d)",
					i+1, err, response.StatusCode,
				)
			}
			t.Fatalf("Subscriber %d failed to connect: %v", i+1, err)
		}

		t.Cleanup(func() {
			_ = ws.Close()
		})

		readEvent := func(timeout time.Duration) (map[string]interface{}, error) {
			if err := ws.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				return nil, fmt.Errorf("set read deadline: %w", err)
			}

			_, message, err := ws.ReadMessage()
			if err != nil {
				return nil, fmt.Errorf("read WebSocket message: %w", err)
			}

			var event map[string]interface{}
			if err := json.Unmarshal(message, &event); err != nil {
				return nil, fmt.Errorf("decode WebSocket event: %w", err)
			}

			return event, nil
		}

		payload, err := json.Marshal(map[string]string{
			"resourceType": "widget",
		})
		if err != nil {
			t.Fatalf("Failed to encode subscription payload: %v", err)
		}

		if err := ws.WriteMessage(websocket.TextMessage, payload); err != nil {
			t.Fatalf("Subscriber %d failed to subscribe: %v", i+1, err)
		}

		syncDeadline := time.Now().Add(5 * time.Second)
		synced := false

		for time.Now().Before(syncDeadline) {
			event, err := readEvent(time.Until(syncDeadline))
			if err != nil {
				t.Fatalf(
					"Subscriber %d failed waiting for synchronization: %v",
					i+1, err,
				)
			}

			if event["name"] == "resource.start" {
				synced = true
				break
			}
		}

		if !synced {
			t.Fatalf("Subscriber %d timed out waiting for resource.start", i+1)
		}

		subscribers = append(subscribers, subscriber{
			conn:      ws,
			readEvent: readEvent,
		})
	}

	// Create one widget after both subscriptions are ready.
	payload, err := json.Marshal(map[string]string{
		"name":    widgetName,
		"profile": "production",
	})
	if err != nil {
		t.Fatalf("Failed to encode widget payload: %v", err)
	}

	resp, err := http.Post(
		ts.URL+"/v1/widgets",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("Failed to create widget: %v", err)
	}

	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if readErr != nil {
		t.Fatalf("Failed to read creation response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("Failed to close creation response: %v", closeErr)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("Widget creation failed: HTTP %d: %s", resp.StatusCode, body)
	}

	// Each subscriber must receive a change event referencing this widget.
	for i, sub := range subscribers {
		t.Run(fmt.Sprintf("subscriber_%d", i+1), func(t *testing.T) {
			deadline := time.Now().Add(3 * time.Second)

			for time.Now().Before(deadline) {
				event, err := sub.readEvent(time.Until(deadline))
				if err != nil {
					t.Fatalf("Failed waiting for widget change event: %v", err)
				}

				if event["name"] != "change" {
					continue
				}

				encoded, err := json.Marshal(event)
				if err != nil {
					t.Fatalf("Failed to encode received event: %v", err)
				}

				if !strings.Contains(string(encoded), widgetName) {
					continue
				}

				t.Logf("Subscriber %d received widget event: %s", i+1, encoded)
				return
			}

			t.Fatalf(
				"Subscriber %d did not receive a change event for widget %q",
				i+1, widgetName,
			)
		})
	}
}

func TestMultipleSubscribersReceiveWidgetDeletion(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/subscribe"

	type subscriber struct {
		conn      *websocket.Conn
		readEvent func(time.Duration) (map[string]interface{}, error)
	}

	subscribers := make([]subscriber, 0, 2)

	for i := 0; i < 2; i++ {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			if resp != nil {
				t.Fatalf("subscriber %d failed to connect: %v (HTTP %d)",
					i+1, err, resp.StatusCode)
			}
			t.Fatalf("subscriber %d failed to connect: %v", i+1, err)
		}
		t.Cleanup(func() {
			_ = conn.Close()
		})

		readEvent := func(timeout time.Duration) (map[string]interface{}, error) {
			if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				return nil, err
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				return nil, err
			}

			var event map[string]interface{}
			if err := json.Unmarshal(message, &event); err != nil {
				return nil, fmt.Errorf("failed to decode event: %w", err)
			}

			return event, nil
		}

		subscription := map[string]interface{}{
			"resourceType": "widget",
		}
		if err := conn.WriteJSON(subscription); err != nil {
			t.Fatalf("subscriber %d failed to subscribe: %v", i+1, err)
		}

		// Wait for the subscription to become active before proceeding.
		ready := false
		deadline := time.Now().Add(5 * time.Second)

		for time.Now().Before(deadline) {
			event, err := readEvent(time.Until(deadline))
			if err != nil {
				t.Fatalf("subscriber %d failed waiting for resource.start: %v",
					i+1, err)
			}

			if event["name"] == "resource.start" {
				ready = true
				break
			}
		}

		if !ready {
			t.Fatalf("subscriber %d did not receive resource.start", i+1)
		}

		subscribers = append(subscribers, subscriber{
			conn:      conn,
			readEvent: readEvent,
		})
	}

	// Create the widget after both subscribers are ready.
	widgetName := "multi-subscriber-delete-widget"
	payload := map[string]string{
		"name":    widgetName,
		"profile": "production",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal widget: %v", err)
	}

	createResp, err := http.Post(
		ts.URL+"/v1/widgets",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("failed to create widget: %v", err)
	}
	createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated &&
		createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected create status 201 or 200, got %d",
			createResp.StatusCode)
	}

	// Delete the widget through the API.
	req, err := http.NewRequest(
		http.MethodDelete,
		ts.URL+"/v1/widgets/"+widgetName,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to create delete request: %v", err)
	}

	deleteResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to delete widget: %v", err)
	}
	deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK &&
		deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected delete status 200 or 204, got %d",
			deleteResp.StatusCode)
	}

	// Each subscriber may still have the earlier creation event queued.
	// Ignore unrelated events until the matching remove event arrives.
	for i, sub := range subscribers {
		deadline := time.Now().Add(3 * time.Second)
		received := false

		for time.Now().Before(deadline) {
			event, err := sub.readEvent(time.Until(deadline))
			if err != nil {
				t.Fatalf("subscriber %d failed waiting for remove event: %v",
					i+1, err)
			}

			if event["name"] != "remove" ||
				event["resourceType"] != "widget" {
				continue
			}

			data, ok := event["data"].(map[string]interface{})
			if !ok {
				t.Fatalf("subscriber %d received remove event without valid data: %#v",
					i+1, event)
			}

			if data["id"] != widgetName {
				continue
			}

			received = true
			t.Logf("subscriber %d received remove event for %q", i+1, widgetName)
			break
		}

		if !received {
			t.Fatalf("subscriber %d did not receive remove event for widget %q",
				i+1, widgetName)
		}
	}

	// Confirm the widget is no longer accessible.
	getResp, err := http.Get(ts.URL + "/v1/widgets/" + widgetName)
	if err != nil {
		t.Fatalf("failed to verify widget deletion: %v", err)
	}
	getResp.Body.Close()

	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected GET after deletion to return 404, got %d",
			getResp.StatusCode)
	}
}

func TestSubscriberDisconnectDoesNotAffectOtherSubscribers(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/subscribe"

	type subscriber struct {
		conn      *websocket.Conn
		readEvent func(time.Duration) (map[string]interface{}, error)
	}

	subscribers := make([]subscriber, 0, 2)

	// Connect both subscribers and wait for their subscriptions to become active.
	for i := 0; i < 2; i++ {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			if resp != nil {
				t.Fatalf("subscriber %d failed to connect: %v (HTTP %d)",
					i+1, err, resp.StatusCode)
			}
			t.Fatalf("subscriber %d failed to connect: %v", i+1, err)
		}

		t.Cleanup(func() {
			_ = conn.Close()
		})

		readEvent := func(timeout time.Duration) (map[string]interface{}, error) {
			if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				return nil, err
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				return nil, err
			}

			var event map[string]interface{}
			if err := json.Unmarshal(message, &event); err != nil {
				return nil, fmt.Errorf("failed to decode event: %w", err)
			}

			return event, nil
		}

		if err := conn.WriteJSON(map[string]interface{}{
			"resourceType": "widget",
		}); err != nil {
			t.Fatalf("subscriber %d failed to subscribe: %v", i+1, err)
		}

		deadline := time.Now().Add(5 * time.Second)
		ready := false

		for time.Now().Before(deadline) {
			event, err := readEvent(time.Until(deadline))
			if err != nil {
				t.Fatalf("subscriber %d failed waiting for resource.start: %v",
					i+1, err)
			}

			if event["name"] == "resource.start" {
				ready = true
				break
			}
		}

		if !ready {
			t.Fatalf("subscriber %d did not receive resource.start", i+1)
		}

		subscribers = append(subscribers, subscriber{
			conn:      conn,
			readEvent: readEvent,
		})
	}

	// Disconnect subscriber 1.
	if err := subscribers[0].conn.Close(); err != nil {
		t.Fatalf("failed to close subscriber 1: %v", err)
	}
	t.Log("subscriber 1 disconnected")

	// Create a widget after subscriber 1 disconnects.
	widgetName := "remaining-subscriber-widget"
	payload := map[string]string{
		"name":    widgetName,
		"profile": "production",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal widget: %v", err)
	}

	createResp, err := http.Post(
		ts.URL+"/v1/widgets",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("failed to create widget: %v", err)
	}
	createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated &&
		createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected create status 201 or 200, got %d",
			createResp.StatusCode)
	}

	// Subscriber 2 must still receive the creation event.
	deadline := time.Now().Add(3 * time.Second)
	received := false

	for time.Now().Before(deadline) {
		event, err := subscribers[1].readEvent(time.Until(deadline))
		if err != nil {
			t.Fatalf("subscriber 2 failed waiting for change event: %v", err)
		}

		if event["name"] != "change" ||
			event["resourceType"] != "widget" {
			continue
		}

		data, ok := event["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("received change event without valid data: %#v", event)
		}

		if data["id"] != widgetName {
			continue
		}

		if data["profile"] != "production" {
			t.Fatalf("expected profile production, got %#v", data["profile"])
		}

		if data["managedBy"] != testManagedBy {
			t.Fatalf("expected managedBy %q, got %#v",
				testManagedBy, data["managedBy"])
		}

		received = true
		t.Logf("subscriber 2 received change event for %q after subscriber 1 disconnected",
			widgetName)
		break
	}

	if !received {
		t.Fatalf("subscriber 2 did not receive change event for %q after subscriber 1 disconnected",
			widgetName)
	}
}

func TestSubscriberCanReconnectAndReceiveEvents(t *testing.T) {
	handler, err := server.BuildAPIHandler()
	if err != nil {
		t.Fatalf("failed to build API handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/subscribe"

	connectAndSubscribe := func() (*websocket.Conn, func(time.Duration) (map[string]interface{}, error)) {
		t.Helper()

		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			if resp != nil {
				t.Fatalf("failed to connect WebSocket: %v (HTTP %d)",
					err, resp.StatusCode)
			}
			t.Fatalf("failed to connect WebSocket: %v", err)
		}

		readEvent := func(timeout time.Duration) (map[string]interface{}, error) {
			if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				return nil, fmt.Errorf("set read deadline: %w", err)
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				return nil, fmt.Errorf("read WebSocket message: %w", err)
			}

			var event map[string]interface{}
			if err := json.Unmarshal(message, &event); err != nil {
				return nil, fmt.Errorf("decode WebSocket event: %w", err)
			}

			return event, nil
		}

		if err := conn.WriteJSON(map[string]string{
			"resourceType": "widget",
		}); err != nil {
			_ = conn.Close()
			t.Fatalf("failed to register subscription: %v", err)
		}

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			event, err := readEvent(time.Until(deadline))
			if err != nil {
				_ = conn.Close()
				t.Fatalf("failed waiting for subscription synchronization: %v", err)
			}

			if event["name"] == "resource.start" {
				return conn, readEvent
			}
		}

		_ = conn.Close()
		t.Fatal("timed out waiting for resource.start")
		return nil, nil
	}

	// Establish and then close the initial subscription.
	firstConn, _ := connectAndSubscribe()
	if err := firstConn.Close(); err != nil {
		t.Fatalf("failed to close initial subscription: %v", err)
	}

	// A fresh connection should be able to subscribe independently.
	reconnected, readEvent := connectAndSubscribe()
	defer reconnected.Close()

	const widgetName = "reconnected-subscriber-widget"

	payload, err := json.Marshal(map[string]string{
		"name":    widgetName,
		"profile": "production",
	})
	if err != nil {
		t.Fatalf("failed to encode widget payload: %v", err)
	}

	resp, err := http.Post(
		ts.URL+testWidgetEndpoint,
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("failed to create widget: %v", err)
	}

	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		t.Fatalf("failed to read creation response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("failed to close creation response: %v", closeErr)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to create widget: HTTP %d: %s", resp.StatusCode, body)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		event, err := readEvent(time.Until(deadline))
		if err != nil {
			t.Fatalf("failed waiting for widget event after reconnect: %v", err)
		}

		if event["name"] != "change" || event["resourceType"] != "widget" {
			continue
		}

		data, ok := event["data"].(map[string]interface{})
		if !ok {
			continue
		}

		if data["id"] == widgetName {
			t.Logf("reconnected subscriber received change event for %q", widgetName)
			return
		}
	}

	t.Fatalf("reconnected subscriber did not receive change event for widget %q", widgetName)
}
