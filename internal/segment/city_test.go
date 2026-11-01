package segment_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

// City spelling canonicalisation (REQ-AUD-01 C5, D66): segment.CanonicalCity and the rewrite that
// segment.Parse applies to city conditions before Validate and Compile.

// REQ-AUD-01 C5
// segment.CanonicalCity: NFC + trim (unicode.IsSpace), then the fold table decides whether the text
// is one of the ten canonical cities; otherwise the trimmed NFC text is returned unchanged. The first
// 21 rows are the criterion's examples verbatim; the rest exercise each fold-table row and the
// "rune not in the table is lowered with unicode.ToLower" rule.
func TestCanonicalCity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The 21 examples of the criterion.
		{name: "Istanbul with dotless capital I", in: "Istanbul", want: "İstanbul"},
		{name: "istanbul lower case", in: "istanbul", want: "İstanbul"},
		{name: "ISTANBUL upper case ASCII", in: "ISTANBUL", want: "İstanbul"},
		{name: "İSTANBUL upper case Turkish", in: "İSTANBUL", want: "İstanbul"},
		{name: "İstanbul canonical stays", in: "İstanbul", want: "İstanbul"},
		{name: "ıstanbul with dotless small i", in: "ıstanbul", want: "İstanbul"},
		{name: "i plus combining dot above", in: "i̇stanbul", want: "İstanbul"},
		{name: "NFD İstanbul becomes NFC", in: "İstanbul", want: "İstanbul"},
		{name: "izmir", in: "izmir", want: "İzmir"},
		{name: "IZMIR", in: "IZMIR", want: "İzmir"},
		{name: "ESKISEHIR", in: "ESKISEHIR", want: "Eskişehir"},
		{name: "eskişehir", in: "eskişehir", want: "Eskişehir"},
		{name: "Eskisehir", in: "Eskisehir", want: "Eskişehir"},
		{name: "gaziantep", in: "gaziantep", want: "Gaziantep"},
		{name: "Bursa with surrounding spaces", in: "  Bursa ", want: "Bursa"},
		{name: "Kars has no canonical form", in: "Kars", want: "Kars"},
		{name: "Kars with surrounding spaces is trimmed", in: "  Kars ", want: "Kars"},
		{name: "kars stays as written", in: "kars", want: "kars"},
		{name: "no substring match", in: "İstanbul'da", want: "İstanbul'da"},
		{name: "inner space is kept", in: "Is tanbul", want: "Is tanbul"},
		{name: "whitespace only becomes empty", in: "   ", want: ""},

		// Owner case and the remaining canonical cities.
		{name: "ankara", in: "ankara", want: "Ankara"},
		{name: "ANKARA", in: "ANKARA", want: "Ankara"},
		{name: "Ankara canonical stays", in: "Ankara", want: "Ankara"},
		{name: "ANTALYA", in: "ANTALYA", want: "Antalya"},
		{name: "adana", in: "adana", want: "Adana"},
		{name: "KONYA", in: "KONYA", want: "Konya"},
		{name: "KAYSERİ with dotted capital I", in: "KAYSERİ", want: "Kayseri"},
		{name: "GAZİANTEP", in: "GAZİANTEP", want: "Gaziantep"},
		{name: "bursa", in: "bursa", want: "Bursa"},

		// Fold table rows.
		{name: "ı and İ fold to i", in: "eskışehİr", want: "Eskişehir"},
		{name: "Î folds to i", in: "Îzmir", want: "İzmir"},
		{name: "î folds to i", in: "îzmir", want: "İzmir"},
		{name: "Ş folds to s", in: "ESKIŞEHIR", want: "Eskişehir"},
		{name: "ü folds to u", in: "bürsa", want: "Bursa"},
		{name: "Ü folds to u", in: "BÜRSA", want: "Bursa"},
		{name: "û folds to u", in: "bûrsa", want: "Bursa"},
		{name: "Û folds to u", in: "BÛRSA", want: "Bursa"},
		{name: "ö folds to o", in: "könya", want: "Konya"},
		{name: "Ö folds to o", in: "KÖNYA", want: "Konya"},
		{name: "ğ folds to g", in: "ğaziantep", want: "Gaziantep"},
		{name: "Ğ folds to g", in: "ĞAZIANTEP", want: "Gaziantep"},
		{name: "â folds to a", in: "ankâra", want: "Ankara"},
		{name: "Â folds to a", in: "ÂNKARA", want: "Ankara"},
		{name: "rune outside the table is not stripped", in: "Ankára", want: "Ankára"},

		// Trimming uses unicode.IsSpace.
		{name: "tab and newline are trimmed", in: "\tİzmir\n", want: "İzmir"},
		{name: "no-break space is trimmed", in: " Konya ", want: "Konya"},
		{name: "empty stays empty", in: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := segment.CanonicalCity(tc.in)
			if got != tc.want {
				t.Fatalf("CanonicalCity(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := segment.CanonicalCity(got); again != got {
				t.Fatalf("CanonicalCity(%q) = %q, want the canonical output %q unchanged", got, again, got)
			}
		})
	}
}

