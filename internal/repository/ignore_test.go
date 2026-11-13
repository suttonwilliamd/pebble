package repository

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoreRuleReadSubstitution(t *testing.T) {
	for _, name := range []string{".gitignore", "nested/.gitignore", "nested/.pebbleignore"} {
		for _, remove := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/replace", true: "/remove"}[remove], func(t *testing.T) {
				r, err := Init(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				write(t, r.root, name, []byte("private\n"))
				write(t, r.root, "nested/private", []byte("must remain visible on failure"))
				fired := false
				r.fault = func(stage, target string) error {
					if fired || stage != "read-open" || target != name {
						return nil
					}
					fired = true
					p := filepath.Join(r.root, filepath.FromSlash(name))
					if err := os.Rename(p, p+".saved"); err != nil {
						return err
					}
					if remove {
						return nil
					}
					return os.WriteFile(p, []byte("*\n"), 0644)
				}
				if _, err := r.Status(); err == nil {
					t.Fatal("substituted ignore rules accepted")
				}
				if !fired {
					t.Fatal("guarded ignore read not reached")
				}
			})
		}
	}
}

func TestIgnoreRuleLinks(t *testing.T) {
	for _, name := range []string{".gitignore", ".pebbleignore", "nested/.gitignore", "nested/.pebbleignore"} {
		t.Run(name, func(t *testing.T) {
			r, err := Init(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "rules")
			if err := os.WriteFile(outside, []byte("*\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if !requireFixtureLinks(t, r.root) {
				return
			}
			p := filepath.Join(r.root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := createLink(outside, p, false); err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "nested/private", []byte("user data"))
			if _, err := r.Status(); err == nil {
				t.Fatal("dereferenced external rule file")
			}
			if _, err := r.CheckIgnore([]string{"nested/private"}); err == nil {
				t.Fatal("inspection dereferenced external rules")
			}
			got, err := os.ReadFile(outside)
			if err != nil || string(got) != "*\n" {
				t.Fatalf("external rules changed: %q %v", got, err)
			}
		})
	}
}

func TestIgnoreExcludedParentDoesNotReadChildren(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, ".gitignore", []byte("blocked/\n!blocked/keep\n"))
	write(t, r.root, "blocked/.pebbleignore", []byte("!keep\n"))
	write(t, r.root, "blocked/keep", []byte("ignored"))
	r.fault = func(stage, name string) error {
		if stage == "read-open" && name == "blocked/.pebbleignore" {
			return errors.New("excluded rule file must not be read")
		}
		return nil
	}
	results, err := r.CheckIgnore([]string{"blocked/keep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Ignored || results[0].Source != ".gitignore" || results[0].Line != 1 || results[0].Pattern != "blocked/" {
		t.Fatalf("excluded-parent provenance: %+v", results)
	}
	changes, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Path == "blocked/keep" {
			t.Fatal("excluded descendant exposed")
		}
	}
}
