package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Decoding and encoding question files again must give the same bytes, so that
// rewriting a question changes only what changed (a question written before
// "cancelled" existed gets "cancelled": null, as in the TypeScript version). Run files
// and journal lines are never rewritten: reading them must lose no field, which the API
// (verify get, trace) and recovery rely on; how they are written may differ.
func TestQuestionAndRunFilesRoundTrip(t *testing.T) {
	for _, r := range compatRoots {
		t.Run("[D1] "+r.name, func(t *testing.T) { questionAndRunFilesRoundTrip(t, r.dir) })
	}
}

func questionAndRunFilesRoundTrip(t *testing.T, root string) {
	check := func(pattern string, decode func([]byte) (any, error)) int {
		files, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			v, err := decode(data)
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			out, err := PrettyJSON(v)
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			// Files written before a key was added to the schema get its default on the
			// next write, in both implementations: compare with that.
			want := string(data)
			if strings.Contains(pattern, "questions") {
				raw, _ := DecodeJSON(data)
				if m, ok := raw.(*OMap); ok && !m.Has("cancelled") {
					m.Set("cancelled", nil)
					b, _ := PrettyJSON(m)
					want = string(b) + "\n"
				}
			}
			if string(out)+"\n" != want {
				firstDiff(t, f, want, string(out)+"\n")
			}
		}
		return len(files)
	}
	nq := check(filepath.Join("questions", "*.json"), func(b []byte) (any, error) {
		var q Question
		if err := json.Unmarshal(b, &q); err != nil {
			return nil, err
		}
		return &q, q.Validate()
	})
	nr := checkNoLoss(t, root, filepath.Join("runs", "*.json"), func(b []byte) (any, error) {
		var r RunRecord
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		return &r, r.Validate()
	})
	if nq == 0 || nr == 0 {
		t.Fatalf("no question or run files in %s", root)
	}
	t.Logf("checked %d question files and %d run files", nq, nr)
}

func TestJournalLinesRoundTrip(t *testing.T) {
	for _, r := range compatRoots {
		t.Run("[D1] "+r.name, func(t *testing.T) { journalRoundTrip(t, r.dir) })
	}
}

func journalRoundTrip(t *testing.T, root string) {
	data, err := os.ReadFile(filepath.Join(root, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		out, err := ev.MarshalJSON()
		if err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if lost := lostFields([]byte(line), out); lost != "" {
			t.Fatalf("line %d loses %s when read:\n  file %s\n  read %s", i+1, lost, line, out)
		}
		n++
	}
	t.Logf("checked %d journal lines", n)
}

func firstDiff(t *testing.T, name, want, got string) {
	t.Helper()
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			t.Errorf("%s line %d:\n  want %q\n  got  %q", name, i+1, x, y)
			return
		}
	}
}

// checkNoLoss decodes each file and fails if a field of the file is missing or
// different in what the decoded value encodes to.
func checkNoLoss(t *testing.T, root, pattern string, decode func([]byte) (any, error)) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(root, pattern))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		v, err := decode(data)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		out, err := MarshalJSONNoEscape(v)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if lost := lostFields(data, out); lost != "" {
			t.Errorf("%s loses %s when read", f, lost)
		}
	}
	return len(files)
}

// lostFields names the first value of the JSON document orig that is missing or
// different in got ("" when every value is kept). Fields only in got are allowed.
func lostFields(orig, got []byte) string {
	var a, b any
	if err := json.Unmarshal(orig, &a); err != nil {
		return "everything (" + err.Error() + ")"
	}
	if err := json.Unmarshal(got, &b); err != nil {
		return "everything (" + err.Error() + ")"
	}
	return contained("$", a, b)
}

func contained(path string, a, b any) string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return path
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, ok := y[k]
			if !ok {
				return path + "." + k
			}
			if lost := contained(path+"."+k, x[k], v); lost != "" {
				return lost
			}
		}
		return ""
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return path
		}
		for i := range x {
			if lost := contained(fmt.Sprintf("%s[%d]", path, i), x[i], y[i]); lost != "" {
				return lost
			}
		}
		return ""
	default:
		if !reflect.DeepEqual(a, b) {
			return path
		}
		return ""
	}
}
