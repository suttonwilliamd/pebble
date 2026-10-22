package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/syncproto"
)

func main() {
	if err := serve(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func serve() error {
	data := flag.String("data", "./pebble-server-data", "dedicated repository directory")
	addr := flag.String("listen", "127.0.0.1:7931", "HTTP listener (use TLS reverse proxy/SSH tunnel)")
	tokenFile := flag.String("token-file", os.Getenv("PEBBLE_TOKEN_FILE"), "path to bearer token secret")
	health := flag.String("healthcheck", "", "check a health URL and exit (container healthcheck)")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *health != "" {
		c := &http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(*health)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("unhealthy HTTP %d", resp.StatusCode)
		}
		return nil
	}
	token, err := syncproto.ReadTokenFile(*tokenFile)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	var repo *snapshot.Repository
	if _, err = os.Stat(filepath.Join(root, ".pebble")); os.IsNotExist(err) {
		repo, err = snapshot.Init(root)
	} else if err == nil {
		repo, err = snapshot.NewRepository(root)
	}
	if err != nil {
		return err
	}
	if repo == nil || repo.RootPath() != root {
		return fmt.Errorf("data directory does not contain an initialized repository")
	}
	if _, err = repo.Verify(); err != nil {
		return fmt.Errorf("repository failed startup integrity check: %w", err)
	}
	handler, err := syncproto.NewHandler(repo, token)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		log.Printf("Pebble experimental sync listening on %s; one repository; 32 MiB transfers", *addr)
		done <- server.ListenAndServe()
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
