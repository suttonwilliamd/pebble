package syncproto

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func repo(t *testing.T) *snapshot.Repository {
	t.Helper()
	r, err := snapshot.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func write(t *testing.T, r *snapshot.Repository, name string, data []byte) {
	t.Helper()
	p := filepath.Join(r.RootPath(), name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatal(err)
	}
}
func commit(t *testing.T, r *snapshot.Repository, msg string) string {
	t.Helper()
	hash, err := r.Commit(msg, "Test", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
func testServer(t *testing.T) (*snapshot.Repository, *httptest.Server, *Client) {
	t.Helper()
	r := repo(t)
	h, err := NewHandler(r, testToken)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := NewClient(s.URL, testToken)
	if err != nil {
		t.Fatal(err)
	}
	return r, s, c
}
func TestTwoClientRoundtrip(t *testing.T) {
	server, _, c := testServer(t)
	a, b := repo(t), repo(t)
	write(t, a, "hello.txt", []byte("first"))
	first := commit(t, a, "initial")
	binary := []byte{0, 255, 0, 128, 64, 2, 1}
	write(t, a, "binary.dat", binary)
	write(t, a, "hello.txt", []byte("second"))
	tip := commit(t, a, "binary snapshot")
	if err := c.Push(context.Background(), a, "heads/main", "heads/main"); err != nil {
		t.Fatal(err)
	}
	bundle, err := c.Fetch(context.Background(), "heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Hash != tip {
		t.Fatal("wrong tip")
	}
	bundle.Ref = "heads/incoming/main"
	if err = b.ApplyBundle(bundle, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(b.RootPath(), "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("fetch modified files")
	}
	if err = b.Checkout("incoming/main"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(b.RootPath(), "binary.dat"))
	if !bytes.Equal(got, binary) {
		t.Fatal("binary corruption")
	}
	parent, err := b.GetCommit(tip)
	if err != nil || parent.Parent != first {
		t.Fatal("history incomplete")
	}
	if err = b.CreateBranch("work"); err != nil {
		t.Fatal(err)
	}
	if err = b.Checkout("work"); err != nil {
		t.Fatal(err)
	}
	write(t, b, "hello.txt", []byte("client B"))
	next := commit(t, b, "B edit")
	if err = c.Push(context.Background(), b, "heads/work", "heads/main"); err != nil {
		t.Fatal(err)
	}
	// The old client must not overwrite the newer remote, even with a fresh expected hash.
	if err = c.Push(context.Background(), a, "heads/main", "heads/main"); err == nil {
		t.Fatal("stale history overwrote remote")
	}
	if got, _ := server.GetRef("heads/main"); got != next {
		t.Fatal("remote tip corrupted")
	}
	if _, err = server.Verify(); err != nil {
		t.Fatal(err)
	}
}
func TestHTTPBoundaries(t *testing.T) {
	_, s, _ := testServer(t)
	cases := []struct {
		path, method, body, token string
		want                      int
	}{
		{"/", "GET", "", "", 200}, {"/healthz", "GET", "", "", 200}, {"/healthz", "POST", "", "", 405},
		{"/v1/refs", "GET", "", "", 401}, {"/v1/refs", "GET", "", "wrong", 401}, {"/v1/refs", "POST", "", testToken, 405},
		{"/v1/fetch", "POST", `{"ref":"heads/../../outside"}`, testToken, 400},
		{"/v1/fetch", "POST", `{"ref":"heads/main","unknown":true}`, testToken, 400},
		{"/v1/fetch", "POST", `{"ref":"heads/main"} {}`, testToken, 400},
		{"/v1/push", "POST", `{"bundle":null,"expected":""}`, testToken, 400},
		{"/objects/a", "GET", "", testToken, 404}, // Legacy panic-prone routes are not mounted.
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, s.URL+tc.path, strings.NewReader(tc.body))
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s: got %d want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
	req, _ := http.NewRequest("POST", s.URL+"/v1/push", strings.NewReader(strings.Repeat(" ", int(MaxTransferBytes)+1)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("oversize: %d", resp.StatusCode)
	}
}
func TestConcurrentCAS(t *testing.T) {
	server, _, c := testServer(t)
	source := repo(t)
	write(t, source, "file", []byte("base"))
	base := commit(t, source, "base")
	if err := source.CreateBranch("right"); err != nil {
		t.Fatal(err)
	}
	if err := c.Push(context.Background(), source, "heads/main", "heads/main"); err != nil {
		t.Fatal(err)
	}
	write(t, source, "file", []byte("left"))
	commit(t, source, "left")
	left, _ := source.Export("heads/main")
	if err := source.Checkout("right"); err != nil {
		t.Fatal(err)
	}
	write(t, source, "file", []byte("right"))
	commit(t, source, "right")
	right, _ := source.Export("heads/right")
	right.Ref = "heads/main"
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, b := range []*snapshot.Bundle{left, right} {
		wg.Add(1)
		go func(b *snapshot.Bundle) {
			defer wg.Done()
			var ack struct {
				OK bool `json:"ok"`
			}
			results <- c.request(context.Background(), "POST", "/v1/push", PushRequest{b, base}, &ack)
		}(b)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if strings.Contains(err.Error(), "HTTP 409") {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("lost CAS: successes=%d conflicts=%d", success, conflict)
	}
	if _, err := server.Verify(); err != nil {
		t.Fatal(err)
	}
}
func TestClientTransportPolicy(t *testing.T) {
	for _, u := range []string{"http://example.com", "ftp://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?token=secret", "https://example.com#fragment"} {
		if _, err := NewClient(u, testToken); err == nil {
			t.Errorf("unsafe URL allowed: %s", u)
		}
	}
	for _, u := range []string{"https://example.com", "http://127.0.0.1:7931", "http://[::1]:7931"} {
		if _, err := NewClient(u, testToken); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewHandler(repo(t), "short"); err == nil {
		t.Fatal("weak server token allowed")
	}
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHit = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	c, _ := NewClient(redirect.URL, testToken)
	if _, err := c.Refs(context.Background()); err == nil {
		t.Fatal("redirect followed")
	}
	if targetHit {
		t.Fatal("token sent to redirected origin")
	}
}
func TestInvalidGraphHTTP(t *testing.T) {
	server, s, _ := testServer(t)
	source := repo(t)
	write(t, source, "file", []byte("good"))
	commit(t, source, "one")
	b, _ := source.Export("heads/main")
	b.Objects[0].Content = []byte("tampered")
	body, _ := json.Marshal(PushRequest{b, ""})
	req, _ := http.NewRequest("POST", s.URL+"/v1/push", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("invalid graph status %d", resp.StatusCode)
	}
	if tip, _ := server.GetRef("heads/main"); tip != "" {
		t.Fatal("invalid graph published")
	}
}
