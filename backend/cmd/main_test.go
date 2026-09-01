package main

import (
	"net"
	"testing"
)

func TestListenOnConfiguredAddrFallsBackWhenDefaultPortBusy(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:53727")
	if err != nil {
		t.Skipf("default port already in use: %v", err)
	}
	defer occupied.Close()

	t.Setenv("PORT", "")

	listener, err := listenOnConfiguredAddr()
	if err != nil {
		t.Fatalf("listenOnConfiguredAddr() error = %v", err)
	}
	defer listener.Close()

	if listener.Addr().String() == occupied.Addr().String() {
		t.Fatalf("listener did not fall back from occupied default port %s", occupied.Addr())
	}
}

func TestListenOnConfiguredAddrDoesNotFallbackWhenPortExplicit(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral port: %v", err)
	}
	defer occupied.Close()

	t.Setenv("PORT", occupied.Addr().String())

	listener, err := listenOnConfiguredAddr()
	if err == nil {
		listener.Close()
		t.Fatal("listenOnConfiguredAddr() succeeded on explicitly occupied port")
	}
}
