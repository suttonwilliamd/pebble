package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/suttonwilliamd/pebble/internal/remote"
	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "init":
		handleInit(args)
	case "commit":
		handleCommit(args)
	case "log":
		handleLog(args)
	case "status":
		handleStatus(args)
	case "push":
		handlePush(args)
	case "pull":
		handlePull(args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Pebble - Version Control System")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  pebble init              Initialize a new repository")
	fmt.Println("  pebble commit <message>  Create a new commit")
	fmt.Println("  pebble log               Show commit history")
	fmt.Println("  pebble status            Show working tree status")
	fmt.Println("  pebble help              Show this help message")
}

func handleInit(args []string) {
	if len(args) > 0 {
		fmt.Println("Usage: pebble init")
		os.Exit(1)
	}

	// Initialize in current directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Check if already initialized
	if snapshot.IsInitialized(cwd) {
		fmt.Println("Already initialized")
		os.Exit(1)
	}

	repo, err := snapshot.Init(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Create .pebbleignore if it doesn't exist
	ignorePath := filepath.Join(cwd, ".pebbleignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		defaultIgnore := "# Pebble ignore patterns\n.pebble/\n.git/\n*.log\n"
		os.WriteFile(ignorePath, []byte(defaultIgnore), 0644)
	}

	fmt.Printf("Initialized empty Pebble repository in %s\n", repo.RootPath())
}

func handleCommit(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: pebble commit <message>")
		os.Exit(1)
	}

	message := args[0]

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	repo, err := snapshot.NewRepository(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Load ignore patterns
	ignorePatterns := []string{".pebble", ".git"}
	if data, err := os.ReadFile(filepath.Join(cwd, ".pebbleignore")); err == nil {
		// Simple parsing - just split by newlines
		lines := string(data)
		for _, line := range []byte(lines) {
			if line == '\n' || line == '#' {
				continue
			}
			// Add more sophisticated parsing if needed
		}
	}

	// Generate index
	generator := snapshot.NewFileIndexGenerator(cwd, ignorePatterns)
	index, err := generator.GenerateIndex()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating index: %v\n", err)
		os.Exit(1)
	}

	// Store all blob objects
	for _, entry := range index.Entries {
		if entry.Hash == "" {
			continue // Directory or symlink
		}
		obj, err := snapshot.CreateBlob(filepath.Join(cwd, entry.Path))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating blob for %s: %v\n", entry.Path, err)
			continue
		}
		repo.StoreObject(obj)
	}

	// Create tree from index
	entries := make([]snapshot.TreeEntry, 0)
	for _, entry := range index.Entries {
		if entry.Hash == "" {
			continue
		}
		entries = append(entries, snapshot.TreeEntry{
			Name: entry.Path,
			Mode: entry.Mode,
			Hash: entry.Hash,
			Type: snapshot.ObjectTypeBlob,
		})
	}
	tree := snapshot.CreateTree(entries)
	repo.StoreObject(tree)

	// Get parent commit
	headRef, err := repo.GetHead()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting HEAD: %v\n", err)
		os.Exit(1)
	}
	parentHash, _ := repo.GetRef(headRef)

	// Create commit
	commit := snapshot.CreateCommit(tree.Hash, parentHash, "User", "user@example.com", message)
	repo.StoreObject(commit)

	// Update ref
	err = repo.SetRef(headRef, commit.Hash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting ref: %v\n", err)
		os.Exit(1)
	}

	// Save index
	repo.SaveIndex(index)

	fmt.Printf("[%s] %s\n", commit.Hash[:8], message)
}

func handleLog(args []string) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	repo, err := snapshot.NewRepository(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	headRef, _ := repo.GetHead()
	commitHash, _ := repo.GetRef(headRef)

	if commitHash == "" {
		fmt.Println("No commits yet")
		return
	}

	// Get commit properly
	commit, err := repo.GetCommit(commitHash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading commit: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("commit %s\n", commitHash)
	fmt.Printf("Author: %s <%s>\n", commit.Author, commit.Email)
	fmt.Printf("Date:   %s\n", commit.Timestamp.Format("Mon Jan 2 15:04:05 2006 -0700"))
	fmt.Println()
	fmt.Printf("    %s\n", commit.Message)
}

func handleStatus(args []string) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	repo, err := snapshot.NewRepository(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Try to load index
	index, err := repo.LoadIndex()
	if err != nil {
		fmt.Println("No commits yet")
		return
	}

	fmt.Printf("On branch main\n")
	fmt.Printf("Index hash: %s\n", index.Hash)
	fmt.Printf("Files tracked: %d\n", len(index.Entries))
}

func handlePush(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: pebble push <remote-url> [token]")
		os.Exit(1)
	}

	remoteURL := args[0]
	token := ""
	if len(args) > 1 {
		token = args[1]
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	repo, err := snapshot.NewRepository(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get current ref
	headRef, _ := repo.GetHead()
	commitHash, _ := repo.GetRef(headRef)

	if commitHash == "" {
		fmt.Println("Nothing to push - no commits")
		return
	}

	// Create client
	auth := remote.Auth{Token: token}
	client := remote.NewClient(remoteURL, auth)

	// Collect objects to push
	objects := make(map[string][]byte)
	
	// Get commit object
	commitObj, err := repo.GetObject(commitHash)
	if err == nil && commitObj.Content != nil {
		objects[commitHash] = commitObj.Content
	}

	// Push refs
	refs := []remote.RefInfo{{Name: headRef, Hash: commitHash}}
	err = client.Push(context.Background(), refs, objects)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Push error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Pushed to %s\n", remoteURL)
}

func handlePull(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: pebble pull <remote-url> [token]")
		os.Exit(1)
	}

	remoteURL := args[0]
	token := ""
	if len(args) > 1 {
		token = args[1]
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	repo, err := snapshot.NewRepository(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Create client
	auth := remote.Auth{Token: token}
	client := remote.NewClient(remoteURL, auth)

	// Pull refs
	wantRefs := []string{"heads/main"}
	remoteRefs, objects, err := client.Pull(context.Background(), wantRefs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Pull error: %v\n", err)
		os.Exit(1)
	}

	// Store objects
	for hash, data := range objects {
		obj := &snapshot.Object{
			Type:    snapshot.ObjectTypeBlob,
			Size:    int64(len(data)),
			Hash:    hash,
			Content: data,
		}
		repo.StoreObject(obj)
	}

	// Update refs
	for refName, hash := range remoteRefs {
		repo.SetRef(refName, hash)
	}

	fmt.Printf("Pulled from %s\n", remoteURL)
	fmt.Printf("Updated refs: %d\n", len(remoteRefs))
}
