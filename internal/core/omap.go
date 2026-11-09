// Package core holds the data model shared by the runtime, the CLI and the plugins:
// ordered maps, frontmatter, record schemas, journal events, ids and hashing.
package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// OMap is a JSON/YAML object that keeps its keys in insertion order, the way a
// JavaScript object does. Values are string, int64, float64, bool, nil, []any or *OMap.
type OMap struct {
	keys []string
	vals map[string]any
}

// NewOMap returns an empty ordered map.
func NewOMap() *OMap { return &OMap{vals: map[string]any{}} }

// OM builds an ordered map from alternating keys and values.
func OM(kv ...any) *OMap {
	m := NewOMap()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

func (o *OMap) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Keys returns the keys in order.
func (o *OMap) Keys() []string {
	if o == nil {
		return nil
	}
	return append([]string(nil), o.keys...)
}

func (o *OMap) Get(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[k]
	return v, ok
}

func (o *OMap) Has(k string) bool {
	_, ok := o.Get(k)
	return ok
}

// Set keeps the position of an existing key and appends a new one, like assigning
// a property of a JavaScript object.
func (o *OMap) Set(k string, v any) {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *OMap) Delete(k string) {
	if o == nil {
		return
	}
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// Str returns the value of k when it is a string.
func (o *OMap) Str(k string) (string, bool) {
	v, ok := o.Get(k)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// Map returns the value of k when it is an ordered map.
func (o *OMap) Map(k string) (*OMap, bool) {
	v, ok := o.Get(k)
	if !ok {
		return nil, false
	}
	m, ok := v.(*OMap)
	return m, ok
}

// Clone copies the map and every nested map and slice.
func (o *OMap) Clone() *OMap {
	if o == nil {
		return nil
	}
	return CloneValue(o).(*OMap)
}

// Merge returns a copy of o with the entries of other assigned over it ({...o, ...other}).
func (o *OMap) Merge(other *OMap) *OMap {
	out := o.Clone()
	if out == nil {
		out = NewOMap()
	}
	for _, k := range other.Keys() {
		v, _ := other.Get(k)
		out.Set(k, CloneValue(v))
	}
	return out
}

// CloneValue deep-copies an ordered-map value.
func CloneValue(v any) any {
	switch t := v.(type) {
	case *OMap:
		if t == nil {
			return (*OMap)(nil)
		}
		out := NewOMap()
		for _, k := range t.keys {
			out.Set(k, CloneValue(t.vals[k]))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = CloneValue(x)
		}
		return out
	default:
		return v
	}
}

// MarshalJSON writes the keys in order without HTML escaping.
func (o *OMap) MarshalJSON() ([]byte, error) {
	if o == nil {
		return []byte("null"), nil
	}
	var b bytes.Buffer
	if err := writeJSONValue(&b, o); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// UnmarshalJSON reads an object keeping its key order.
func (o *OMap) UnmarshalJSON(data []byte) error {
	v, err := DecodeJSON(data)
	if err != nil {
		return err
	}
	m, ok := v.(*OMap)
	if !ok {
		return fmt.Errorf("expected a JSON object")
	}
	*o = *m
	return nil
}

// DecodeJSON parses JSON into ordered-map values (objects become *OMap, numbers
// become int64 when integral and float64 otherwise).
func DecodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("unexpected data after JSON value")
	}
	return v, nil
}

func decodeJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := NewOMap()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("object key is not a string")
				}
				val, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				m.Set(key, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return m, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	case json.Number:
		return numberValue(string(t)), nil
	default:
		return t, nil // string, bool, nil
	}
}

// numberValue converts a JSON number to int64 when it is integral, like a JavaScript number.
func numberValue(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return normalizeFloat(f)
}

func normalizeFloat(f float64) any {
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 && !(f == 0 && math.Signbit(f)) {
		return int64(f)
	}
	return f
}

// PrettyJSON is JSON.stringify(v, null, 2) (no trailing newline).
func PrettyJSON(v any) ([]byte, error) {
	compact, err := MarshalJSONNoEscape(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// MarshalJSONNoEscape encodes v like JSON.stringify: no HTML escaping, ordered maps in order.
func MarshalJSONNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeJSONValue(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeJSONValue(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case *OMap:
		if t == nil {
			b.WriteString("null")
			return nil
		}
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			if err := writeJSONValue(b, t.vals[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSONValue(b, x); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case string:
		writeJSONString(b, t)
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			b.WriteString("null")
			return nil
		}
		enc, err := json.Marshal(t)
		if err != nil {
			return err
		}
		b.Write(enc)
	default:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return err
		}
		b.Write(bytes.TrimRight(buf.Bytes(), "\n"))
	}
	return nil
}

// writeJSONString writes s the way JSON.stringify does.
func writeJSONString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// JSONString returns s encoded as a JSON string literal.
func JSONString(s string) string {
	var b bytes.Buffer
	writeJSONString(&b, s)
	return b.String()
}

// ToOMapValue converts a value built from Go structs (via JSON) into ordered-map values.
func ToOMapValue(v any) (any, error) {
	data, err := MarshalJSONNoEscape(v)
	if err != nil {
		// fall back to encoding/json for structs
		data, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	return DecodeJSON(data)
}
