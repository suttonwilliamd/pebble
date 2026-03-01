package remote

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Client represents a remote repository client
type Client struct {
	baseURL    string
	httpClient *http.Client
	auth       Auth
}

// Auth represents authentication credentials
type Auth struct {
	Username string
	Password string
	Token   string
}

// RemoteInfo represents remote repository information
type RemoteInfo struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	LastPush    string   `json:"last_push,omitempty"`
	LastPull    string   `json:"last_pull,omitempty"`
}

// ObjectInfo represents object metadata
type ObjectInfo struct {
	Hash string `json:"hash"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// RefInfo represents reference information
type RefInfo struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// ListRefsResponse represents the response from listing refs
type ListRefsResponse struct {
	Refs []RefInfo `json:"refs"`
}

// ListObjectsResponse represents the response from listing objects
type ListObjectsResponse struct {
	Objects []ObjectInfo `json:"objects"`
	HasMore bool         `json:"has_more"`
}

// NewClient creates a new remote client
func NewClient(baseURL string, auth Auth) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		auth: auth,
	}
}

// ListRefs lists all references from the remote
func (c *Client) ListRefs(ctx context.Context) ([]RefInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/refs", nil)
	if err != nil {
		return nil, err
	}
	
	c.addAuth(req)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list refs: %s", resp.Status)
	}
	
	var result ListRefsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return result.Refs, nil
}

// Push pushes refs and objects to the remote
func (c *Client) Push(ctx context.Context, refs []RefInfo, objects map[string][]byte) error {
	// Prepare request body
	type PushRequest struct {
		Refs    []RefInfo          `json:"refs"`
		Objects map[string]string  `json:"objects"` // hash -> base64 content
	}
	
	objMap := make(map[string]string)
	for hash, data := range objects {
		objMap[hash] = hex.EncodeToString(data)
	}
	
	body := PushRequest{
		Refs:    refs,
		Objects: objMap,
	}
	
	bodyData, err := json.Marshal(body)
	if err != nil {
		return err
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/push", bytes.NewReader(bodyData))
	if err != nil {
		return err
	}
	
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("push failed: %s - %s", resp.Status, string(body))
	}
	
	return nil
}

// Pull pulls refs and objects from the remote
func (c *Client) Pull(ctx context.Context, wantRefs []string) (map[string]string, map[string][]byte, error) {
	type PullRequest struct {
		Refs []string `json:"refs"`
	}
	
	body := PullRequest{Refs: wantRefs}
	bodyData, _ := json.Marshal(body)
	
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/pull", bytes.NewReader(bodyData))
	if err != nil {
		return nil, nil, err
	}
	
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("pull failed: %s", resp.Status)
	}
	
	// Parse response
	type PullResponse struct {
		Refs    map[string]string `json:"refs"`
		Objects map[string]string `json:"objects"` // hash -> base64
	}
	
	var result PullResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
	}
	
	// Decode objects
	objects := make(map[string][]byte)
	for hash, encoded := range result.Objects {
		data, err := hex.DecodeString(encoded)
		if err != nil {
			return nil, nil, err
		}
		objects[hash] = data
	}
	
	return result.Refs, objects, nil
}

// GetObject downloads a single object
func (c *Client) GetObject(ctx context.Context, hash string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/objects/"+hash, nil)
	if err != nil {
		return nil, err
	}
	
	c.addAuth(req)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("object not found: %s", hash)
	}
	
	return io.ReadAll(resp.Body)
}

// AddAuth adds authentication to the request
func (c *Client) addAuth(req *http.Request) {
	if c.auth.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.auth.Token)
	} else if c.auth.Username != "" {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
}

// Server represents a remote server
type Server struct {
	basePath   string
	httpServer *http.Server
	objectsDir string
	refsDir    string
	mu         sync.RWMutex
}

// NewServer creates a new remote server
func NewServer(basePath, listenAddr string) (*Server, error) {
	objectsDir := filepath.Join(basePath, "objects")
	refsDir := filepath.Join(basePath, "refs")
	
	for _, dir := range []string{objectsDir, refsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	
	srv := &Server{
		basePath:   basePath,
		objectsDir: objectsDir,
		refsDir:    refsDir,
	}
	
	mux := http.NewServeMux()
	mux.HandleFunc("/refs", srv.handleRefs)
	mux.HandleFunc("/push", srv.handlePush)
	mux.HandleFunc("/pull", srv.handlePull)
	mux.HandleFunc("/objects/", srv.handleObject)
	
	srv.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}
	
	return srv, nil
}

// Start starts the server
func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

// StartTLS starts the server with TLS
func (s *Server) StartTLS(certFile, keyFile string) error {
	return s.httpServer.ListenAndServeTLS(certFile, keyFile)
}

// Shutdown shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleRefs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.listRefs(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listRefs(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var refs []RefInfo
	
	// Walk refs directory
	filepath.Walk(s.refsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || info.Name() == "HEAD" {
			return nil
		}
		
		relPath, _ := filepath.Rel(s.refsDir, path)
		relPath = strings.ReplaceAll(relPath, string(os.PathSeparator), "/")
		
		data, _ := os.ReadFile(path)
		hash := string(bytes.TrimSpace(data))
		
		refs = append(refs, RefInfo{
			Name: relPath,
			Hash: hash,
		})
		return nil
	})
	
	json.NewEncoder(w).Encode(ListRefsResponse{Refs: refs})
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	type PushRequest struct {
		Refs    []RefInfo         `json:"refs"`
		Objects map[string]string `json:"objects"`
	}
	
	var req PushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// Store objects
	for hash, encoded := range req.Objects {
		data, err := hex.DecodeString(encoded)
		if err != nil {
			continue
		}
		
		objPath := filepath.Join(s.objectsDir, hash[:2], hash[2:])
		os.MkdirAll(filepath.Dir(objPath), 0755)
		os.WriteFile(objPath, data, 0644)
	}
	
	// Update refs
	for _, ref := range req.Refs {
		refPath := filepath.Join(s.refsDir, ref.Name)
		os.MkdirAll(filepath.Dir(refPath), 0755)
		os.WriteFile(refPath, []byte(ref.Hash+"\n"), 0644)
	}
	
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	type PullRequest struct {
		Refs []string `json:"refs"`
	}
	
	var req PullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	// Get refs
	refs := make(map[string]string)
	for _, refName := range req.Refs {
		refPath := filepath.Join(s.refsDir, refName)
		if data, err := os.ReadFile(refPath); err == nil {
			refs[refName] = string(bytes.TrimSpace(data))
		}
	}
	
	// Get objects (simplified - would need proper dependency tracking)
	objects := make(map[string]string)
	// For now, include all objects
	filepath.Walk(s.objectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		
		hash := info.Name()
		data, _ := os.ReadFile(path)
		objects[hash] = hex.EncodeToString(data)
		return nil
	})
	
	type PullResponse struct {
		Refs    map[string]string `json:"refs"`
		Objects map[string]string `json:"objects"`
	}
	
	json.NewEncoder(w).Encode(PullResponse{
		Refs:    refs,
		Objects: objects,
	})
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/objects/")
	
	objPath := filepath.Join(s.objectsDir, hash[:2], hash[2:])
	
	data, err := os.ReadFile(objPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}
