package server

import (
	"fmt"

	"github.com/wim-web/prepare-dev/internal/core"
	"github.com/wim-web/prepare-dev/internal/plugin"
)

// body validates a JSON request body field by field and collects the problems.
type body struct {
	m      *core.OMap
	issues []core.Issue
}

func (b *body) add(path, format string, args ...any) {
	b.issues = append(b.issues, core.Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (b *body) err() error {
	if len(b.issues) == 0 {
		return nil
	}
	return &core.ValidationError{Issues: b.issues}
}

func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case int64, float64:
		return "number"
	case []any:
		return "array"
	case *core.OMap:
		return "object"
	}
	return "unknown"
}

func (b *body) strict(allowed ...string) {
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	for _, k := range b.m.Keys() {
		if !ok[k] {
			b.add(k, "unrecognized key %q", k)
		}
	}
}

func (b *body) str(key string) string {
	v, ok := b.m.Get(key)
	if !ok {
		b.add(key, "expected string, received undefined")
		return ""
	}
	s, ok := v.(string)
	if !ok {
		b.add(key, "expected string, received %s", kindOf(v))
	}
	return s
}

func (b *body) strDef(key, def string) string {
	if !b.m.Has(key) {
		return def
	}
	return b.str(key)
}

func (b *body) optStr(key string) *string {
	if !b.m.Has(key) {
		return nil
	}
	s := b.str(key)
	return &s
}

func (b *body) nullableStr(key string) *string {
	v, ok := b.m.Get(key)
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		b.add(key, "expected string or null, received %s", kindOf(v))
		return nil
	}
	return &s
}

func (b *body) int(key string) int64 {
	v, ok := b.m.Get(key)
	if !ok {
		b.add(key, "expected number, received undefined")
		return 0
	}
	i, ok := core.AsInt(v)
	if !ok {
		b.add(key, "expected integer, received %s", kindOf(v))
	}
	return i
}

func (b *body) optInt(key string) *int64 {
	if !b.m.Has(key) {
		return nil
	}
	i := b.int(key)
	return &i
}

func (b *body) boolDef(key string, def bool) bool {
	v, ok := b.m.Get(key)
	if !ok {
		return def
	}
	x, ok := v.(bool)
	if !ok {
		b.add(key, "expected boolean, received %s", kindOf(v))
	}
	return x
}

func (b *body) enum(key string, vals ...string) string {
	s := b.str(key)
	for _, v := range vals {
		if s == v {
			return s
		}
	}
	if b.m.Has(key) {
		b.add(key, "expected one of %v", vals)
	}
	return s
}

func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func (b *body) strings(key string) []string {
	v, ok := b.m.Get(key)
	if !ok {
		b.add(key, "expected array, received undefined")
		return nil
	}
	out, ok := stringList(v)
	if !ok {
		b.add(key, "expected an array of strings")
	}
	return out
}

func (b *body) stringsDef(key string) []string {
	if !b.m.Has(key) {
		return []string{}
	}
	return b.strings(key)
}

func (b *body) recordDef(key string) *core.OMap {
	v, ok := b.m.Get(key)
	if !ok {
		return core.NewOMap()
	}
	m, ok := v.(*core.OMap)
	if !ok {
		b.add(key, "expected record, received %s", kindOf(v))
		return core.NewOMap()
	}
	return m
}

func (b *body) options(key string) []core.QuestionOption {
	v, ok := b.m.Get(key)
	if !ok {
		return []core.QuestionOption{}
	}
	list, ok := v.([]any)
	if !ok {
		b.add(key, "expected array, received %s", kindOf(v))
		return nil
	}
	out := []core.QuestionOption{}
	for i, x := range list {
		m, ok := x.(*core.OMap)
		if !ok {
			b.add(fmt.Sprintf("%s.%d", key, i), "expected object")
			continue
		}
		o := core.QuestionOption{}
		sub := &body{m: m}
		o.Key = sub.str("key")
		o.Label = sub.str("label")
		o.Summary = sub.optStr("summary")
		if m.Has("recommended") {
			r := sub.boolDef("recommended", false)
			o.Recommended = &r
		}
		o.Reason = sub.optStr("reason")
		for _, is := range sub.issues {
			b.add(fmt.Sprintf("%s.%d.%s", key, i, is.Path), "%s", is.Message)
		}
		out = append(out, o)
	}
	return out
}

func (b *body) createdBy(key string) core.CreatedBy {
	v, ok := b.m.Get(key)
	if !ok {
		return core.CreatedBy{}
	}
	m, ok := v.(*core.OMap)
	if !ok {
		b.add(key, "expected object, received %s", kindOf(v))
		return core.CreatedBy{}
	}
	sub := &body{m: m}
	cb := core.CreatedBy{Host: sub.optStr("host"), Session: sub.optStr("session")}
	for _, is := range sub.issues {
		b.add(key+"."+is.Path, "%s", is.Message)
	}
	return cb
}

// choice accepts an array of strings, a single string or null (absent is null).
func (b *body) choice(key string) []string {
	v, ok := b.m.Get(key)
	if !ok || v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return []string{s}
	}
	out, ok := stringList(v)
	if !ok {
		b.add(key, "expected string, array of strings or null")
		return nil
	}
	return out
}

func (b *body) decision(key string) core.DecisionDraft {
	v, ok := b.m.Get(key)
	if !ok {
		b.add(key, "expected object, received undefined")
		return core.DecisionDraft{}
	}
	m, ok := v.(*core.OMap)
	if !ok {
		b.add(key, "expected object, received %s", kindOf(v))
		return core.DecisionDraft{}
	}
	sub := &body{m: m}
	d := core.DecisionDraft{Title: sub.str("title"), Body: sub.str("body"), Chosen: sub.optStr("chosen"), Alternatives: sub.stringsDef("alternatives")}
	for _, is := range sub.issues {
		b.add(key+"."+is.Path, "%s", is.Message)
	}
	return d
}

func (b *body) claims(key string) []plugin.ClaimRequest {
	v, ok := b.m.Get(key)
	if !ok {
		b.add(key, "expected array, received undefined")
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		b.add(key, "expected array, received %s", kindOf(v))
		return nil
	}
	out := []plugin.ClaimRequest{}
	for i, x := range list {
		m, ok := x.(*core.OMap)
		if !ok {
			b.add(fmt.Sprintf("%s.%d", key, i), "expected object")
			continue
		}
		sub := &body{m: m}
		c := plugin.ClaimRequest{Spec: sub.str("spec"), Claim: sub.str("claim")}
		for _, is := range sub.issues {
			b.add(fmt.Sprintf("%s.%d.%s", key, i, is.Path), "%s", is.Message)
		}
		out = append(out, c)
	}
	return out
}
