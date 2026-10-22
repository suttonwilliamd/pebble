// Package syncproto implements the bounded, authenticated Pebble v1 test protocol.
// It is separate from internal/remote, whose old prototype is not deployment-safe.
package syncproto

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

const MaxTransferBytes int64 = 32 << 20

type PushRequest struct {
	Bundle   *snapshot.Bundle `json:"bundle"`
	Expected string           `json:"expected"`
}
type FetchRequest struct {
	Ref string `json:"ref"`
}
type RefResponse struct {
	Version int            `json:"version"`
	Refs    []snapshot.Ref `json:"refs"`
}

// NewHandler serves one repository. The caller must provide a high-entropy token
// and TLS termination or an SSH tunnel. No legacy prototype routes are mounted.
func NewHandler(repo *snapshot.Repository, token string) (http.Handler, error) {
	if repo == nil || len(token) < 32 {
		return nil, fmt.Errorf("repository and a token of at least 32 characters are required")
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	slots := make(chan struct{}, 2)
	var writes sync.Mutex
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="pebble"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "busy; retry later", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/v1/refs":
			if !method(w, r, "GET") {
				return
			}
			refs, err := repo.GetRefs()
			if err != nil {
				http.Error(w, "cannot read refs", 500)
				return
			}
			sendJSON(w, RefResponse{snapshot.TransferVersion, refs})
		case "/v1/fetch":
			if !method(w, r, "POST") {
				return
			}
			var req FetchRequest
			if !receiveJSON(w, r, &req) {
				return
			}
			if err := snapshot.ValidateRef(req.Ref); err != nil {
				http.Error(w, "invalid ref", 400)
				return
			}
			bundle, err := repo.Export(req.Ref)
			if err != nil {
				http.Error(w, "branch unavailable or invalid", 404)
				return
			}
			sendJSON(w, bundle)
		case "/v1/push":
			if !method(w, r, "POST") {
				return
			}
			var req PushRequest
			if !receiveJSON(w, r, &req) {
				return
			}
			writes.Lock()
			err := repo.ApplyBundle(req.Bundle, req.Expected)
			writes.Unlock()
			if err != nil {
				if errors.Is(err, snapshot.ErrRefConflict) {
					http.Error(w, "branch changed or update is not fast-forward; fetch first", 409)
				} else if errors.Is(err, snapshot.ErrInvalidBundle) {
					http.Error(w, "bundle rejected; check graph and branch", 400)
				} else {
					http.Error(w, "storage unavailable; no ref update was acknowledged", 500)
				}
				return
			}
			sendJSON(w, struct {
				OK bool `json:"ok"`
			}{true})
		default:
			http.NotFound(w, r)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		switch r.URL.Path {
		case "/healthz":
			if !method(w, r, "GET") {
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			io.WriteString(w, "ok\n")
		case "/":
			if !method(w, r, "GET") {
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, landingPage)
		default:
			api.ServeHTTP(w, r)
		}
	}), nil
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	http.Error(w, "method not allowed", 405)
	return false
}
func receiveJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxTransferBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		var extra any
		if next := dec.Decode(&extra); next != io.EOF {
			if next == nil {
				err = fmt.Errorf("multiple JSON values")
			} else {
				err = next
			}
		}
	}
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			http.Error(w, "transfer exceeds 32 MiB limit", 413)
		} else {
			http.Error(w, "invalid request JSON", 400)
		}
		return false
	}
	return true
}
func sendJSON(w http.ResponseWriter, value any) {
	b, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "encoding failed", 500)
		return
	}
	if int64(len(b)) > MaxTransferBytes {
		http.Error(w, "history exceeds 32 MiB test-protocol limit", 413)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

const landingPage = `<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Pebble — remote dogfood server</title>
<style>body{font:18px system-ui;background:#111827;color:#e5e7eb;max-width:48rem;margin:10vh auto;padding:2rem}h1{color:#67e8f9}code{color:#a7f3d0}aside{border-left:3px solid #fbbf24;padding:1rem}li{margin:.7rem 0}</style>
<h1>Pebble</h1><p>Experimental version control. Real snapshots. A place to test remote sync.</p>
<aside>Dogfood service — keep Git and independent backups. Not production storage.</aside>
<h2>Protocol v1</h2><ul><li>Authenticated push and fetch for one repository</li><li>Complete, typed, SHA-256-verified history</li><li>Fast-forward updates with compare-and-swap protection</li><li>32 MiB per transfer; no merge, resumable transfer or large-binary optimization yet</li></ul>
<p>Use <code>pebble push &lt;https-url&gt;</code> and <code>pebble fetch &lt;https-url&gt;</code>. API routes require a bearer token; repository contents are not public.</p>
<p>The landing page and <code>/healthz</code> contain no private repository data.</p></html>`
