package repository

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mutationFixture(t *testing.T) *Repository {
	t.Helper()
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "dir/tracked", []byte("owned original"))
	committed(t, r, "before")
	if err = r.CreateBranch("before"); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "dir/tracked", []byte("owned target"))
	committed(t, r, "after")
	if err = r.CreateBranch("after"); err != nil {
		t.Fatal(err)
	}
	if err = r.Checkout("before"); err != nil {
		t.Fatal(err)
	}
	return r
}
func requireFixtureLinks(t *testing.T, root string) bool {
	t.Helper()
	p := filepath.Join(root, "probe-link")
	err := createLink("missing", p, false)
	if err == nil {
		if err = os.Remove(p); err != nil {
			t.Fatal(err)
		}
		return true
	}
	if os.Getenv("PEBBLE_REQUIRE_LINKS") == "1" {
		t.Fatalf("required native link capability unavailable: %v", err)
	}
	t.Logf("link capability unavailable; this subcase does not prove a native link race: %v", err)
	return false
}
func TestFilesystemMutationRaces(t *testing.T) {
	for _, point := range []string{"retain", "retain-anchored", "retain-rename", "install", "install-anchored", "install-rename", "head"} {
		for _, kind := range []string{"leaf", "parent", "link", "content"} {
			if kind == "content" && strings.HasPrefix(point, "install") {
				continue
			}
			t.Run(point+"/"+kind, func(t *testing.T) {
				r := mutationFixture(t)
				head, err := r.CurrentID()
				if err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				outsideFile := filepath.Join(outside, "tracked")
				if err = os.WriteFile(outsideFile, []byte("external target must not be touched"), 0644); err != nil {
					t.Fatal(err)
				}
				if kind == "link" && !requireFixtureLinks(t, outside) {
					return
				}
				expected := publicSnapshot(t, r.root)
				fired, blocked := false, false
				stop := errors.New("native pin blocked replacement")
				r.fault = func(stage, path string) error {
					if fired || stage != point {
						return nil
					}
					fired = true
					if strings.HasPrefix(point, "install") {
						expected = publicSnapshot(t, r.root)
					}
					p := filepath.Join(r.root, "dir", "tracked")
					if kind == "content" {
						before, err := os.Lstat(p)
						if err != nil {
							return err
						}
						if err = os.WriteFile(p, []byte("foreign same-inode bytes"), 0644); err != nil {
							if runtime.GOOS != "windows" || point != "retain-rename" {
								return err
							}
							blocked = true
							return stop
						}
						after, err := os.Lstat(p)
						if err != nil {
							return err
						}
						if !os.SameFile(before, after) {
							return errors.New("content race unexpectedly replaced identity")
						}
					} else if kind == "parent" {
						err := os.Rename(filepath.Join(r.root, "dir"), filepath.Join(r.root, "saved-parent"))
						if err != nil {
							if runtime.GOOS != "windows" || !strings.HasSuffix(point, "-anchored") && !strings.HasSuffix(point, "-rename") {
								return err
							}
							blocked = true
							return stop
						}
						if err = os.Mkdir(filepath.Join(r.root, "dir"), 0755); err != nil {
							return err
						}
						if err = os.WriteFile(p, []byte("foreign parent bytes"), 0644); err != nil {
							return err
						}
					} else {
						if _, err := os.Lstat(p); err == nil {
							if err = os.Rename(p, filepath.Join(r.root, "saved-leaf")); err != nil {
								if runtime.GOOS != "windows" || point != "retain-rename" {
									return err
								}
								blocked = true
								return stop
							}
						} else if !os.IsNotExist(err) {
							return err
						}
						if kind == "link" {
							err = createLink(outsideFile, p, false)
						} else {
							err = os.WriteFile(p, []byte("foreign leaf bytes"), 0644)
						}
						if err != nil {
							return err
						}
					}
					expected = publicSnapshot(t, r.root)
					return nil
				}
				if err = r.Checkout("after"); err == nil {
					t.Fatal("ownership substitution acknowledged")
				}
				if !fired {
					t.Fatal("mutation window not reached")
				}
				r.fault = nil
				if !journalExists(t, r) {
					t.Fatal("failed post-intent operation discarded recovery state")
				}
				if id, err := r.CurrentID(); err != nil || id != head {
					t.Fatalf("HEAD advanced across external change: %s %v", id, err)
				}
				assertPublicSnapshot(t, r.root, expected)
				if got, err := os.ReadFile(outsideFile); err != nil || string(got) != "external target must not be touched" {
					t.Fatalf("followed/changed external target: %q %v", got, err)
				}
				if blocked {
					t.Logf("OS blocked %s substitution at %s; no substitution was performed", kind, point)
					if err = r.Recover(context.Background()); err != nil {
						t.Fatal(err)
					}
					if got, err := os.ReadFile(filepath.Join(r.root, "dir", "tracked")); err != nil || string(got) != "owned target" {
						t.Fatalf("blocked substitution retry: %q %v", got, err)
					}
				} else {
					t.Logf("performed %s substitution at %s; exact public namespace preserved", kind, point)
					before := fixtureSnapshot(t, r.root)
					if err = r.Recover(context.Background()); err == nil {
						t.Fatal("recovery claimed later replacement")
					}
					assertPublicSnapshot(t, r.root, expected)
					after := fixtureSnapshot(t, r.root)
					if len(before) != len(after) {
						t.Fatal("recovery removed foreign data")
					}
					for name, want := range before {
						if after[name] != want {
							t.Fatalf("recovery changed %s", name)
						}
					}
				}
			})
		}
	}
}
func TestFilesystemReadParentRace(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "link"}[link], func(t *testing.T) {
			r := mutationFixture(t)
			head, err := r.CurrentID()
			if err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if link && !requireFixtureLinks(t, outside) {
				return
			}
			if err = os.WriteFile(filepath.Join(outside, "tracked"), []byte("external unread bytes"), 0644); err != nil {
				t.Fatal(err)
			}
			fired := false
			expected := publicSnapshot(t, r.root)
			r.fault = func(stage, path string) error {
				if fired || stage != "read-open" || path != "dir/tracked" {
					return nil
				}
				fired = true
				if err := os.Rename(filepath.Join(r.root, "dir"), filepath.Join(r.root, "saved-parent")); err != nil {
					return err
				}
				if link {
					err = createLink(outside, filepath.Join(r.root, "dir"), true)
				} else {
					if err = os.Mkdir(filepath.Join(r.root, "dir"), 0755); err == nil {
						err = os.WriteFile(filepath.Join(r.root, "dir", "tracked"), []byte("owned original"), 0644)
					}
				}
				if err == nil {
					expected = publicSnapshot(t, r.root)
				}
				return err
			}
			if _, err = r.Commit("must refuse substituted source", "Fixture", "fixture@example.invalid"); err == nil {
				t.Fatal("commit read through a substituted parent")
			}
			if !fired {
				t.Fatal("read window not reached")
			}
			if id, err := r.CurrentID(); err != nil || id != head {
				t.Fatalf("HEAD changed: %s %v", id, err)
			}
			assertPublicSnapshot(t, r.root, expected)
			if got, err := os.ReadFile(filepath.Join(outside, "tracked")); err != nil || string(got) != "external unread bytes" {
				t.Fatalf("outside data changed: %q %v", got, err)
			}
		})
	}
}
func TestFilesystemCommitIdentityRace(t *testing.T) {
	r := mutationFixture(t)
	head, err := r.CurrentID()
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "dir/tracked", []byte("new commit candidate"))
	fired := false
	expected := publicSnapshot(t, r.root)
	r.fault = func(stage, path string) error {
		if fired || stage != "commit-publish" {
			return nil
		}
		fired = true
		p := filepath.Join(r.root, "dir", "tracked")
		if err := os.Rename(p, filepath.Join(r.root, "saved-candidate")); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte("new commit candidate"), 0644); err != nil {
			return err
		}
		expected = publicSnapshot(t, r.root)
		return nil
	}
	if _, err = r.Commit("must not adopt replacement", "Fixture", "fixture@example.invalid"); err == nil {
		t.Fatal("commit adopted identical-byte foreign identity")
	}
	if !fired {
		t.Fatal("commit publication window not reached")
	}
	if id, err := r.CurrentID(); err != nil || id != head {
		t.Fatalf("HEAD changed: %s %v", id, err)
	}
	assertPublicSnapshot(t, r.root, expected)
	got, err := os.ReadFile(filepath.Join(r.root, "dir", "tracked"))
	if err != nil || !bytes.Equal(got, []byte("new commit candidate")) {
		t.Fatalf("foreign working bytes changed: %q %v", got, err)
	}
}
