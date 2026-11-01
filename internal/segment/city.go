package segment

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// canonicalCities are the seed's city spellings (segment-dsl.md §2); customers.city holds only these.
var canonicalCities = []string{
	"İstanbul", "Ankara", "İzmir", "Bursa", "Antalya", "Adana", "Konya", "Gaziantep", "Kayseri", "Eskişehir",
}

// canonicalByFold maps the fold of each canonical city to its spelling.
var canonicalByFold = func() map[string]string {
	m := make(map[string]string, len(canonicalCities))
	for _, c := range canonicalCities {
		m[foldCity(c)] = c
	}
	return m
}()

// CanonicalCity returns the canonical seed spelling when the fold of s (NFC, TrimSpace, then the
// rune table of segment-dsl.md §2.1) equals the fold of a canonical city; otherwise s after NFC +
// TrimSpace. Never fails: "ISTANBUL" -> "İstanbul", "  Bursa " -> "Bursa", "Kars" -> "Kars".
func CanonicalCity(s string) string {
	s = strings.TrimSpace(norm.NFC.String(s))
	if c, ok := canonicalByFold[foldCity(s)]; ok {
		return c
	}
	return s
}

// foldCity is the comparison key of segment-dsl.md §2.1: a fixed rune table (independent of
// locale and case order, so I/ı/İ/i all fold to i), U+0307 dropped, every other rune
// unicode.ToLower. Inner spaces are kept. Never stored or shown.
func foldCity(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'I', 'i', 'İ', 'ı', 'Î', 'î':
			b.WriteRune('i')
		case 'Ş', 'ş':
			b.WriteRune('s')
		case 'Ğ', 'ğ':
			b.WriteRune('g')
		case 'Ü', 'ü', 'Û', 'û':
			b.WriteRune('u')
		case 'Ö', 'ö':
			b.WriteRune('o')
		case 'Ç', 'ç':
			b.WriteRune('c')
		case 'Â', 'â':
			b.WriteRune('a')
		case '̇':
			// combining dot above: "i̇" (i + U+0307) does not compose under NFC.
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// canonicaliseCities rewrites, in place, the value of every city condition with op eq/neq/in/not_in
// (segment-dsl.md §2.1 step 3): a string becomes CanonicalCity(string); an array of only strings
// becomes CanonicalCity of each element with exact duplicates removed (first kept, order kept).
// Any other value is left for Validate to report.
func canonicaliseCities(n *Node) {
	for i := range n.All {
		canonicaliseCities(&n.All[i])
	}
	for i := range n.Any {
		canonicaliseCities(&n.Any[i])
	}
	if n.Not != nil {
		canonicaliseCities(n.Not)
	}
	c := n.Cond
	if c == nil || c.Field != "city" || !slices.Contains(stringOps, c.Op) {
		return
	}
	if s, ok := asString(c.Value); ok {
		if out, err := json.Marshal(CanonicalCity(s)); err == nil {
			c.Value = out
		}
		return
	}
	if jsonKind(c.Value) != '[' {
		return
	}
	var items []json.RawMessage
	if json.Unmarshal(c.Value, &items) != nil {
		return
	}
	list := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := asString(it)
		if !ok {
			return
		}
		if s = CanonicalCity(s); !slices.Contains(list, s) {
			list = append(list, s)
		}
	}
	if out, err := json.Marshal(list); err == nil {
		c.Value = out
	}
}
