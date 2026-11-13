package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// Child-only isolation: never export Git configuration into the worker process.
func isolatedIdentityEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	empty := filepath.Join(home, "empty-gitconfig")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, entry := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "PEBBLE_AUTHOR") || strings.HasPrefix(key, "PEBBLE_COMMITTER") || key == "PEBBLE_EMAIL" || key == "HOME" || key == "USERPROFILE" || key == "XDG_CONFIG_HOME" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+empty,
		"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=core.autocrlf", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_KEY_1=core.filemode", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_KEY_2=core.excludesFile", "GIT_CONFIG_VALUE_2="+empty,
		"GIT_AUTHOR_NAME=Oracle Fixture", "GIT_AUTHOR_EMAIL=oracle@example.invalid", "GIT_COMMITTER_NAME=Oracle Fixture", "GIT_COMMITTER_EMAIL=oracle@example.invalid")
}
func TestStagingIdentityCLI(t *testing.T) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "pebble"+suffix)
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"+suffix), "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI: %v\n%s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("real Git oracle required", err)
	}
	t.Run("ExactRevisionsAndGitParity", func(t *testing.T) {
		root := t.TempDir()
		env := append(isolatedIdentityEnv(t), "PEBBLE_AUTHOR=Stage Fixture", "PEBBLE_EMAIL=stage@example.invalid")
		invoke := func(program string, success bool, args ...string) string {
			t.Helper()
			cmd := exec.Command(program, args...)
			cmd.Dir = root
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if (err == nil) != success {
				t.Fatalf("%s %v: %v\n%s", program, args, err, out)
			}
			return string(out)
		}
		put := func(name string, data []byte) {
			t.Helper()
			p := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, data, 0644); err != nil {
				t.Fatal(err)
			}
		}
		remove := func(name string) {
			t.Helper()
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				t.Fatal(err)
			}
		}
		invoke(bin, true, "init")
		invoke(git, true, "init", "-q", "-b", "main")
		// Avoid a Pebble-only rule file in the Git comparison without altering globals.
		put(".pebbleignore", nil)
		for name, data := range map[string][]byte{"chosen": []byte("base"), "other": []byte("base"), "gone": []byte("base"), "ignored/mod": []byte("base"), "ignored/del": []byte("base")} {
			put(name, data)
		}
		invoke(bin, true, "add", ".")
		invoke(git, true, "add", "--", ".pebbleignore", "chosen", "other", "gone", "ignored")
		invoke(bin, true, "commit", "base")
		invoke(git, true, "commit", "-qm", "base")
		parity := func() {
			t.Helper()
			store, err := storev2.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			id, err := store.GetRef(ctx, "heads/main")
			if err != nil {
				t.Fatal(err)
			}
			c, err := store.ReadCommit(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := store.ReadTree(ctx, c.Tree)
			if err != nil {
				t.Fatal(err)
			}
			names := strings.Split(strings.TrimSuffix(invoke(git, true, "ls-tree", "-r", "--name-only", "HEAD"), "\n"), "\n")
			got := make([]string, 0, len(entries))
			for _, entry := range entries {
				got = append(got, entry.Path)
				var data bytes.Buffer
				if err := store.CopyFile(ctx, entry.Manifest, &data); err != nil {
					t.Fatal(err)
				}
				oracle := []byte(invoke(git, true, "show", "HEAD:"+entry.Path))
				if !bytes.Equal(data.Bytes(), oracle) || sha256.Sum256(data.Bytes()) != sha256.Sum256(oracle) {
					t.Fatalf("Git byte/checksum mismatch: %s", entry.Path)
				}
			}
			sort.Strings(got)
			sort.Strings(names)
			if strings.Join(got, "\n") != strings.Join(names, "\n") {
				t.Fatalf("paths: Pebble %v Git %v", got, names)
			}
		}
		parity()
		a := []byte{0, 255, '\r', '\n', 128, '\n', 0, 13}
		b := []byte{255, 0, 'B', '\r', '\n', 0}
		put("chosen", a)
		put("other", []byte("unselected B"))
		put("addition", a)
		put("untracked", []byte("never accidentally committed"))
		invoke(bin, true, "add", "chosen", "addition")
		invoke(git, true, "add", "--", "chosen", "addition")
		put("chosen", b)
		put("addition", b)
		status := invoke(bin, true, "status")
		for _, want := range []string{"MM chosen\n", "AM addition\n", " M other\n", "?? untracked\n"} {
			if !strings.Contains(status, want) {
				t.Fatalf("missing %q in %s", want, status)
			}
		}
		staged := invoke(bin, true, "status", "--staged")
		if strings.Contains(staged, "other") || strings.Contains(staged, "untracked") {
			t.Fatal("unstaged path in staged inspection", staged)
		}
		unstaged := invoke(bin, true, "status", "--unstaged")
		if !strings.Contains(unstaged, "MM chosen") {
			t.Fatal(unstaged)
		}
		invoke(bin, true, "commit", "staged A not working B")
		invoke(git, true, "commit", "-qm", "staged A not working B")
		parity()
		if got, err := os.ReadFile(filepath.Join(root, "chosen")); err != nil || !bytes.Equal(got, b) {
			t.Fatalf("commit changed working B: %x %v", got, err)
		}
		invoke(bin, true, "branch", "exact-A")
		remove("gone")
		invoke(bin, true, "add", "gone")
		invoke(git, true, "add", "--", "gone")
		put("gone", []byte("readded but unstaged"))
		invoke(bin, true, "commit", "staged deletion")
		invoke(git, true, "commit", "-qm", "staged deletion")
		parity()
		// A staged addition participates in --all, but the staged deletion's re-add
		// and unrelated untracked content do not become index-tracked automatically.
		put("new-stage", a)
		invoke(bin, true, "add", "new-stage")
		invoke(git, true, "add", "--", "new-stage")
		put("new-stage", b)
		put(".gitignore", []byte("ignored/\n"))
		invoke(bin, true, "add", ".gitignore")
		invoke(git, true, "add", "--", ".gitignore")
		put("ignored/mod", b)
		remove("ignored/del")
		put("ignored/new", []byte("excluded"))
		invoke(bin, true, "commit", "--all", "explicit tracked refresh")
		invoke(git, true, "commit", "-aqm", "explicit tracked refresh")
		parity()
		invoke(bin, false, "commit", "no staged changes")
		// Preserve the generated untracked re-add before entering a branch where
		// that name is tracked; checkout must not overwrite that conflicting file.
		if got, err := os.ReadFile(filepath.Join(root, "gone")); err != nil || string(got) != "readded but unstaged" {
			t.Fatalf("re-add lost: %q %v", got, err)
		}
		if err := os.Rename(filepath.Join(root, "gone"), filepath.Join(root, "readded-saved")); err != nil {
			t.Fatal(err)
		}
		// Binary A is reconstructed from immutable staged objects in a fresh process.
		invoke(bin, true, "branch", "latest")
		invoke(bin, true, "checkout", "exact-A")
		for _, name := range []string{"chosen", "addition"} {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || sha256.Sum256(got) != sha256.Sum256(a) {
				t.Fatalf("checkout A checksum %s: %x %v", name, got, err)
			}
		}
		invoke(bin, true, "checkout", "latest")
		for _, name := range []string{"chosen", "addition", "new-stage", "ignored/mod"} {
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil || sha256.Sum256(got) != sha256.Sum256(b) {
				t.Fatalf("checkout B checksum %s: %x %v", name, got, err)
			}
		}
		invoke(bin, true, "fsck")
		for _, name := range []string{"readded-saved", "untracked", "ignored/new"} {
			if _, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				t.Fatalf("untracked bytes lost %s: %v", name, err)
			}
		}
	})
	t.Run("ConfigurationPrecedenceAndProvenance", func(t *testing.T) {
		root := t.TempDir()
		env := isolatedIdentityEnv(t)
		invoke := func(success bool, args ...string) string {
			t.Helper()
			cmd := exec.Command(bin, args...)
			cmd.Dir = root
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if (err == nil) != success {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
			return string(out)
		}
		invoke(true, "config", "--global", "user.name", "Global Fixture")
		invoke(true, "config", "--global", "user.email", "global@example.invalid")
		invoke(true, "init")
		invoke(true, "config", "user.name", "Local Fixture")
		sequence := 0
		commit := func(author, email, committer, committerEmail string) {
			t.Helper()
			sequence++
			if err := os.WriteFile(filepath.Join(root, "revision"), bytes.Repeat([]byte{byte(sequence), 0, 255, '\r', '\n'}, sequence), 0644); err != nil {
				t.Fatal(err)
			}
			invoke(true, "add", "revision")
			invoke(true, "commit", "identity")
			store, err := storev2.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			id, err := store.GetRef(context.Background(), "heads/main")
			if err != nil {
				t.Fatal(err)
			}
			c, err := store.ReadCommit(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if c.Author != author || c.Email != email || c.Committer != committer || c.CommitterEmail != committerEmail {
				t.Fatalf("identity provenance: %#v", c)
			}
			log := invoke(true, "log")
			if !strings.Contains(log, "Committer: "+committer+" <"+committerEmail+">") {
				t.Fatal(log)
			}
		}
		commit("Local Fixture", "global@example.invalid", "Local Fixture", "global@example.invalid")
		invoke(true, "config", "author.name", "Configured Author")
		invoke(true, "config", "committer.email", "configured-committer@example.invalid")
		commit("Configured Author", "global@example.invalid", "Local Fixture", "configured-committer@example.invalid")
		env = append(env, "PEBBLE_AUTHOR=Legacy Environment", "PEBBLE_EMAIL=legacy@example.invalid")
		commit("Legacy Environment", "legacy@example.invalid", "Legacy Environment", "legacy@example.invalid")
		env = append(env, "PEBBLE_AUTHOR_NAME=Actual Author", "PEBBLE_AUTHOR_EMAIL=author@example.invalid", "PEBBLE_COMMITTER_NAME=Actual Committer", "PEBBLE_COMMITTER_EMAIL=committer@example.invalid")
		commit("Actual Author", "author@example.invalid", "Actual Committer", "committer@example.invalid")
		env = append(env, "PEBBLE_AUTHOR_NAME=")
		invoke(true, "add", ".pebbleignore")
		invoke(false, "commit", "empty override must not fallback")
		// Malformed scope fails even when environment could otherwise supply values.
		if err := os.WriteFile(filepath.Join(root, ".pebble", "config.json"), []byte(`{"user.name":"first","user.name":"duplicate"}`), 0600); err != nil {
			t.Fatal(err)
		}
		invoke(false, "commit", "duplicate configuration")
	})
}
