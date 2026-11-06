package snapshot

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxBundleBytes bounds the complete version-1 JSON file, including base64 data.
// Local bundles reuse the typed, complete-history transfer format and object limit.
const MaxBundleBytes int64 = 32 << 20

// CreateBundleFile exports current HEAD, not uncommitted files or other branches.
// The writer lock pins HEAD for validation. Exclusive creation never replaces a
// destination (including a symlink); failed writes remove only our new file.
func (r *Repository) CreateBundleFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// A bundle is a backup, not repository metadata. Do not manufacture bogus
	// objects/refs or Git metadata when the destination is inside this repository.
	rel, err := filepath.Rel(r.rootPath, abs)
	first := strings.Split(filepath.ToSlash(rel), "/")[0]
	if err == nil && (strings.EqualFold(first, ".pebble") || strings.EqualFold(first, ".git")) {
		return fmt.Errorf("bundle destination must be outside repository metadata")
	}
	unlock, err := r.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	head, err := r.GetHead()
	if err != nil {
		return err
	}
	var contentBytes int64
	bundle, err := r.export(head, func(hash string) (*Object, error) {
		obj, err := r.GetObject(hash)
		if err != nil {
			return nil, err
		}
		// Stop accumulating large graphs before encoding. JSON overhead is
		// checked exactly below; object count separately bounds that overhead.
		contentBytes += int64(base64.StdEncoding.EncodedLen(len(obj.Content)))
		if contentBytes > MaxBundleBytes {
			return nil, fmt.Errorf("bundle exceeds %d bytes", MaxBundleBytes)
		}
		return obj, nil
	})
	if err != nil {
		return err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if int64(len(data)) > MaxBundleBytes {
		return fmt.Errorf("bundle exceeds %d bytes", MaxBundleBytes)
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("cannot create new bundle: %w", err)
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			os.Remove(abs)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

// ImportBundleFile validates the entire file and graph before storing objects,
// then publishes only a NEW branch under the existing repository writer lock.
// Even an existing empty branch is refused. HEAD and working files never change.
func (r *Repository) ImportBundleFile(path, branch string) error {
	ref := "heads/" + branch
	if err := validateRef(ref); err != nil {
		return err
	}
	// Stat before opening: opening a FIFO can otherwise block before f.Stat.
	// As with repository metadata, the input directory must be trusted and must
	// not be concurrently replaced between this check and os.Open.
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err = checkBundleFileInfo(info); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return err
	}
	if err = checkBundleFileInfo(info); err != nil {
		return err
	}
	bundle, err := decodeBundle(f)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	// Validate the original source reference as well as the caller's target.
	// Changing the ref does not change content addresses or commit metadata.
	if err = validateRef(bundle.Ref); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	bundle.Ref = ref
	return r.applyBundle(bundle, "", true, true)
}

func checkBundleFileInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: bundle must be a regular file", ErrInvalidBundle)
	}
	if info.Size() > MaxBundleBytes {
		return fmt.Errorf("%w: bundle exceeds %d bytes", ErrInvalidBundle, MaxBundleBytes)
	}
	return nil
}

// Decode array entries individually so small invalid objects cannot expand into
// an unbounded object slice before graph validation enforces the object limit.
func decodeBundle(reader io.Reader) (*Bundle, error) {
	limited := &io.LimitedReader{R: reader, N: MaxBundleBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected a JSON bundle object")
	}
	bundle := &Bundle{}
	var seen uint8
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var field uint8
		switch key {
		case "version":
			field = 1
		case "ref":
			field = 2
		case "hash":
			field = 4
		case "objects":
			field = 8
		default:
			return nil, fmt.Errorf("unknown bundle field %q", key)
		}
		if seen&field != 0 {
			return nil, fmt.Errorf("duplicate bundle field %q", key)
		}
		seen |= field
		switch key {
		case "version":
			err = decoder.Decode(&bundle.Version)
		case "ref":
			err = decoder.Decode(&bundle.Ref)
		case "hash":
			err = decoder.Decode(&bundle.Hash)
		case "objects":
			token, err = decoder.Token()
			if err != nil {
				return nil, err
			}
			if token != json.Delim('[') {
				return nil, fmt.Errorf("expected objects array")
			}
			for decoder.More() {
				if len(bundle.Objects) == MaxTransferObjects {
					return nil, fmt.Errorf("too many objects (maximum %d)", MaxTransferObjects)
				}
				var object *Object
				if err = decoder.Decode(&object); err != nil {
					return nil, err
				}
				bundle.Objects = append(bundle.Objects, object)
			}
			_, err = decoder.Token() // closing array delimiter
		}
		if err != nil {
			return nil, err
		}
	}
	if _, err = decoder.Token(); err != nil { // closing object delimiter
		return nil, err
	}
	// Read only the next token, never decode an arbitrary trailing value into
	// interface{} (which could itself allocate an unbounded array of values).
	if _, err = decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one JSON bundle")
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("bundle exceeds %d bytes", MaxBundleBytes)
	}
	return bundle, nil
}
