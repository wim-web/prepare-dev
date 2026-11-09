package core

import (
	"fmt"
	"math"
	"strings"
)

// The frontmatter schemas follow the zod schemas of the original implementation:
// the base keys come first, then the keys of the kind, then unknown keys in input
// order (records keep unknown keys; nested objects drop them). Defaults are filled
// in when a key is absent; null is only accepted where the schema allows it.

// RecordKinds lists the record kinds in the order of their directories.
var RecordKinds = []string{"discovery", "requirement", "decision", "design", "spec", "review", "record", "proposal"}

// KindDirs maps a record kind to its directory under the records dir.
var KindDirs = map[string]string{
	"discovery":   "discovery",
	"requirement": "requirements",
	"decision":    "decisions",
	"design":      "designs",
	"spec":        "specs",
	"review":      "reviews",
	"record":      "records",
	"proposal":    "proposals",
}

// IsRecordKind reports whether k is one of the eight record kinds.
func IsRecordKind(k string) bool {
	_, ok := KindDirs[k]
	return ok
}

// Issue is one validation problem.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type validator struct{ issues []Issue }

func (v *validator) add(path, format string, args ...any) {
	v.issues = append(v.issues, Issue{Path: strings.TrimPrefix(path, "."), Message: fmt.Sprintf(format, args...)})
}

// ValidationError lists the problems found by a schema.
type ValidationError struct{ Issues []Issue }

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "invalid"
	}
	first := e.Issues[0]
	msg := first.Message
	if first.Path != "" {
		msg = first.Path + ": " + msg
	}
	if len(e.Issues) > 1 {
		msg += fmt.Sprintf(" (and %d more)", len(e.Issues)-1)
	}
	return msg
}

// conv converts one field. present tells whether the key exists; the second result
// tells whether the key appears in the output.
type conv func(v *validator, path string, val any, present bool) (any, bool)

type field struct {
	key string
	c   conv
}

func typeName(val any) string {
	switch val.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case int64, float64, int:
		return "number"
	case []any:
		return "array"
	case *OMap:
		return "object"
	}
	return fmt.Sprintf("%T", val)
}

func required(v *validator, path, want string) (any, bool) {
	v.add(path, "expected %s, received undefined", want)
	return nil, false
}

func strConv(optional bool, def *string) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present {
			if def != nil {
				return *def, true
			}
			if optional {
				return nil, false
			}
			return required(v, path, "string")
		}
		s, ok := val.(string)
		if !ok {
			v.add(path, "expected string, received %s", typeName(val))
			return nil, false
		}
		return s, true
	}
}

func str() conv                { return strConv(false, nil) }
func optStr() conv             { return strConv(true, nil) }
func strDef(d string) conv     { return strConv(false, &d) }
func enum(vals ...string) conv { return enumConv(nil, vals) }
func enumDef(d string, vals ...string) conv {
	return enumConv(&d, vals)
}

func enumConv(def *string, vals []string) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present {
			if def != nil {
				return *def, true
			}
			return required(v, path, "one of "+strings.Join(vals, ", "))
		}
		s, ok := val.(string)
		if ok {
			for _, x := range vals {
				if s == x {
					return s, true
				}
			}
		}
		v.add(path, "expected one of %s", strings.Join(vals, "|"))
		return nil, false
	}
}

func literal(want string) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if s, ok := val.(string); present && ok && s == want {
			return s, true
		}
		v.add(path, "expected %q", want)
		return nil, false
	}
}

// AsInt returns an integral number as int64.
func AsInt(val any) (int64, bool) {
	switch t := val.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return int64(t), true
		}
	}
	return 0, false
}

func intConv(min int64, def *int64) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present {
			if def != nil {
				return *def, true
			}
			return required(v, path, "number")
		}
		i, ok := AsInt(val)
		if !ok {
			v.add(path, "expected integer, received %s", typeName(val))
			return nil, false
		}
		if i < min {
			v.add(path, "too small: expected number to be >=%d", min)
			return nil, false
		}
		return i, true
	}
}

func intMin(min int64) conv { return intConv(min, nil) }
func intMinDef(min, d int64) conv {
	return intConv(min, &d)
}

func boolOpt() conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present {
			return nil, false
		}
		b, ok := val.(bool)
		if !ok {
			v.add(path, "expected boolean, received %s", typeName(val))
			return nil, false
		}
		return b, true
	}
}

// nullableDefNull accepts null or the inner type; absent becomes null.
func nullableDefNull(inner conv) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present || val == nil {
			return nil, true
		}
		return inner(v, path, val, true)
	}
}

func arrConv(elem conv, def bool) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present {
			if def {
				return []any{}, true
			}
			return required(v, path, "array")
		}
		a, ok := val.([]any)
		if !ok {
			v.add(path, "expected array, received %s", typeName(val))
			return nil, false
		}
		out := make([]any, 0, len(a))
		for i, x := range a {
			r, ok := elem(v, fmt.Sprintf("%s.%d", path, i), x, true)
			if ok {
				out = append(out, r)
			}
		}
		return out, true
	}
}

func arr(elem conv) conv    { return arrConv(elem, false) }
func arrDef(elem conv) conv { return arrConv(elem, true) }

func recDef(elem conv) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present {
			return NewOMap(), true
		}
		m, ok := val.(*OMap)
		if !ok {
			v.add(path, "expected record, received %s", typeName(val))
			return nil, false
		}
		out := NewOMap()
		for _, k := range m.keys {
			r, ok := elem(v, path+"."+k, m.vals[k], true)
			if ok {
				out.Set(k, r)
			}
		}
		return out, true
	}
}

func anyConv() conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		return CloneValue(val), present
	}
}

