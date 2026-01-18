package sync

import (
	"testing"

	"golang.org/x/net/http2"
)

func TestHTTP2Protocol(t *testing.T) {
	// Create HTTP2Protocol instance
	h2 := NewHTTP2Protocol()

	// Create client
	client, err := h2.CreateClient()
	if err != nil {
		t.Fatal(err)
	}

	// Verify client is created
	if client == nil {
		t.Error("Client not created")
	}

	// Verify client transport is HTTP/2
	if _, ok := client.Transport.(*http2.Transport); !ok {
		t.Error("Client transport is not HTTP/2")
	}
}
