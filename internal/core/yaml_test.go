package core

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compatRoots are the record trees the round-trip tests read: testdata/ts-342bc64 is a
// fixed copy of what the TypeScript version (npm "yaml", JSON.stringify) wrote. Records
// this version writes are checked when they are written (SerializeMarkdownChecked), so
// the tests do not read the changing prepare/ of this repository.
var compatRoots = []struct{ name, dir string }{
	{"written by the TypeScript version (342bc64)", filepath.Join("testdata", "ts-342bc64")},
}

// Reading and writing a record again must give the same bytes, so that rewriting a
// record changes only the fields that changed.
func TestRecordsRoundTripByteForByte(t *testing.T) {
	for _, r := range compatRoots {
		t.Run("[D1] "+r.name, func(t *testing.T) { recordsRoundTrip(t, r.dir) })
	}
}

func recordsRoundTrip(t *testing.T, root string) {
	files, _ := filepath.Glob(filepath.Join(root, "*", "*.md"))
	if len(files) == 0 {
		t.Fatalf("no records found in %s", root)
	}
	checked := 0
	defer func() { t.Logf("checked %d record files", checked) }()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		fm, body, err := ParseMarkdown(text)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		checked++
		out := SerializeMarkdown(fm, body)
		if out != text {
			a, b := strings.Split(text, "\n"), strings.Split(out, "\n")
			for i := 0; i < len(a) || i < len(b); i++ {
				var x, y string
				if i < len(a) {
					x = a[i]
				}
				if i < len(b) {
					y = b[i]
				}
				if x != y {
					t.Errorf("%s line %d:\n  want %q\n  got  %q", f, i+1, x, y)
					break
				}
			}
		}
	}
}

func TestPlainScalarResolution(t *testing.T) {
	cases := map[string]any{
		"":           nil,
		"~":          nil,
		"null":       nil,
		"true":       true,
		"False":      false,
		"yes":        "yes",
		"2026-11-08": "2026-11-08",
		"12":         int64(12),
		"0o17":       int64(15),
		"0x1f":       int64(31),
		"1.5":        1.5,
		"3.0":        int64(3),
		"1e3":        int64(1000),
		"abc":        "abc",
	}
	for in, want := range cases {
		got := ResolvePlainScalar(in)
		if got != want {
			t.Errorf("%q: got %#v want %#v", in, got, want)
		}
	}
	if f, ok := ResolvePlainScalar(".inf").(float64); !ok || !math.IsInf(f, 1) {
		t.Errorf(".inf")
	}
}

func TestEmitQuoting(t *testing.T) {
	m := OM(
		"a", "2026-11-08",
		"b", "123",
		"c", "x: y",
		"d", "#x",
		"e", "",
		"f", "line1\nline2",
		"g", "line1\nline2\n",
		"h", "true",
		"i", `say "hi"`,
		"i2", `"quoted" start`,
		"j", []any{},
		"k", NewOMap(),
		"l", nil,
		"m", []any{OM("x", int64(1), "y", "z")},
		"n", "日本語のテキスト",
	)
	got := EmitYAML(m)
	want := strings.Join([]string{
		"a: 2026-11-08",
		`b: "123"`,
		`c: "x: y"`,
		`d: "#x"`,
		`e: ""`,
		"f: |-",
		"  line1",
		"  line2",
		"g: |",
		"  line1",
		"  line2",
		`h: "true"`,
		`i: say "hi"`,
		`i2: '"quoted" start'`,
		"j: []",
		"k: {}",
		"l: null",
		"m:",
		"  - x: 1",
		"    y: z",
		"n: 日本語のテキスト",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	back, err := ParseYAML(got)
	if err != nil {
		t.Fatal(err)
	}
	if EmitYAML(back) != got {
		t.Errorf("round trip changed the output")
	}
}

// Strings with characters a YAML 1.1 reader takes as line breaks (or that are not
// printable) are written escaped and read back unchanged.
func TestLineSeparatorsRoundTrip(t *testing.T) {
	t.Run("[D1] strings with line separators and control characters read back unchanged", lineSeparatorsRoundTrip)
}

func lineSeparatorsRoundTrip(t *testing.T) {
	ls, ps, bom := string(rune(0x2028)), string(rune(0x2029)), string(rune(0xfeff))
	for _, v := range []string{"a" + ls + "b", "a" + ps + "b", "x\u0085y", "del\u007f", "c1\u0090", "bom" + bom, "multi\nline" + ls} {
		fm := NewOMap()
		fm.Set("id", "REQ-0001")
		fm.Set("title", v)
		fm.Set("list", []any{v})
		text, err := SerializeMarkdownChecked(fm, "body\n")
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		back, _, err := ParseMarkdown(text)
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if got, _ := back.Str("title"); got != v {
			t.Fatalf("%q read back as %q from\n%s", v, got, text)
		}
	}
}
