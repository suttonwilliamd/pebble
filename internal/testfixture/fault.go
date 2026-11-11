package testfixture

import "io"

// FailAfter returns bytes up to offset, then the supplied error (including on a
// partial read). It never converts an injected error into EOF. offset=0 fails
// immediately; offset=len(data) fails with the final bytes. Larger offsets
// reach ordinary EOF. A nil fault or negative offset is a fixture programming error.
func FailAfter(data []byte, offset int, fault error) io.Reader {
	if offset < 0 || fault == nil {
		panic("testfixture: invalid reader fault")
	}
	return &faultReader{data: data, remaining: offset, fault: fault}
}

type faultReader struct {
	data      []byte
	remaining int
	fault     error
}

func (r *faultReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		return 0, r.fault
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	r.remaining -= n
	if r.remaining == 0 {
		return n, r.fault
	}
	return n, nil
}
