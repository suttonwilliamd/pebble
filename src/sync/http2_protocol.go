package sync

import (
	"crypto/tls"
	"fmt"
	"net/http"

	"golang.org/x/net/http2"
)

// HTTP2Protocol is responsible for handling HTTP/2 communication
type HTTP2Protocol struct {
	Server *http.Server
}

// NewHTTP2Protocol creates a new HTTP2Protocol instance
func NewHTTP2Protocol() *HTTP2Protocol {
	return &HTTP2Protocol{}
}

// StartServer starts the HTTP/2 server
func (h2 *HTTP2Protocol) StartServer(addr string, handler http.Handler) error {
	h2.Server = &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Configure HTTP/2
	err := http2.ConfigureServer(h2.Server, &http2.Server{})
	if err != nil {
		return err
	}

	// Start the server
	fmt.Printf("Starting HTTP/2 server on %s\n", addr)
	return h2.Server.ListenAndServeTLS("server.crt", "server.key")
}

// StopServer stops the HTTP/2 server
func (h2 *HTTP2Protocol) StopServer() error {
	if h2.Server != nil {
		return h2.Server.Close()
	}
	return nil
}

// CreateClient creates an HTTP/2 client
func (h2 *HTTP2Protocol) CreateClient() (*http.Client, error) {
	// Configure HTTP/2 transport
	transport := &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	// Create client
	client := &http.Client{
		Transport: transport,
	}

	return client, nil
}
