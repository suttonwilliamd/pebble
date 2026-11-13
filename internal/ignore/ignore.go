// Package ignore implements repository-local Git-style ignore rules. I/O is
// supplied by the caller so the repository can retain its anchored read policy.
package ignore

import (
	"fmt"
	"path"
	"strings"
)

// Result identifies the last applicable rule, or the excluded ancestor that
// prevents reaching the requested path. Metadata protection cannot be negated.
type Result struct {
	Source  string
	Line    int
	Pattern string
	Ignored bool
}

type rule struct {
	Result
	basename, directory bool
	parts               []*segment // nil is a whole-component **
}

type Matcher struct {
	read   func(string) ([]byte, error)
	layers map[string][]rule
}

// New expects read to return nil, nil for absent rule files, never for unsafe I/O.
func New(read func(string) ([]byte, error)) *Matcher {
	return &Matcher{read: read, layers: make(map[string][]rule)}
}

func trimTrailingSpaces(s string) string {
	for strings.HasSuffix(s, " ") {
		n := len(s) - 2
		for n >= 0 && s[n] == '\\' {
			n--
		}
		if (len(s)-2-n)%2 == 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func parse(source string, data []byte) []rule {
	var rules []rule
	for n, line := range strings.Split(strings.TrimPrefix(string(data), "\xef\xbb\xbf"), "\n") {
		line = trimTrailingSpaces(strings.TrimSuffix(line, "\r"))
		if line == "" || line[0] == '#' {
			continue
		}
		r := rule{Result: Result{Source: source, Line: n + 1, Pattern: line, Ignored: true}}
		pattern := line
		if pattern[0] == '!' {
			r.Ignored = false
			pattern = pattern[1:]
		}
		if pattern == "" {
			continue
		}
		r.directory = strings.HasSuffix(pattern, "/")
		pattern = strings.TrimSuffix(pattern, "/")
		r.basename = !strings.Contains(pattern, "/")
		pattern = strings.TrimPrefix(pattern, "/")
		if pattern == "" {
			continue
		}
		parts := strings.Split(pattern, "/")
		for i, part := range parts {
			// An escaped separator is still a separator in Git wildmatch.
			if i < len(parts)-1 {
				n := len(part)
				for n > 0 && part[n-1] == '\\' {
					n--
				}
				if (len(part)-n)%2 == 1 {
					part = part[:len(part)-1]
				}
			}
			if len(part) >= 2 && strings.Trim(part, "*") == "" {
				r.parts = append(r.parts, nil)
			} else {
				r.parts = append(r.parts, component(part))
			}
		}
		rules = append(rules, r)
	}
	return rules
}

func matchParts(pattern []*segment, names []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == nil {
			if len(pattern) == 1 {
				return len(names) > 0
			}
			for n := 0; n <= len(names); n++ {
				if matchParts(pattern[1:], names[n:]) {
					return true
				}
			}
			return false
		}
		if len(names) == 0 || !pattern[0].match(names[0]) {
			return false
		}
		pattern, names = pattern[1:], names[1:]
	}
	return len(names) == 0
}

func (m *Matcher) load(dir string) ([]rule, error) {
	if rules, ok := m.layers[dir]; ok {
		return rules, nil
	}
	var rules []rule
	for _, file := range []string{".gitignore", ".pebbleignore"} {
		source := path.Join(dir, file)
		data, err := m.read(source)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		rules = append(rules, parse(source, data)...)
	}
	m.layers[dir] = rules
	return rules, nil
}

// Check evaluates a slash-separated root-relative path, including every parent
// before loading child rules. Thus !parent/file cannot reopen excluded parent/.
func (m *Matcher) Check(name string, isDir bool) (Result, error) {
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if strings.EqualFold(part, ".pebble") || strings.EqualFold(part, ".git") {
			return Result{Source: "<metadata>", Pattern: part + "/", Ignored: true}, nil
		}
	}
	var layers [][]rule
	dir := ""
	var result Result
	for i := range parts {
		rules, err := m.load(dir)
		if err != nil {
			return Result{}, err
		}
		layers = append(layers, rules)
		result = Result{}
		directory := i < len(parts)-1 || isDir
		candidate := strings.Join(parts[:i+1], "/")
		for depth, layer := range layers {
			for _, r := range layer {
				if r.directory && !directory {
					continue
				}
				names := parts[depth : i+1]
				if r.basename {
					names = parts[i : i+1]
				}
				if matchParts(r.parts, names) {
					result = r.Result
				}
			}
		}
		if result.Ignored {
			return result, nil
		}
		dir = candidate
	}
	return result, nil
}
