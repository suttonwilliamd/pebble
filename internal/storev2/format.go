// Package storev2 implements the guarded format-2 object store used by the local
// repository engine. Legacy services remain separate and reject marked stores.
package storev2

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

type Kind byte

const (
	Chunk Kind = iota + 1
	ManifestPage
	Manifest
	Tree
	Commit
	Tag
	HeaderSize       = 13
	MaxChunkBytes    = 8 << 20
	MaxMetadataBytes = 1 << 20
)

func (k Kind) String() string {
	switch k {
	case Chunk:
		return "chunk"
	case ManifestPage:
		return "manifest-page"
	case Manifest:
		return "manifest"
	case Tree:
		return "tree"
	case Commit:
		return "commit"
	case Tag:
		return "tag"
	default:
		return "unknown"
	}
}
func (k Kind) valid() bool { return k >= Chunk && k <= Tag }

var (
	ErrInvalidObject = errors.New("invalid v2 object")
	ErrFormat        = errors.New("unsupported or incomplete v2 repository")
	ErrLocked        = errors.New("v2 repository writer is locked")
	ErrConflict      = errors.New("v2 reference conflict")
	ErrGraphLimit    = errors.New("v2 graph verification resource limit")
	// ErrPublicationUncertain means a complete new value may already be visible.
	// Reopen/verify and retry the same operation; never assume the old value won.
	ErrPublicationUncertain = errors.New("v2 publication outcome uncertain")
)

type ID struct {
	Kind   Kind
	Digest [sha256.Size]byte
}

func (id ID) String() string { return fmt.Sprintf("pv2:%s:sha256:%x", id.Kind, id.Digest) }

func ParseID(s string) (ID, error) {
	var id ID
	prefix, rest, ok := strings.Cut(s, ":")
	if !ok || prefix != "pv2" {
		return id, invalid("id")
	}
	kind, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return id, invalid("id")
	}
	for k := Chunk; k <= Tag; k++ {
		if k.String() == kind {
			id.Kind = k
			break
		}
	}
	algorithm, digest, ok := strings.Cut(rest, ":")
	if !ok || !id.Kind.valid() || algorithm != "sha256" || len(digest) != 64 {
		return ID{}, invalid("id")
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ID{}, invalid("id")
		}
	}
	if _, err := hex.Decode(id.Digest[:], []byte(digest)); err != nil {
		return ID{}, invalid("id")
	}
	return id, nil
}

// Header returns the exact canonical envelope header, without allocating payload
// storage. Negotiation/ref messages are deliberately not addressable kinds.
func Header(kind Kind, size uint64) ([HeaderSize]byte, error) {
	var header [HeaderSize]byte
	if !kind.valid() {
		return header, invalid("kind")
	}
	limit := uint64(MaxMetadataBytes)
	if kind == Chunk {
		limit = MaxChunkBytes
	}
	if size > limit {
		return header, invalid("limit")
	}
	copy(header[:4], "PB2\x00")
	header[4] = byte(kind)
	binary.BigEndian.PutUint64(header[5:], size)
	return header, nil
}

// NewID validates a bounded payload's canonical schema and hashes its complete
// envelope. It does not build a manifest, store bytes, or validate dependencies.
func NewID(kind Kind, payload []byte) (ID, error) {
	header, err := Header(kind, uint64(len(payload)))
	if err != nil {
		return ID{}, err
	}
	if kind != Chunk {
		if _, err = parsePayload(kind, payload); err != nil {
			return ID{}, err
		}
	}
	h := sha256.New()
	h.Write(header[:])
	h.Write(payload)
	id := ID{Kind: kind}
	h.Sum(id.Digest[:0])
	return id, nil
}

func invalid(rule string) error { return fmt.Errorf("%w: %s", ErrInvalidObject, rule) }
