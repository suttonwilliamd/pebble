package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the shipped CLI, not parser wiring. Every repository, configuration
// file and build output is generated under TempDir; no user Git excludes apply.
func TestIgnoreCLI(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pebble")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("Git oracle required:", err)
	}
	config := t.TempDir()
	empty := filepath.Join(config, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "PEBBLE_AUTHOR") || strings.HasPrefix(key, "PEBBLE_COMMITTER") || key == "HOME" || key == "USERPROFILE" || key == "XDG_CONFIG_HOME" || key == "PEBBLE_EMAIL" {
			continue
		}
		env = append(env, value)
	}
	env = append(env, "HOME="+config, "USERPROFILE="+config, "XDG_CONFIG_HOME="+config, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+empty,
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.excludesFile", "GIT_CONFIG_VALUE_0="+empty,
		"GIT_CONFIG_KEY_1=core.ignoreCase", "GIT_CONFIG_VALUE_1=false", "PEBBLE_AUTHOR=Ignore Fixture", "PEBBLE_EMAIL=fixture@example.invalid")
	root := t.TempDir()
	put := func(name, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(program, cwd string, ok bool, args ...string) string {
		t.Helper()
		c := exec.Command(program, args...)
		c.Dir, c.Env = cwd, env
		out, err := c.CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("%s %v: %v\n%s", program, args, err, out)
		}
		return string(out)
	}
	t.Run("GitParity", func(t *testing.T) {
		invoke(git, root, true, "init", "-q")
		put(".pebbleignore", "")
		invoke(bin, root, true, "init")
		put(".gitignore", "\xef\xbb\xbfbom.txt\r\n# comment\r\n*.log\r\n!keep.log\r\n/root.txt\r\ncache/\r\nblocked/\r\n!blocked/keep.txt\r\nopened/*\r\n!opened/keep/\r\n\\#hash\r\n\\!bang\r\nspace\\ name\r\ntrailing   \r\nedge\\ \r\na/**/b.txt\r\n**/deep.dat\r\ntrail/**\r\nfoo?bar\r\n[ab].tmp\r\n[!a].one\r\n[[:digit:]].num\r\nliteral\\[x\\]\r\n/nested/local.txt\r\n*.order\r\n!keep.order\r\n*.order\r\n!last.order\r\n?.utf8\n??.utf8\n!é.utf8\n?.byte\npair\\/leaf\n[]a].bracket\n[^x].not\nmix/a**b/end\nmiddle/**/end\n[z-a].rev\n[a-c-e].range\n")
		put("nested/.gitignore", "!error.log\n/local.txt\n*.nested\n!keep.nested\n")
		put("nested/deeper/.gitignore", "!take.nested\n")
		// Excluded directories' rules must not be consulted.
		put("blocked/.gitignore", "!keep.txt\n")
		paths := []string{"error.log", "keep.log", "plain", "root.txt", "nested/root.txt", "cache/item", "nested/cache/item", "blocked/keep.txt", "opened/no.txt", "opened/keep/yes", "#hash", "!bang", "space name", "trailing", "edge ", "a/b.txt", "a/x/y/b.txt", "a/x/c.txt", "deep.dat", "nested/deep.dat", "trail/child", "fooXbar", "a.tmp", "c.tmp", "a.one", "b.one", "3.num", "x.num", "literal[x]", "nested/error.log", "nested/local.txt", "nested/x.nested", "nested/keep.nested", "nested/deeper/take.nested", "keep.order", "last.order", "ghost/absent.log", "cache", "file/cache"}
		paths = append(paths, "é.utf8", "é.byte", "a.byte", "pair/leaf", "].bracket", "a.bracket", "y.not", "x.not", "mix/axxb/end", "mix/a/x/b/end", "middle/end", "middle/a/b/end")
		paths = append(paths, "bom.txt", "z.rev", "a.rev", "n.rev", "a.range", "b.range", "c.range", "d.range", "e.range", "-.range")
		// Create ordinary portable leaves; trailing-space paths are inspect-only.
		for _, p := range paths {
			if p == "cache" || p == "edge " {
				continue
			}
			put(p, "fixture")
		}
		// A directory-only rule must not exclude an ordinary leaf of the same name.
		put("file/cache", "file not directory")
		args := append([]string{"check-ignore", "-v", "--"}, paths...)
		// Pebble rejects nonportable user paths even for inspection; test Git's
		// escaped trailing-space rule using a missing portable sibling instead.
		for i, p := range paths {
			if p == "edge " {
				paths[i] = "edge"
				args[i+3] = "edge"
			}
		}
		c := exec.Command(git, "check-ignore", "-z", "-v", "--non-matching", "--stdin")
		c.Dir, c.Env, c.Stdin = root, env, strings.NewReader(strings.Join(paths, "\x00")+"\x00")
		wire, err := c.CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(string(wire), "\x00")
		if len(fields) != len(paths)*4+1 {
			t.Fatalf("oracle records: %q", wire)
		}
		out := invoke(bin, root, true, args...)
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) != len(paths) {
			t.Fatalf("explanations: %s", out)
		}
		status := invoke(bin, root, true, "status")
		for i, p := range paths {
			source, line, pattern := fields[i*4], fields[i*4+1], fields[i*4+2]
			state := "unmatched"
			if source != "" {
				state = "ignored"
				if strings.HasPrefix(pattern, "!") {
					state = "included"
				}
			} else {
				line = "0"
			}
			want := fmt.Sprintf("%s:%s:%s\t%s\t%s", source, line, pattern, p, state)
			if lines[i] != want {
				t.Errorf("%s: got %q; Git expects %q", p, lines[i], want)
			}
			if p == "cache" || p == "edge" {
				continue
			}
			visible := strings.Contains(status, "?? "+p+"\n")
			if visible != (state != "ignored") {
				t.Errorf("%s visibility differs from Git (%s):\n%s", p, state, status)
			}
		}
		if out := invoke(bin, root, false, "check-ignore", "plain"); out != "" {
			t.Fatalf("quiet no-match: %q", out)
		}
		if out := invoke(bin, root, true, "check-ignore", "error.log", "plain"); out != "error.log\n" {
			t.Fatalf("default output: %q", out)
		}
		if out := invoke(bin, filepath.Join(root, "nested"), true, "check-ignore", "-v", "error.log"); out != "nested/.gitignore:1:!error.log\terror.log\tincluded\n" {
			t.Fatal(out)
		}
		invoke(bin, root, false, "check-ignore", "../outside")
	})
	t.Run("LayeringAndTrackedHistory", func(t *testing.T) {
		root = t.TempDir()
		put(".pebbleignore", "")
		invoke(bin, root, true, "init")
		invoke(git, root, true, "init", "-q")
		put("hidden/deep/tracked.log", "original")
		put("hidden/deep/deleted.log", "deleted original")
		invoke(bin, root, true, "add", ".")
		invoke(bin, root, true, "commit", "before ignores")
		invoke(bin, root, true, "branch", "before")
		invoke(git, root, true, "add", "--", "hidden/deep/tracked.log", "hidden/deep/deleted.log")
		put(".gitignore", "hidden/\n*.layer\n")
		put(".pebbleignore", "!same.layer\n!nested/deep.layer\n!.pebble/\n!.git/\n")
		put("nested/.gitignore", "deep.layer\n!git.layer\n")
		put("nested/.pebbleignore", "!deep.layer\ngit.layer\n")
		put("hidden/deep/tracked.log", "modified tracked")
		if err := os.Remove(filepath.Join(root, "hidden/deep/deleted.log")); err != nil {
			t.Fatal(err)
		}
		put("hidden/untracked", "do not track")
		put("same.layer", "visible")
		put("nested/deep.layer", "visible")
		put("nested/git.layer", "ignored")
		out := invoke(bin, root, true, "check-ignore", "-v", "same.layer", "nested/deep.layer", "nested/git.layer", "hidden/deep/tracked.log", "hidden/deep/deleted.log", ".pebble/worktree.guard", ".git/config")
		for _, want := range []string{
			".pebbleignore:1:!same.layer\tsame.layer\tincluded\n",
			"nested/.pebbleignore:1:!deep.layer\tnested/deep.layer\tincluded\n",
			"nested/.pebbleignore:2:git.layer\tnested/git.layer\tignored\n",
			":0:\thidden/deep/tracked.log\ttracked\n", ":0:\thidden/deep/deleted.log\ttracked\n",
			"<metadata>:0:.pebble/\t.pebble/worktree.guard\tignored\n", "<metadata>:0:.git/\t.git/config\tignored\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %s", want, out)
			}
		}
		if out := invoke(git, root, false, "check-ignore", "hidden/deep/tracked.log", "hidden/deep/deleted.log"); out != "" {
			t.Fatal(out)
		}
		status := invoke(bin, root, true, "status")
		for _, want := range []string{" M hidden/deep/tracked.log\n", " D hidden/deep/deleted.log\n", "?? same.layer\n", "?? nested/deep.layer\n"} {
			if !strings.Contains(status, want) {
				t.Errorf("missing %q: %s", want, status)
			}
		}
		for _, bad := range []string{"hidden/untracked", "nested/git.layer", ".pebble/", ".git/"} {
			if strings.Contains(status, bad) {
				t.Errorf("excluded %s exposed: %s", bad, status)
			}
		}
		invoke(bin, root, false, "checkout", "before")
		invoke(bin, root, true, "restore", "hidden/deep/tracked.log")
		invoke(bin, root, true, "restore", "hidden/deep/deleted.log")
		put("hidden/deep/tracked.log", "committed under ignore")
		if err := os.Remove(filepath.Join(root, "hidden/deep/deleted.log")); err != nil {
			t.Fatal(err)
		}
		invoke(bin, root, true, "add", ".")
		invoke(bin, root, true, "commit", "ignored tracked modifications and deletion")
		invoke(bin, root, true, "branch", "after")
		invoke(bin, root, true, "checkout", "before")
		for p, want := range map[string]string{"hidden/deep/tracked.log": "original", "hidden/deep/deleted.log": "deleted original"} {
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			if err != nil || string(got) != want {
				t.Fatalf("history %s: %q %v", p, got, err)
			}
		}
		invoke(bin, root, true, "checkout", "after")
		if got, err := os.ReadFile(filepath.Join(root, "hidden/deep/tracked.log")); err != nil || string(got) != "committed under ignore" {
			t.Fatalf("tracked history: %q %v", got, err)
		}
		if _, err := os.Lstat(filepath.Join(root, "hidden/deep/deleted.log")); !os.IsNotExist(err) {
			t.Fatalf("deletion not committed: %v", err)
		}
		if got, err := os.ReadFile(filepath.Join(root, "hidden/untracked")); err != nil || string(got) != "do not track" {
			t.Fatalf("ignored data lost: %q %v", got, err)
		}
		invoke(bin, root, true, "fsck")
	})
}
