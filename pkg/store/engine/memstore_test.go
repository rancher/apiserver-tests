package engine

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rancher/apiserver/pkg/types"
)

func TestBroadcastDoesNotBlockOnFullSubscriber(t *testing.T) {
	store := NewMemoryStore()

	// Simulate a slow subscriber whose buffer is already full.
	slowSubscriber := make(chan types.APIEvent, 1)
	slowSubscriber <- types.APIEvent{Name: "existing"}

	// Simulate a healthy subscriber that can receive the next event.
	healthySubscriber := make(chan types.APIEvent, 1)

	store.watchers[1] = slowSubscriber
	store.watchers[2] = healthySubscriber

	event := types.APIEvent{Name: "change"}

	// Broadcast asynchronously so a regression that blocks can be detected.
	done := make(chan struct{})
	go func() {
		store.broadcast(event)
		close(done)
	}()

	select {
	case <-done:
		// Expected: broadcasting completed without blocking.
	case <-time.After(time.Second):
		t.Fatal("broadcast blocked because a subscriber channel was full")
	}

	// The healthy subscriber must receive the new event.
	select {
	case received := <-healthySubscriber:
		if received.Name != "change" {
			t.Fatalf("expected event name %q, got %q",
				"change", received.Name)
		}
	default:
		t.Fatal("healthy subscriber did not receive the event")
	}

	// The slow subscriber's existing buffered event must remain untouched.
	select {
	case received := <-slowSubscriber:
		if received.Name != "existing" {
			t.Fatalf("expected existing buffered event %q, got %q",
				"existing", received.Name)
		}
	default:
		t.Fatal("slow subscriber's existing event was unexpectedly missing")
	}

	// The full channel should not have received the new event.
	select {
	case received := <-slowSubscriber:
		t.Fatalf("expected the new event to be dropped for the slow subscriber, got %q",
			received.Name)
	default:
		// Expected: the new event was dropped for the full channel.
	}
}

func TestWatchCleanupOnContextCancellation(t *testing.T) {
	store := NewMemoryStore()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		cancel()
		t.Fatalf("create HTTP request: %v", err)
	}

	apiOp := &types.APIRequest{Request: req}

	ch, err := store.Watch(apiOp, nil, types.WatchRequest{})
	if err != nil {
		cancel()
		t.Fatalf("failed to create watch: %v", err)
	}
	if ch == nil {
		cancel()
		t.Fatal("expected a non-nil watch channel")
	}

	// Confirm the watcher was registered.
	store.mu.RLock()
	initialCount := len(store.watchers)
	store.mu.RUnlock()

	if initialCount != 1 {
		cancel()
		t.Fatalf("expected 1 registered watcher, got %d", initialCount)
	}

	// Cancel the request context to trigger watcher cleanup.
	cancel()

	// Wait for the cleanup goroutine to remove the watcher.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.RLock()
		count := len(store.watchers)
		store.mu.RUnlock()

		if count == 0 {
			break
		}

		time.Sleep(time.Millisecond)
	}

	store.mu.RLock()
	remaining := len(store.watchers)
	store.mu.RUnlock()

	if remaining != 0 {
		t.Fatalf(
			"expected watcher cleanup after context cancellation; %d watcher(s) remain",
			remaining,
		)
	}

	// The cleanup goroutine should also close the watch channel.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected watch channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch channel to close")
	}
}

func TestRepeatedWatchCleanupDoesNotLeakWatchers(t *testing.T) {
	store := NewMemoryStore()

	const iterations = 100

	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
		if err != nil {
			cancel()
			t.Fatalf("iteration %d: create HTTP request: %v", i, err)
		}

		apiOp := &types.APIRequest{Request: req}

		ch, err := store.Watch(apiOp, nil, types.WatchRequest{})
		if err != nil {
			cancel()
			t.Fatalf("iteration %d: failed to create watch: %v", i, err)
		}
		if ch == nil {
			cancel()
			t.Fatalf("iteration %d: expected a non-nil watch channel", i)
		}

		store.mu.RLock()
		count := len(store.watchers)
		store.mu.RUnlock()

		if count != 1 {
			cancel()
			t.Fatalf(
				"iteration %d: expected 1 watcher before cancellation, got %d",
				i, count,
			)
		}

		// Cancel immediately; do not defer cancellation inside the loop.
		cancel()

		// Wait for this iteration's watcher to be removed.
		deadline := time.Now().Add(time.Second)
		cleaned := false

		for time.Now().Before(deadline) {
			store.mu.RLock()
			count = len(store.watchers)
			store.mu.RUnlock()

			if count == 0 {
				cleaned = true
				break
			}

			time.Sleep(time.Millisecond)
		}

		if !cleaned {
			store.mu.RLock()
			remaining := len(store.watchers)
			store.mu.RUnlock()

			t.Fatalf(
				"iteration %d: watcher cleanup timed out; %d watcher(s) remain",
				i, remaining,
			)
		}

		// Verify the channel was closed as part of cleanup.
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("iteration %d: expected watch channel to be closed", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: timed out waiting for watch channel to close", i)
		}
	}

	store.mu.RLock()
	finalCount := len(store.watchers)
	store.mu.RUnlock()

	if finalCount != 0 {
		t.Fatalf(
			"expected no watchers after %d iterations, got %d",
			iterations, finalCount,
		)
	}
}