// REQ-AUD-01 C5
// segment.Parse rewrites every city value to its canonical spelling for eq, neq, in and not_in
// (each array element separately, also under any/not), de-duplicates arrays after the rewrite
// (first wins, order kept; values without a canonical form only when identical) and leaves other
// fields untouched. The rewritten rule is what Rule.MarshalJSON returns.
func TestParse_CanonicalizesCity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string // MarshalJSON of the parsed rule (root group)
	}{
		{
			name: "criterion example eq Istanbul",
			raw:  `{"rule":{"all":[{"field":"city","op":"eq","value":"Istanbul"}]}}`,
			want: `{"all":[{"field":"city","op":"eq","value":"İstanbul"}]}`,
		},
		{
			name: "eq ankara becomes Ankara",
			raw:  doc(all(cond("city", "eq", `"ankara"`))),
			want: all(cond("city", "eq", `"Ankara"`)),
		},
		{
			name: "neq ISTANBUL becomes İstanbul",
			raw:  doc(all(cond("city", "neq", `"ISTANBUL"`))),
			want: all(cond("city", "neq", `"İstanbul"`)),
		},
		{
			name: "eq Kars is kept as written",
			raw:  doc(all(cond("city", "eq", `"Kars"`))),
			want: all(cond("city", "eq", `"Kars"`)),
		},
		{
			name: "eq with surrounding spaces is trimmed",
			raw:  doc(all(cond("city", "eq", `"  Bursa "`))),
			want: all(cond("city", "eq", `"Bursa"`)),
		},
		{
			name: "criterion example in is rewritten and de-duplicated",
			raw:  `{"rule":{"all":[{"field":"city","op":"in","value":["Istanbul","Ankara","İstanbul","istanbul"]}]}}`,
			want: all(cond("city", "in", `["İstanbul","Ankara"]`)),
		},
		{
			name: "in keeps the order of first occurrences",
			raw:  doc(all(cond("city", "in", `["ankara","ISTANBUL","ANKARA","izmir","İstanbul"]`))),
			want: all(cond("city", "in", `["Ankara","İstanbul","İzmir"]`)),
		},
		{
			name: "in keeps Kars and kars as two elements",
			raw:  doc(all(cond("city", "in", `["Kars","kars"]`))),
			want: all(cond("city", "in", `["Kars","kars"]`)),
		},
		{
			name: "in drops an identical non-canonical value",
			raw:  doc(all(cond("city", "in", `["Kars"," Kars","Kars "]`))),
			want: all(cond("city", "in", `["Kars"]`)),
		},
		{
			name: "not_in is rewritten element by element",
			raw:  doc(all(cond("city", "not_in", `["izmir","IZMIR","Kars","kars"]`))),
			want: all(cond("city", "not_in", `["İzmir","Kars","kars"]`)),
		},
		{
			name: "city under any and not is rewritten",
			raw:  doc(all(anyOf(not(cond("city", "eq", `"ESKISEHIR"`)), cond("city", "in", `["gaziantep"]`)))),
			want: all(anyOf(not(cond("city", "eq", `"Eskişehir"`)), cond("city", "in", `["Gaziantep"]`))),
		},
		{
			name: "whitespace-only value becomes empty",
			raw:  doc(all(cond("city", "eq", `"   "`))),
			want: all(cond("city", "eq", `""`)),
		},
		{
			name: "other fields are not rewritten",
			raw:  doc(all(cond("language", "eq", `"TR"`), cond("loyalty_tier", "in", `["GOLD","gold"]`), cond("city", "eq", `"istanbul"`))),
			want: all(cond("language", "eq", `"TR"`), cond("loyalty_tier", "in", `["GOLD","gold"]`), cond("city", "eq", `"İstanbul"`)),
		},
		{
			name: "number value is left untouched",
			raw:  doc(all(cond("city", "eq", "5"))),
			want: all(cond("city", "eq", "5")),
		},
		{
			name: "array with a non-string element is left untouched",
			raw:  doc(all(cond("city", "in", `["istanbul",5,"istanbul"]`))),
			want: all(cond("city", "in", `["istanbul",5,"istanbul"]`)),
		},
		{
			name: "boolean array is left untouched",
			raw:  doc(all(cond("city", "not_in", `[true]`))),
			want: all(cond("city", "not_in", `[true]`)),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := segment.Parse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Parse(%s) error = %v, want nil", tc.raw, err)
			}
			got, err := r.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON() error = %v", err)
			}
			assertSameJSON(t, tc.want, got)
		})
	}
}

