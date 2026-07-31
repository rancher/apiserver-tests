package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rancher/apiserver-tests/pkg/server"
	"github.com/gorilla/websocket"
)

func TestStreamingEventSubscription(t *testing.T) {
	handler, _ := server.BuildAPIHandler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Dial up WebSocket subscribe controller target at /v1/subscribe
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/subscribe"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket link initialization anomalies: %v", err)
	}
	defer ws.Close()

	// Issue subscriber registration frames
	subPayload := map[string]string{"resourceType": "widget"}
	subBytes, _ := json.Marshal(subPayload)
	if err := ws.WriteMessage(websocket.TextMessage, subBytes); err != nil {
		t.Fatalf("Failed to register subscribe message: %v", err)
	}

	// Helper to read frames
	readEvent := func() (map[string]interface{}, error) {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		var parsedEvt map[string]interface{}
		json.Unmarshal(msg, &parsedEvt)
		return parsedEvt, nil
	}

	// Consume the initial synchronization frames (resource.start)
	// apiserver always sends a 'resource.start' frame upon subscription
	for {
		evt, err := readEvent()
		if err != nil {
			t.Fatalf("Connection dropped during sync: %v", err)
		}
		if evt["name"] == "resource.start" {
			continue
		}
		if evt["name"] == "change" {
			t.Fatalf("Got change event too early: %v", evt)
		}
		break
	}

	// Simulate backend storage mutation
	time.Sleep(100 * time.Millisecond)
	mutationPayload, _ := json.Marshal(map[string]string{"name": "stream-widget", "profile": "production"})
	_, err = http.Post(ts.URL+"/v1/widgets", "application/json", bytes.NewReader(mutationPayload))
	if err != nil {
		t.Fatalf("Failed to execute data modification trigger: %v", err)
	}

	// Assert the actual 'change' event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for real-time WebSocket subscriber events")
		default:
			evt, err := readEvent()
			if err != nil {
				t.Fatalf("Error reading event: %v", err)
			}
			if evt["name"] == "change" {
				t.Logf("Successfully captured real-time event frame: %v", evt)
				return
			}
		}
	}
}