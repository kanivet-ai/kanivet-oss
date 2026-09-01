package watcher

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

func TestProcessWatchEventsReturnsCredentialError(t *testing.T) {
	fw := watch.NewFake()
	svc := &Service{}

	done := make(chan struct{})
	var expired bool
	var err error
	go func() {
		expired, err = svc.processWatchEvents(context.Background(), fw, schema.GroupVersionResource{}, "topic", new(string))
		close(done)
	}()

	fw.Error(&metav1.Status{
		Code:    401,
		Reason:  metav1.StatusReasonUnauthorized,
		Message: "the server has asked for the client to provide credentials",
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch error")
	}
	if expired {
		t.Fatalf("expected credential watch error not to be treated as RV expiration")
	}
	if err == nil {
		t.Fatalf("expected credential watch error")
	}
	if ok, code, _ := isCredentialError(err); !ok || code != "unauthorized" {
		t.Fatalf("expected unauthorized credential error, got ok=%v code=%q err=%v", ok, code, err)
	}
}

func TestProcessWatchEventsReturnsExpiredForGoneStatus(t *testing.T) {
	fw := watch.NewFake()
	svc := &Service{}

	done := make(chan struct{})
	var expired bool
	var err error
	go func() {
		expired, err = svc.processWatchEvents(context.Background(), fw, schema.GroupVersionResource{}, "topic", new(string))
		close(done)
	}()

	fw.Error(&metav1.Status{
		Code:    410,
		Reason:  metav1.StatusReasonGone,
		Message: "too old resource version",
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch error")
	}
	if !expired {
		t.Fatalf("expected RV expiration")
	}
	if err != nil {
		t.Fatalf("expected no error for RV expiration, got %v", err)
	}
}