// objConv builds an object from fields; loose keeps unknown keys.
func objConv(fields []field, loose bool, def func() *OMap) conv {
	return func(v *validator, path string, val any, present bool) (any, bool) {
		if !present {
			if def != nil {
				val = def()
			} else {
				return required(v, path, "object")
			}
		}
		m, ok := val.(*OMap)
		if !ok || m == nil {
			v.add(path, "expected object, received %s", typeName(val))
			return nil, false
		}
		out := NewOMap()
		known := map[string]bool{}
		for _, f := range fields {
			known[f.key] = true
			fv, fp := m.Get(f.key)
			if r, ok := f.c(v, path+"."+f.key, fv, fp); ok {
				out.Set(f.key, r)
			}
		}
		if loose {
			for _, k := range m.keys {
				if !known[k] {
					out.Set(k, CloneValue(m.vals[k]))
				}
			}
		}
		return out, true
	}
}

func obj(fields ...field) conv { return objConv(fields, false, nil) }

// ---------- schemas ----------

var coverageState = []string{"decided", "hypothesis", "unverified", "out_of_scope"}

var subjectRefConv = obj(
	field{"id", str()},
	field{"rev", intMin(1)},
	field{"sha256", str()},
)

var baseFields = []field{
	{"id", str()},
	{"kind", enum(RecordKinds...)},
	{"title", str()},
	{"status", enumDef("draft", "draft", "proposed", "accepted", "rejected", "superseded")},
	{"rev", intMinDef(1, 1)},
	{"created", str()},
	{"updated", str()},
	{"links", recDef(arr(str()))},
}

// extend replaces fields with the same key in place and appends the others.
func extend(base []field, ext ...field) []field {
	out := append([]field(nil), base...)
	for _, e := range ext {
		replaced := false
		for i := range out {
			if out[i].key == e.key {
				out[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
}

var findingConv = obj(
	field{"id", str()},
	field{"severity", enum("blocker", "major", "nit")},
	field{"scope", str()},
	field{"summary", str()},
	field{"rationale", strDef("")},
	field{"proposed", enum("fix", "contest", "escalate", "decline")},
	field{"proposed_note", strDef("")},
	field{"actual", enumDef("unaddressed", "unaddressed", "fixed", "contested_accepted", "escalated", "declined", "accepted_risk")},
	field{"actual_ref", nullableDefNull(str())},
)

var kindFields = map[string][]field{
	"decision": extend(baseFields,
		field{"kind", literal("decision")},
		field{"question", optStr()},
		field{"answer", optStr()},
		field{"txid", optStr()},
		field{"chosen", optStr()},
		field{"alternatives", arrDef(str())},
		field{"reviewed", arrDef(subjectRefConv)},
	),
	"spec": extend(baseFields,
		field{"kind", literal("spec")},
		field{"claims", arrDef(obj(
			field{"id", str()},
			field{"statement", str()},
			field{"requirement", optStr()},
			field{"native", arrDef(obj(field{"plugin", str()}, field{"ref", str()}))},
		))},
		field{"assumptions", arrDef(str())},
		field{"out_of_scope", arrDef(str())},
	),
	"requirement": extend(baseFields,
		field{"kind", literal("requirement")},
		field{"priority", enumDef("must", "must", "should", "could", "wont")},
		field{"acceptance", arrDef(str())},
		field{"coverage", recDef(enum(coverageState...))},
	),
	"discovery": extend(baseFields,
		field{"kind", literal("discovery")},
		field{"problem", strDef("")},
		field{"stakeholders", arrDef(str())},
		field{"hypotheses", arrDef(obj(
			field{"id", str()},
			field{"statement", str()},
			field{"evidence", strDef("")},
			field{"state", enumDef("hypothesis", coverageState...)},
		))},
		field{"non_goals", arrDef(str())},
		field{"coverage", recDef(enum(coverageState...))},
	),
	"design": extend(baseFields,
		field{"kind", literal("design")},
		field{"alternatives", arrDef(obj(
			field{"key", str()},
			field{"title", str()},
			field{"status", enumDef("open", "chosen", "rejected", "open")},
			field{"reason", strDef("")},
		))},
		field{"chosen", nullableDefNull(str())},
		field{"tradeoffs", arrDef(str())},
		field{"new_decisions", arrDef(str())},
		field{"coverage", recDef(enum(coverageState...))},
	),
	"review": extend(baseFields,
		field{"kind", literal("review")},
		field{"stage", enum("zoom_out", "comprehension", "design_critique", "implementation_readiness", "custom")},
		field{"subjects", arr(subjectRefConv)},
		field{"inputs", arrDef(subjectRefConv)},
		field{"reviewer", obj(
			field{"persona", str()},
			field{"persona_version", str()},
			field{"host", strDef("unknown")},
			field{"context", enumDef("fresh", "fresh", "shared")},
		)},
		field{"paraphrase", nullableDefNull(str())},
		field{"findings", arrDef(findingConv)},
	),
}

func runSchema(fields []field, fm *OMap) (*OMap, error) {
	var v validator
	out, ok := objConv(fields, true, nil)(&v, "", fm, true)
	if len(v.issues) > 0 || !ok {
		return nil, &ValidationError{Issues: v.issues}
	}
	return out.(*OMap), nil
}

// ParseBaseFrontmatter validates the base keys only (used when reading a record).
func ParseBaseFrontmatter(fm *OMap) (*OMap, error) { return runSchema(baseFields, fm) }

// ValidateFrontmatter validates the base keys and then the schema of the kind.
func ValidateFrontmatter(fm *OMap) (*OMap, error) {
	base, err := runSchema(baseFields, fm)
	if err != nil {
		return nil, err
	}
	kind, _ := base.Str("kind")
	fields, ok := kindFields[kind]
	if !ok {
		return base, nil
	}
	return runSchema(fields, fm)
}
