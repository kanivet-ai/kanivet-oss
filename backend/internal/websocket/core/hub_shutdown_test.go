package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHubRejectsConnectionAfterShutdown(t *testing.T) {
	hub := NewHub(nil, newMockRouter(), newMockSubscriptionManager())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	raw, cleanup := stalledPair(t)
	defer cleanup()
	conn := NewConnection("late", raw, nil)
	defer conn.Close()

	if err := hub.RegisterConnection(conn); !errors.Is(err, context.Canceled) {
		t.Fatalf("RegisterConnection after shutdown = %v, want context.Canceled", err)
	}
	if got := hub.CountConnections(); got != 0 {
		t.Fatalf("shut-down hub has %d connections, want 0", got)
	}
}

// A completed HTTP upgrade does not mean the connection is registered yet.
// Race registration against shutdown without waiting for the hub's count.
func TestHubRegistrationRacesWithShutdown(t *testing.T) {
	for i := 0; i < 25; i++ {
		t.Run("", func(t *testing.T) {
			hub := NewHub(nil, newMockRouter(), newMockSubscriptionManager())
			raw, cleanup := stalledPair(t)
			defer cleanup()
			conn := NewConnection("racing", raw, nil)
			defer conn.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := make(chan struct{})
			registered := make(chan error, 1)
			stopped := make(chan error, 1)
			go func() {
				<-start
				registered <- hub.RegisterConnection(conn)
			}()
			go func() {
				<-start
				stopped <- hub.Shutdown(ctx)
			}()
			close(start)

			var registrationErr error
			select {
			case registrationErr = <-registered:
			case <-ctx.Done():
				t.Fatal("registration did not finish")
			}
			if registrationErr != nil && !errors.Is(registrationErr, context.Canceled) {
				t.Fatalf("RegisterConnection = %v", registrationErr)
			}
			select {
			case err := <-stopped:
				if err != nil {
					t.Fatalf("Shutdown = %v", err)
				}
			case <-ctx.Done():
				t.Fatal("shutdown did not finish")
			}
			if registrationErr == nil && conn.State() != StateClosed {
				t.Error("shutdown returned before the accepted connection closed")
			}
			if got := hub.CountConnections(); got != 0 {
				t.Fatalf("shutdown left %d connections registered", got)
			}
		})
	}
}
