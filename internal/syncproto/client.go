package syncproto

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("remote must be an HTTP(S) origin without credentials, query or path")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	local := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, fmt.Errorf("HTTPS required except for loopback HTTP via a local server or SSH tunnel")
	}
	if len(token) < 32 {
		return nil, fmt.Errorf("set PEBBLE_TOKEN_FILE to a strong bearer token (at least 32 characters)")
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("remote redirects are refused") }}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, payload, result any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if int64(len(b)) > MaxTransferBytes {
			return fmt.Errorf("transfer exceeds 32 MiB test-protocol limit")
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("remote returned HTTP %d (401: token required; 409: fetch/divergence; 413: transfer too large)", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxTransferBytes+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > MaxTransferBytes {
		return fmt.Errorf("remote response exceeds 32 MiB limit")
	}
	return json.Unmarshal(b, result)
}
func (c *Client) Refs(ctx context.Context) ([]snapshot.Ref, error) {
	var resp RefResponse
	if err := c.request(ctx, "GET", "/v1/refs", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Version != snapshot.TransferVersion {
		return nil, fmt.Errorf("unsupported remote protocol version")
	}
	for _, ref := range resp.Refs {
		if err := snapshot.ValidateRef(ref.Name); err != nil {
			return nil, err
		}
	}
	return resp.Refs, nil
}
func (c *Client) Push(ctx context.Context, repo *snapshot.Repository, localRef, remoteRef string) error {
	if err := snapshot.ValidateRef(remoteRef); err != nil {
		return err
	}
	b, err := repo.Export(localRef)
	if err != nil {
		return err
	}
	b.Ref = remoteRef
	refs, err := c.Refs(ctx)
	if err != nil {
		return err
	}
	expected := ""
	for _, ref := range refs {
		if ref.Name == remoteRef {
			expected = ref.Hash
		}
	}
	var ack struct {
		OK bool `json:"ok"`
	}
	if err = c.request(ctx, "POST", "/v1/push", PushRequest{b, expected}, &ack); err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("remote did not acknowledge push")
	}
	return nil
}
func (c *Client) Fetch(ctx context.Context, remoteRef string) (*snapshot.Bundle, error) {
	if err := snapshot.ValidateRef(remoteRef); err != nil {
		return nil, err
	}
	var b snapshot.Bundle
	if err := c.request(ctx, "POST", "/v1/fetch", FetchRequest{remoteRef}, &b); err != nil {
		return nil, err
	}
	if b.Ref != remoteRef {
		return nil, fmt.Errorf("remote returned a different branch")
	}
	return &b, nil
}
