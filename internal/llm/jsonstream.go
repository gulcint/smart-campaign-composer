package llm

import (
	"strings"
	"unicode/utf16"
)

// fieldScanner incrementally tokenizes a JSON document that arrives in arbitrary pieces
// (input_json_delta.partial_json) and returns the decoded text that falls into the string values
// at the wanted object paths ("sms.body", ...). It never fails: malformed input simply yields no
// text; the complete document is validated separately against the full schema.
type fieldScanner struct {
	wanted map[string]bool
	stack  []scanFrame

	inString  bool
	stringKey bool   // the current string is an object key
	keyBuf    []rune // key being read
	emitPath  string // path of the current value string if wanted, else ""
	esc       int    // 0 none, 1 after '\', 2 reading \u hex digits
	hexN      int    // \u digits read so far
	hexVal    rune   // value of the \u digits read so far
	highSurr  rune   // pending high surrogate from a previous \u escape
	out       []rune // text decoded in the current Feed for emitPath
	chunks    []ContentChunk
}

type scanFrame struct {
	object    bool
	key       string
	expectKey bool
}

func newFieldScanner(paths ...string) *fieldScanner {
	w := make(map[string]bool, len(paths))
	for _, p := range paths {
		w[p] = true
	}
	return &fieldScanner{wanted: w}
}

// Feed consumes the next piece and returns the chunks (non-empty text, in order) it completes.
func (s *fieldScanner) Feed(piece string) []ContentChunk {
	s.chunks = nil
	for _, r := range piece {
		if s.inString {
			s.stringRune(r)
			continue
		}
		switch r {
		case '{':
			s.stack = append(s.stack, scanFrame{object: true, expectKey: true})
		case '[':
			s.stack = append(s.stack, scanFrame{})
		case '}', ']':
			if len(s.stack) > 0 {
				s.stack = s.stack[:len(s.stack)-1]
			}
		case ':':
			if top := s.top(); top != nil && top.object {
				top.expectKey = false
			}
		case ',':
			if top := s.top(); top != nil && top.object {
				top.expectKey = true
			}
		case '"':
			s.inString = true
			s.esc, s.hexN, s.hexVal, s.highSurr = 0, 0, 0, 0
			top := s.top()
			s.stringKey = top != nil && top.object && top.expectKey
			s.keyBuf = s.keyBuf[:0]
			s.emitPath = ""
			if !s.stringKey {
				if p := s.path(); s.wanted[p] {
					s.emitPath = p
				}
			}
		}
	}
	s.flush()
	return s.chunks
}

func (s *fieldScanner) top() *scanFrame {
	if len(s.stack) == 0 {
		return nil
	}
	return &s.stack[len(s.stack)-1]
}

// path joins the current keys of all enclosing objects; "" if inside an array.
func (s *fieldScanner) path() string {
	parts := make([]string, 0, len(s.stack))
	for _, f := range s.stack {
		if !f.object {
			return ""
		}
		parts = append(parts, f.key)
	}
	return strings.Join(parts, ".")
}

func (s *fieldScanner) stringRune(r rune) {
	switch {
	case s.esc == 1:
		s.esc = 0
		switch r {
		case 'u':
			s.esc, s.hexN, s.hexVal = 2, 0, 0
			return
		case 'n':
			r = '\n'
		case 't':
			r = '\t'
		case 'r':
			r = '\r'
		case 'b':
			r = '\b'
		case 'f':
			r = '\f'
		}
		s.emit(r)
	case s.esc == 2:
		d := hexDigit(r)
		if d < 0 {
			s.esc = 0 // malformed escape: drop it
			return
		}
		s.hexVal = s.hexVal<<4 | d
		if s.hexN++; s.hexN < 4 {
			return
		}
		s.esc = 0
		u := s.hexVal
		switch {
		case utf16.IsSurrogate(u) && u < 0xDC00:
			s.highSurr = u
			return
		case utf16.IsSurrogate(u) && s.highSurr != 0:
			u = utf16.DecodeRune(s.highSurr, u)
		}
		s.highSurr = 0
		s.emit(u)
	case r == '\\':
		s.esc = 1
	case r == '"':
		s.inString = false
		if s.stringKey {
			if top := s.top(); top != nil {
				top.key = string(s.keyBuf)
			}
			return
		}
		s.flush()
	default:
		s.emit(r)
	}
}

func hexDigit(r rune) rune {
	switch {
	case r >= '0' && r <= '9':
		return r - '0'
	case r >= 'a' && r <= 'f':
		return r - 'a' + 10
	case r >= 'A' && r <= 'F':
		return r - 'A' + 10
	}
	return -1
}

func (s *fieldScanner) emit(r rune) {
	if s.stringKey {
		s.keyBuf = append(s.keyBuf, r)
		return
	}
	if s.emitPath != "" {
		s.out = append(s.out, r)
	}
}

func (s *fieldScanner) flush() {
	if len(s.out) > 0 && s.emitPath != "" {
		s.chunks = append(s.chunks, ContentChunk{Path: s.emitPath, Text: string(s.out)})
	}
	s.out = s.out[:0]
}
