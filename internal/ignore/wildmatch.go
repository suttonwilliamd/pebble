package ignore

import "strings"

type token struct {
	kind  byte
	value byte
	set   *[4]uint64
}
type segment struct {
	tokens []token
	valid  bool
}

func setByte(set *[4]uint64, c byte)      { set[c/64] |= uint64(1) << (c % 64) }
func hasByte(set *[4]uint64, c byte) bool { return set[c/64]&(uint64(1)<<(c%64)) != 0 }

func namedClass(name string, c byte) (bool, bool) {
	lower, upper, digit := c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9'
	switch name {
	case "alnum":
		return lower || upper || digit, true
	case "alpha":
		return lower || upper, true
	case "blank":
		return c == ' ' || c == '\t', true
	case "cntrl":
		return c < 32 || c == 127, true
	case "digit":
		return digit, true
	case "graph":
		return c > 32 && c < 127, true
	case "lower":
		return lower, true
	case "print":
		return c >= 32 && c < 127, true
	case "punct":
		return c > 32 && c < 127 && !lower && !upper && !digit, true
	case "space":
		return c == ' ' || c >= '\t' && c <= '\r', true
	case "upper":
		return upper, true
	case "xdigit":
		return digit || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F', true
	default:
		return false, false
	}
}

func characterClass(p string, start int) (*[4]uint64, int, bool) {
	set := new([4]uint64)
	i := start + 1
	negative := i < len(p) && (p[i] == '!' || p[i] == '^')
	if negative {
		i++
	}
	first := true
	for i < len(p) {
		if p[i] == ']' && !first {
			if negative {
				for n := range set {
					set[n] = ^set[n]
				}
			}
			return set, i, true
		}
		first = false
		if strings.HasPrefix(p[i:], "[:") {
			end := strings.Index(p[i+2:], ":]")
			if end < 0 {
				return nil, i, false
			}
			name := p[i+2 : i+2+end]
			for n := 0; n < 256; n++ {
				match, valid := namedClass(name, byte(n))
				if !valid {
					return nil, i, false
				}
				if match {
					setByte(set, byte(n))
				}
			}
			i += end + 4
			continue
		}
		c := p[i]
		if c == '\\' {
			i++
			if i == len(p) {
				return nil, i, false
			}
			c = p[i]
		}
		i++
		// Git retains the initial literal even when a range is reversed.
		setByte(set, c)
		if i+1 < len(p) && p[i] == '-' && p[i+1] != ']' {
			i++
			end := p[i]
			if end == '\\' {
				i++
				if i == len(p) {
					return nil, i, false
				}
				end = p[i]
			}
			for n := int(c); n <= int(end); n++ {
				setByte(set, byte(n))
			}
			i++
		}
	}
	return nil, i, false
}

func component(p string) *segment {
	s := &segment{valid: true}
	for i := 0; i < len(p); i++ {
		t := token{kind: 'l', value: p[i]}
		switch p[i] {
		case '*':
			t.kind = '*'
			for i+1 < len(p) && p[i+1] == '*' {
				i++
			}
		case '?':
			t.kind = '?'
		case '\\':
			i++
			if i == len(p) {
				s.valid = false
				return s
			}
			t.value = p[i]
		case '[':
			var ok bool
			t.kind = '['
			t.set, i, ok = characterClass(p, i)
			if !ok {
				s.valid = false
				return s
			}
		}
		s.tokens = append(s.tokens, t)
	}
	return s
}

func (s *segment) match(name string) bool {
	if !s.valid {
		return false
	}
	p, n, star, retry := 0, 0, -1, 0
	for n < len(name) {
		if p < len(s.tokens) {
			t := s.tokens[p]
			if t.kind == '*' {
				star, retry = p, n
				p++
				continue
			}
			if t.kind == '?' || t.kind == 'l' && t.value == name[n] || t.kind == '[' && hasByte(t.set, name[n]) {
				p++
				n++
				continue
			}
		}
		if star < 0 {
			return false
		}
		retry++
		n = retry
		p = star + 1
	}
	for p < len(s.tokens) && s.tokens[p].kind == '*' {
		p++
	}
	return p == len(s.tokens)
}
