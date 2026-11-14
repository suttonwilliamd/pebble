package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/suttonwilliamd/pebble/internal/repository"
	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func printCommit(w io.Writer, id storev2.ID, c storev2.CommitData) error {
	zone, err := time.Parse("-0700", c.Offset)
	if err != nil {
		return err
	}
	_, offset := zone.Zone()
	stamp := time.Unix(c.Seconds, int64(c.Nanoseconds)).In(time.FixedZone(c.Offset, offset))
	_, err = fmt.Fprintf(w, "commit %s\nAuthor: %s <%s>\nDate:   %s %s\n\n    %s\n\n", id, c.Author, c.Email, stamp.Format("Mon Jan 2 15:04:05.999999999 2006"), c.Offset, c.Message)
	if err == nil && c.Committer != "" {
		_, err = fmt.Fprintf(w, "Committer: %s <%s>\n\n", c.Committer, c.CommitterEmail)
	}
	return err
}
func handleLog(params []string, repo *repository.Repository, w io.Writer) error {
	if len(params) > 1 {
		return fmt.Errorf("usage: pebble log [revision]")
	}
	visit := func(id storev2.ID, c storev2.CommitData) error { return printCommit(w, id, c) }
	if len(params) == 1 {
		return repo.LogRevision(context.Background(), params[0], visit)
	}
	id, err := repo.CurrentID()
	if err != nil {
		return err
	}
	if id == (storev2.ID{}) {
		_, err = fmt.Fprintln(w, "No commits yet")
		return err
	}
	return repo.LogRevision(context.Background(), "HEAD", visit)
}
func splitRevisionPaths(params []string) ([]string, []string, error) {
	for i, p := range params {
		if p == "--" {
			if i == len(params)-1 {
				return nil, nil, fmt.Errorf("missing path after --")
			}
			return params[:i], params[i+1:], nil
		}
	}
	return params, nil, nil
}
func inspectionPath(cwd string, repo *repository.Repository, input string) (string, error) {
	p := input
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	name, err := filepath.Rel(repo.RootPath(), p)
	if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside repository: %q", input)
	}
	return filepath.ToSlash(name), nil
}
func handleShow(params []string, cwd string, repo *repository.Repository, w io.Writer) error {
	revisions, paths, err := splitRevisionPaths(params)
	if err != nil {
		return err
	}
	if len(revisions) > 1 || len(paths) > 1 {
		return fmt.Errorf("usage: pebble show [revision] [-- file]")
	}
	revision := "HEAD"
	if len(revisions) == 1 {
		revision = revisions[0]
	}
	ctx := context.Background()
	if len(paths) == 1 {
		name, err := inspectionPath(cwd, repo, paths[0])
		if err != nil {
			return err
		}
		return repo.ShowFile(ctx, revision, name, w)
	}
	id, err := repo.ResolveRevision(ctx, revision)
	if err != nil {
		return err
	}
	c, err := repo.GetCommit(id)
	if err != nil {
		return err
	}
	if err = printCommit(w, id, c); err != nil {
		return err
	}
	return repo.DiffCommit(ctx, id.String(), w)
}
func handleDiff(params []string, cwd string, repo *repository.Repository, w io.Writer) error {
	staged := false
	if len(params) > 0 && params[0] == "--staged" {
		staged = true
		params = params[1:]
	}
	revisions, paths, err := splitRevisionPaths(params)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		name, err := inspectionPath(cwd, repo, path)
		if err != nil {
			return err
		}
		names = append(names, name)
	}
	return repo.Diff(context.Background(), repository.DiffOptions{Revisions: revisions, Staged: staged, Paths: names}, w)
}
func handleRestore(params []string, cwd string, repo *repository.Repository, w io.Writer) error {
	revision := "HEAD"
	explicit := false
	if len(params) > 0 && params[0] == "--source" {
		if len(params) < 3 {
			return fmt.Errorf("usage: pebble restore --source revision [--] path")
		}
		explicit = true
		revision = params[1]
		params = params[2:]
	}
	delimited := len(params) > 0 && params[0] == "--"
	if delimited {
		params = params[1:]
	}
	if len(params) != 1 || !delimited && strings.HasPrefix(params[0], "-") {
		return fmt.Errorf("usage: pebble restore [--source revision] [--] path (discards selected working edits)")
	}
	name, err := inspectionPath(cwd, repo, params[0])
	if err != nil {
		return err
	}
	if explicit {
		err = repo.RestoreFromContext(context.Background(), revision, name)
	} else {
		err = repo.Restore(name)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, "Restored from "+revision+":", name)
	return err
}
