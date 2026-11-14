package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/repository"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
	"github.com/suttonwilliamd/pebble/internal/syncproto"
)

var errNoIgnoredPaths = errors.New("no ignored paths")

func main() {
	cwd, err := os.Getwd()
	if err == nil {
		err = run(os.Args[1:], cwd, os.Stdout)
	}
	if err != nil {
		if errors.Is(err, errNoIgnoredPaths) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
func usage(w io.Writer) {
	fmt.Fprintln(w, `Pebble — experimental local-first version control

Usage:
  pebble init                     Initialize in the current directory
  pebble add [--] <path>...        Stage exact file revisions or deletions
  pebble commit [--all] [-m] <message> Commit staged revisions only
  pebble status [--staged|--unstaged] Show HEAD/index and index/worktree changes
  pebble config [--global] <key> [value] Read/write Pebble identity configuration
  pebble log [revision]           Show complete ordered-parent history
  pebble show [revision] [-- file] Show a commit patch or literal file bytes
  pebble diff [--staged] [rev [rev]] [-- path...] Inspect revision/index/worktree
  pebble check-ignore [-v] <path>... Explain ignore rules (tracked paths stay tracked)
  pebble branch [name]            List branches or create one at HEAD
  pebble checkout <branch>        Restore a branch (refuses tracked changes)
  pebble restore [--source rev] [--] <path> Discard selected working edits only
  pebble recover                  Resume an interrupted checkout or restore
  pebble enable-filesystem        Explicitly enable literal-link tree entries
  pebble push <url> [branch]       Push HEAD to a remote branch (default: current)
  pebble fetch <url> [branch]      Fetch into incoming/<branch> (default: main)
  pebble fsck                     Verify all stored objects and references
  pebble migrate-v1 <src> <dest>   Verify/copy v1 into a new separate v2 repository
  pebble help                     Show this help

Identity: pebble config user.name/user.email; environment overrides configuration.
PEBBLE_AUTHOR/PEBBLE_EMAIL set both roles; PEBBLE_AUTHOR_NAME/EMAIL and
PEBBLE_COMMITTER_NAME/EMAIL override their respective roles.
Remotes: legacy v1 repositories only; format2 sync is not implemented.
Legacy/unmarked repositories require explicit migration for local commands.
Checkout/restore retain durable recovery intent; use pebble recover after interruption.
Keep Git and backups; process-interruption tests are not power-loss qualification.`)
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
	if cmd == "migrate-v1" {
		return handleMigration(params, cwd, w)
	}
	if cmd == "config" && len(params) > 0 && params[0] == "--global" {
		return handleConfig(params, "", w)
	}
	if cmd == "pull" {
		return errors.New("pull/merge is not implemented: use pebble fetch <url> [branch], inspect history, then checkout incoming/<branch>; keep Git and backups")
	}
	switch cmd {
	case "init", "add", "config", "status", "check-ignore", "log", "show", "diff", "branch", "checkout", "commit", "restore", "recover", "enable-filesystem", "push", "fetch", "fsck":
	default:
		return fmt.Errorf("unknown command %q; run pebble help", cmd)
	}
	// Resolve only the initial working directory, never stored tree components.
	physical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	cwd = physical
	if cmd == "init" {
		if len(params) != 0 {
			return errors.New("usage: pebble init")
		}
		repo, err := repository.Init(cwd)
		if err != nil {
			return err
		}
		p := filepath.Join(cwd, ".pebbleignore")
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			_, writeErr := f.WriteString("# Git-compatible ignore patterns; loaded after .gitignore in this directory\n.pebble/\n.git/\n*.log\n")
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
	if cmd == "push" || cmd == "fetch" {
		legacy, err := snapshot.NewRepository(cwd)
		if err != nil {
			return err
		}
		return handleSync(cmd, params, legacy, w)
	}
	repo, err := repository.Open(cwd)
	if err != nil {
		return err
	}
	switch cmd {
	case "config":
		return handleConfig(params, repo.RootPath(), w)
	case "add":
		if len(params) > 0 && params[0] == "--" {
			params = params[1:]
		}
		if len(params) == 0 {
			return errors.New("usage: pebble add [--] <path>...")
		}
		names := make([]string, 0, len(params))
		for _, input := range params {
			p := input
			if !filepath.IsAbs(p) {
				p = filepath.Join(cwd, p)
			}
			name, err := filepath.Rel(repo.RootPath(), p)
			if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
				return fmt.Errorf("path outside repository: %q", input)
			}
			names = append(names, filepath.ToSlash(name))
		}
		return repo.Stage(names)
	case "check-ignore":
		verbose := false
		if len(params) > 0 && (params[0] == "-v" || params[0] == "--verbose") {
			verbose, params = true, params[1:]
		}
		if len(params) > 0 && params[0] == "--" {
			params = params[1:]
		}
		if len(params) == 0 {
			return errors.New("usage: pebble check-ignore [-v|--verbose] [--] <path>...")
		}
		names := make([]string, 0, len(params))
		for _, input := range params {
			p := input
			if !filepath.IsAbs(p) {
				p = filepath.Join(cwd, p)
			}
			name, err := filepath.Rel(repo.RootPath(), p)
			if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
				return fmt.Errorf("path outside repository: %q", input)
			}
			name = filepath.ToSlash(name)
			if strings.HasSuffix(input, "/") || strings.HasSuffix(input, string(filepath.Separator)) {
				name += "/"
			}
			names = append(names, name)
		}
		results, err := repo.CheckIgnore(names)
		if err != nil {
			return err
		}
		matched := false
		for i, result := range results {
			if verbose {
				state := "unmatched"
				if result.Tracked {
					state = "tracked"
				} else if result.Ignored {
					state = "ignored"
				} else if result.Source != "" {
					state = "included"
				}
				fmt.Fprintf(w, "%s:%d:%s\t%s\t%s\n", result.Source, result.Line, result.Pattern, params[i], state)
				matched = matched || result.Source != ""
			} else if result.Ignored {
				fmt.Fprintln(w, params[i])
				matched = true
			}
		}
		if !matched {
			return errNoIgnoredPaths
		}
	case "enable-filesystem":
		if len(params) != 0 {
			return errors.New("usage: pebble enable-filesystem")
		}
		if err := repo.EnableFilesystem(context.Background()); err != nil {
			return err
		}
		fmt.Fprintln(w, "Filesystem capability enabled; incompatible older writers must not use this repository")
	case "commit":
		all := false
		if len(params) > 0 && (params[0] == "--all" || params[0] == "-a") {
			all, params = true, params[1:]
		}
		if len(params) > 0 && params[0] == "-m" {
			params = params[1:]
		}
		if len(params) == 0 || strings.HasPrefix(params[0], "-") {
			return errors.New("usage: pebble commit [--all|-a] [-m] <message>")
		}
		identity, err := resolveIdentity(repo.RootPath())
		if err != nil {
			return err
		}
		hash, err := repo.Commit(strings.Join(params, " "), identity, all)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "[%x] %s\n", hash.Digest[:4], strings.Join(params, " "))
	case "status":
		filter := ""
		if len(params) == 1 && (params[0] == "--staged" || params[0] == "--unstaged") {
			filter = params[0]
		} else if len(params) != 0 {
			return errors.New("usage: pebble status [--staged|--unstaged]")
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
			if filter == "--staged" && (c.Code[0] == ' ' || c.Code == "??") || filter == "--unstaged" && c.Code[1] == ' ' {
				continue
			}
			fmt.Fprintf(w, "%s %s\n", c.Code, c.Path)
		}
	case "log":
		return handleLog(params, repo, w)
	case "show":
		return handleShow(params, cwd, repo, w)
	case "diff":
		return handleDiff(params, cwd, repo, w)
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
		return handleRestore(params, cwd, repo, w)
	case "recover":
		if len(params) != 0 {
			return errors.New("usage: pebble recover")
		}
		recoveryErr := repo.Recover(context.Background())
		var aborted *repository.RecoveryAbortedError
		if recoveryErr != nil && !errors.As(recoveryErr, &aborted) {
			return recoveryErr
		}
		receipts, err := repo.RecoveryReceipts()
		if err != nil {
			return err
		}
		for _, receipt := range receipts {
			fmt.Fprintf(w, "Aborted %s; requested operation was NOT completed; preserved versions: %s\n", receipt.Operation, receipt.Directory)
			fmt.Fprintf(w, "Prior HEAD: %s (%s); requested HEAD: %s (%s)\n", receipt.BeforeHead, receipt.BeforeID, receipt.RequestedHead, receipt.RequestedID)
			if receipt.RestoreSource != nil {
				fmt.Fprintln(w, "Restore source:", *receipt.RestoreSource)
			}
			fmt.Fprintln(w, "Reason:", receipt.Reason)
			for _, restored := range receipt.Restorations {
				action := "Automatic alternate placement"
				if restored.Canonical {
					action = "Restored canonical name"
				}
				state := ""
				if restored.Changed {
					state = " (public name subsequently changed; saved version retained)"
				}
				fmt.Fprintf(w, "%s: %s -> %s%s\n", action, restored.Path, restored.Location, state)
			}
			for _, version := range receipt.Versions {
				state := ""
				if version.Changed {
					state = " (subsequently changed or removed; not overwritten)"
				}
				fmt.Fprintf(w, "  %s %s -> %s%s\n", version.Role, version.Path, version.Location, state)
			}
		}
		_, err = fmt.Fprintln(w, "Working-tree recovery complete (or no pending operation)")
		return err
	case "fsck":
		if len(params) != 0 {
			return errors.New("usage: pebble fsck")
		}
		n, err := repo.Verify()
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "Verified %d stored objects and all references\n", n)
	}
	return nil
}

