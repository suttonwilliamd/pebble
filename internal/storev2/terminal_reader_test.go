package storev2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// terminalReader reports its terminal error only with the final data bytes.
// Every later read returns EOF, not the original error. Unlike FailAfter this
// deliberately models a non-sticky source, including data-plus-EOF readers.
type terminalReader struct {
	data     []byte
	terminal error
}

func (r *terminalReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.terminal
	}
	return n, nil
}

func TestTerminalSourceErrorsNeverPublish(t *testing.T) {
	byName, _ := corpus(t)
	fault := errors.New("non-sticky final read failure")
	// Empty chunk ends exactly at the full-header boundary; empty tree ends
	// at a full metadata read; binary chunk exercises the streaming copy path.
	for _, name := range []string{"chunk-empty", "tree-empty", "chunk-binary"} {
		for _, terminal := range []struct {
			name   string
			err    error
			reject bool
		}{
			{"non-EOF", fault, true},
			{"EOF", io.EOF, false},
		} {
			t.Run(name+"/"+terminal.name, func(t *testing.T) {
				s := newStore(t)
				ctx := context.Background()
				v := byName[name]
				id, wire := goldenID(t, v), goldenBytes(t, v)
				err := s.Put(ctx, id, &terminalReader{data: wire, terminal: terminal.err})
				if terminal.reject {
					if !errors.Is(err, fault) {
						t.Fatalf("source failure lost or publication acknowledged: %v", err)
					}
					assertAbsent(t, s.objectPath(id))
					// A healthy retry also proves that a rejected source did not
					// poison the next operation or retain the writer lock.
					if err = s.Put(ctx, id, bytes.NewReader(wire)); err != nil {
						t.Fatal("healthy retry:", err)
					}
				} else if err != nil {
					t.Fatal("valid final bytes plus EOF rejected:", err)
				}
				var restored bytes.Buffer
				if err = s.Copy(ctx, id, &restored); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(restored.Bytes(), wire) {
					t.Fatal("canonical bytes changed")
				}
				if err = s.Verify(ctx, id); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
