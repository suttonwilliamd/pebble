package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

func main() {
	cwd, err := os.Getwd()
	if err == nil {
		err = run(os.Args[1:], cwd, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
func usage(w io.Writer) {
	fmt.Fprintln(w, `Pebble — experimental local-first version control

Usage:
  pebble init                     Initialize in the current directory
  pebble commit [-m] <message>     Snapshot all non-ignored files
  pebble status                   Show added, modified and deleted files
  pebble log                      Show complete current-branch history
  pebble branch [name]            List branches or create one at HEAD
  pebble checkout <branch>        Restore a branch (refuses tracked changes)
  pebble restore <file>           Discard edits to one file, restoring HEAD
  pebble fsck                     Verify all reachable objects
  pebble help                     Show this help

Identity: set PEBBLE_AUTHOR and PEBBLE_EMAIL before committing.
Remote push/pull are not ready. Keep Git and independent backups.`)
}
func run(args []string, cwd string, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing command; run pebble help")
	}
	cmd, params := args[0], args[1:]
	if cmd == "help" || cmd == "--help" || cmd == "-h" {
		usage(w)
		return nil
	}
	if cmd == "push" || cmd == "pull" {
		return errors.New("remote sync is experimental and disabled: complete history transfer and safe ref updates are not implemented; keep using Git for remotes")
	}
	switch cmd {
	case "init", "status", "log", "branch", "checkout", "commit", "restore", "fsck":
	default:
		return fmt.Errorf("unknown command %q; run pebble help", cmd)
	}
	if cmd == "init" {
		if len(params) != 0 {
			return errors.New("usage: pebble init")
		}
		repo, err := snapshot.Init(cwd)
		if err != nil {
			return err
		}
		p := filepath.Join(cwd, ".pebbleignore")
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			_, writeErr := f.WriteString("# Pebble ignore patterns (not Git ignore syntax)\n.pebble/\n.git/\n*.log\n")
			closeErr := f.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
		} else if !os.IsExist(err) {
			return err
		}
		fmt.Fprintln(w, "Initialized empty Pebble repository in", repo.RootPath())
		return nil
	}
	repo, err := snapshot.NewRepository(cwd)
	if err != nil {
		return err
	}
	switch cmd {
	case "commit":
		if len(params) > 0 && params[0] == "-m" {
			params = params[1:]
		}
		if len(params) == 0 {
			return errors.New("usage: pebble commit [-m] <message>")
		}
		author, email := strings.TrimSpace(os.Getenv("PEBBLE_AUTHOR")), strings.TrimSpace(os.Getenv("PEBBLE_EMAIL"))
		if author == "" || email == "" {
			return errors.New("set PEBBLE_AUTHOR and PEBBLE_EMAIL to your commit identity")
		}
		hash, err := repo.Commit(strings.Join(params, " "), author, email)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "[%s] %s\n", hash[:8], strings.Join(params, " "))
	case "status":
		if len(params) != 0 {
			return errors.New("usage: pebble status")
		}
		ref, err := repo.GetHead()
		if err != nil {
			return err
		}
		changes, err := repo.Status()
		if err != nil {
			return err
		}
		fmt.Fprintln(w, "On branch", strings.TrimPrefix(ref, "heads/"))
		if len(changes) == 0 {
			fmt.Fprintln(w, "Working tree clean")
		}
		for _, c := range changes {
			fmt.Fprintf(w, "%s  %s\n", c.Code, c.Path)
		}
	case "log":
		if len(params) != 0 {
			return errors.New("usage: pebble log")
		}
		ref, err := repo.GetHead()
		if err != nil {
			return err
		}
		hash, err := repo.GetRef(ref)
		if err != nil {
			return err
		}
		if hash == "" {
			fmt.Fprintln(w, "No commits yet")
		}
		seen := map[string]bool{}
		for hash != "" {
			if seen[hash] {
				return errors.New("cycle in commit history")
			}
			seen[hash] = true
			c, err := repo.GetCommit(hash)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "commit %s\nAuthor: %s <%s>\nDate:   %s\n\n    %s\n\n", hash, c.Author, c.Email, c.Timestamp.Format("Mon Jan 2 15:04:05 2006 -0700"), c.Message)
			hash = c.Parent
		}
	case "branch":
		if len(params) > 1 {
			return errors.New("usage: pebble branch [name]")
		}
		if len(params) == 1 {
			if err := repo.CreateBranch(params[0]); err != nil {
				return err
			}
			fmt.Fprintln(w, "Created branch:", params[0])
			return nil
		}
		refs, err := repo.GetRefs()
		if err != nil {
			return err
		}
		head, err := repo.GetHead()
		if err != nil {
			return err
		}
		for _, ref := range refs {
			prefix := "  "
			if ref.Name == head {
				prefix = "* "
			}
			fmt.Fprintln(w, prefix+strings.TrimPrefix(ref.Name, "heads/"))
		}
	case "checkout":
		if len(params) != 1 {
			return errors.New("usage: pebble checkout <branch>")
		}
		if err := repo.Checkout(params[0]); err != nil {
			return err
		}
		fmt.Fprintln(w, "Switched to branch:", params[0])
	case "restore":
		if len(params) != 1 {
			return errors.New("usage: pebble restore <file> (discards this file's uncommitted edits)")
		}
		p := params[0]
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(repo.RootPath(), p)
		if err != nil {
			return err
		}
		if err = repo.Restore(filepath.ToSlash(rel)); err != nil {
			return err
		}
		fmt.Fprintln(w, "Restored from HEAD:", filepath.ToSlash(rel))
	case "fsck":
		if len(params) != 0 {
			return errors.New("usage: pebble fsck")
		}
		n, err := repo.Verify()
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "Verified %d reachable objects\n", n)
	}
	return nil
}