func handleSync(cmd string, params []string, repo *snapshot.Repository, w io.Writer) error {
	if len(params) < 1 || len(params) > 2 {
		return fmt.Errorf("usage: pebble %s <url> [branch]", cmd)
	}
	head, err := repo.GetHead()
	if err != nil {
		return err
	}
	branch := "main"
	if cmd == "push" {
		branch = strings.TrimPrefix(head, "heads/")
	}
	if len(params) == 2 {
		branch = params[1]
	}
	ref := "heads/" + branch
	if err = snapshot.ValidateRef(ref); err != nil {
		return err
	}
	token, err := syncproto.ReadTokenFile(os.Getenv("PEBBLE_TOKEN_FILE"))
	if err != nil {
		return err
	}
	client, err := syncproto.NewClient(params[0], token)
	if err != nil {
		return err
	}
	if cmd == "push" {
		if err = client.Push(context.Background(), repo, head, ref); err != nil {
			return err
		}
		fmt.Fprintf(w, "Pushed HEAD to remote branch %s\n", branch)
		return nil
	}
	target := "heads/incoming/" + branch
	if head == target {
		return errors.New("cannot fetch into the checked-out incoming branch; checkout a local work branch first")
	}
	expected, err := repo.GetRef(target)
	if os.IsNotExist(err) {
		expected = ""
	} else if err != nil {
		return err
	}
	bundle, err := client.Fetch(context.Background(), ref)
	if err != nil {
		return err
	}
	bundle.Ref = target
	if err = repo.ImportBundle(bundle, expected); err != nil {
		return err
	}
	fmt.Fprintf(w, "Fetched %s into incoming/%s; working files unchanged\nInspect with: pebble checkout incoming/%s\n", branch, branch, branch)
	return nil
}