// REQ-AUD-01 C5
// segment.Validate runs on the rewritten rule: length (1..100) and array length (1..20) are checked
// after the rewrite and de-duplication; a whitespace-only value becomes "" and is invalid_value; a
// value of another type is left untouched and is still type_mismatch.
func TestValidate_AfterCityRewrite(t *testing.T) {
	t.Parallel()
	// i̇stanbul is a JSON escape: "i" + U+0307 COMBINING DOT ABOVE + "stanbul".
	seven := `"Istanbul","istanbul","ISTANBUL","İSTANBUL","İstanbul","ıstanbul","i̇stanbul"`
	twentyOneSpellings := "[" + seven + "," + seven + "," + seven + "]"
	if n := len(mustStrings(t, twentyOneSpellings)); n != 21 {
		t.Fatalf("test setup: %d spellings, want 21", n)
	}
	twentyDistinctPlusDuplicate := `["İstanbul","Ankara","İzmir","Bursa","Antalya","Adana","Konya","Gaziantep","Kayseri","Eskişehir",` +
		`"Kars","Van","Muş","Rize","Ordu","Sinop","Bolu","Uşak","Iğdır","Batman","istanbul"]`
	if n := len(mustStrings(t, twentyDistinctPlusDuplicate)); n != 21 {
		t.Fatalf("test setup: %d values, want 21", n)
	}
	tests := []struct {
		name string
		raw  string
		code string // "" => accepted
	}{
		{name: "21 spellings of one city de-duplicate to 1 and are accepted", raw: doc(all(cond("city", "in", twentyOneSpellings)))},
		{name: "21 values of which one is a duplicate after rewrite are accepted", raw: doc(all(cond("city", "not_in", twentyDistinctPlusDuplicate)))},
		{name: "100 chars after trimming are accepted", raw: doc(all(cond("city", "eq", jsonString("  "+strings.Repeat("ş", 100)+" "))))},
		{name: "1 char after trimming is accepted", raw: doc(all(cond("city", "eq", `" A "`)))},
		{name: "Kars is a valid city", raw: doc(all(cond("city", "eq", `"Kars"`)))},
		{name: "neq istanbul is accepted", raw: doc(all(cond("city", "neq", `"istanbul"`)))},
		{name: "whitespace-only eq value is invalid_value", raw: doc(all(cond("city", "eq", `"   "`))), code: "invalid_value"},
		{name: "whitespace-only neq value is invalid_value", raw: doc(all(cond("city", "neq", "\" \\t \""))), code: "invalid_value"},
		{name: "whitespace-only element of in is invalid_value", raw: doc(all(cond("city", "in", `["Ankara","   "]`))), code: "invalid_value"},
		{name: "number value is still type_mismatch", raw: doc(all(cond("city", "eq", "5"))), code: "type_mismatch"},
		{name: "array with a number element is still type_mismatch", raw: doc(all(cond("city", "in", `["istanbul",5]`))), code: "type_mismatch"},
		{name: "boolean in not_in is still type_mismatch", raw: doc(all(cond("city", "not_in", `[true]`))), code: "type_mismatch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := segment.Parse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Parse(%s) error = %v, want nil (structure is valid)", tc.raw, err)
			}
			err = segment.Validate(r)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("Validate(%s) error = %v, want nil", tc.raw, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%s) error = nil, want %s", tc.raw, tc.code)
			}
			wantSentinel := map[string]error{"invalid_value": validation.ErrInvalidValue, "type_mismatch": segment.ErrTypeMismatch}[tc.code]
			if !errors.Is(err, wantSentinel) {
				t.Fatalf("Validate(%s) error = %v, want errors.Is(%v)", tc.raw, err, wantSentinel)
			}
			found := false
			for _, fe := range fieldErrors(t, err) {
				if fe.Code == tc.code && strings.HasPrefix(fe.Path, "rule.all[0].value") {
					found = true
				}
			}
			if !found {
				t.Fatalf("field errors = %+v, want code %q at rule.all[0].value", pathCodes(fieldErrors(t, err)), tc.code)
			}
		})
	}
}

// assertSameJSON compares two JSON texts semantically.
func assertSameJSON(t *testing.T, want string, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("test setup: bad want JSON %s: %v", want, err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("output is not JSON (%v): %s", err, got)
	}
	if diff := cmp.Diff(w, g); diff != "" {
		t.Fatalf("JSON mismatch (-want +got):\n%s\ngot: %s", diff, got)
	}
}

// mustStrings decodes a JSON array of strings (test setup only).
func mustStrings(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("test setup: %s: %v", raw, err)
	}
	return out
}
