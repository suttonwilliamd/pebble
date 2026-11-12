package storev2

import (
	"context"
	"reflect"
	"testing"
)

func TestMetadataFacadePreservesCanonicalObjects(t *testing.T) {
	byName, vectors := corpus(t)
	s := newStore(t)
	ctx := context.Background()
	for _, v := range vectors {
		if v.Error != "" || v.ID == "" {
			continue
		}
		id, err := ParseID(v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if id.Kind != Tree && id.Kind != Commit {
			continue
		}
		putGoldens(t, s, byName, v.Name)
		var actual ID
		if id.Kind == Tree {
			entries, err := s.ReadTree(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			actual, err = s.CreateTree(ctx, entries)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			c, err := s.ReadCommit(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			actual, err = s.CreateCommit(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
		}
		if actual != id {
			t.Fatalf("%s metadata changed canonical address: %s != %s", v.Name, actual, id)
		}
	}
	tree, err := s.CreateTree(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := func(message string) ID {
		id, err := s.CreateCommit(ctx, CommitData{Tree: tree, Author: "A", Email: "a@example.com", Seconds: -42, Nanoseconds: 987654321, Offset: "-0000", Message: message})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := parent("a"), parent("b")
	want := CommitData{Tree: tree, Parents: []ID{b, a}, Author: "Å", Email: "x@example.com", Seconds: -1, Nanoseconds: 123456789, Offset: "-0000", Message: "ordered\nmetadata\t"}
	id, err := s.CreateCommit(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.ReadCommit(ctx, id)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata changed: %#v %v", got, err)
	}
	want.Parents = []ID{a, b}
	other, err := s.CreateCommit(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if id == other {
		t.Fatal("parent order lost")
	}
	want.Parents = []ID{a, a}
	if _, err = s.CreateCommit(ctx, want); err == nil {
		t.Fatal("duplicate parents accepted")
	}
	if err = s.UpdateRef(ctx, "heads/main", nil, id); err != nil {
		t.Fatal(err)
	}
	refs, err := reopened.ListRefs(ctx)
	if err != nil || refs["heads/main"] != id {
		t.Fatalf("catalog lost root: %v %v", refs, err)
	}
	if n, err := reopened.VerifyRefs(ctx); err != nil || n != 4 {
		t.Fatalf("closure count: %d %v", n, err)
	}
}
