package core

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	yaml "go.yaml.in/yaml/v3"
)

// ---------- parsing (YAML 1.2 core schema, like the npm "yaml" package) ----------

// ParseYAML parses a YAML document into ordered-map values. An empty document is nil.
func ParseYAML(src string) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	return convertYAMLNode(doc.Content[0], 0)
}

func convertYAMLNode(n *yaml.Node, depth int) (any, error) {
	if depth > 200 {
		return nil, fmt.Errorf("YAML nesting is too deep")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return convertYAMLNode(n.Content[0], depth+1)
	case yaml.AliasNode:
		return convertYAMLNode(n.Alias, depth+1)
	case yaml.MappingNode:
		m := NewOMap()
		for i := 0; i+1 < len(n.Content); i += 2 {
			kv, err := convertYAMLNode(n.Content[i], depth+1)
			if err != nil {
				return nil, err
			}
			key := scalarKey(kv)
			if m.Has(key) {
				return nil, fmt.Errorf("line %d: map keys must be unique (%q)", n.Content[i].Line, key)
			}
			val, err := convertYAMLNode(n.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			m.Set(key, val)
		}
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := convertYAMLNode(c, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return n.Value, nil
		}
		if n.Style&yaml.TaggedStyle != 0 && n.Tag == "!!str" {
			return n.Value, nil
		}
		return ResolvePlainScalar(n.Value), nil
	}
	return nil, fmt.Errorf("unsupported YAML node")
}

func scalarKey(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return formatNumber(t)
	default:
		data, _ := MarshalJSONNoEscape(v)
		return string(data)
	}
}

var (
	reCoreNull     = regexp.MustCompile(`^(?:~|[Nn]ull|NULL)?$`)
	reCoreBool     = regexp.MustCompile(`^(?:[Tt]rue|TRUE|[Ff]alse|FALSE)$`)
	reCoreIntOct   = regexp.MustCompile(`^0o[0-7]+$`)
	reCoreInt      = regexp.MustCompile(`^[-+]?[0-9]+$`)
	reCoreIntHex   = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	reCoreFloatNaN = regexp.MustCompile(`^(?:[-+]?\.(?:inf|Inf|INF)|\.nan|\.NaN|\.NAN)$`)
	reCoreFloatExp = regexp.MustCompile(`^[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)[eE][-+]?[0-9]+$`)
	reCoreFloat    = regexp.MustCompile(`^[-+]?(?:\.[0-9]+|[0-9]+\.[0-9]*)$`)
	coreTests      = []*regexp.Regexp{reCoreNull, reCoreBool, reCoreIntOct, reCoreInt, reCoreIntHex, reCoreFloatNaN, reCoreFloatExp, reCoreFloat}
)

// ResolvePlainScalar applies the YAML 1.2 core schema to a plain scalar.
func ResolvePlainScalar(s string) any {
	switch {
	case reCoreNull.MatchString(s):
		return nil
	case reCoreBool.MatchString(s):
		return s[0] == 't' || s[0] == 'T'
	case reCoreIntOct.MatchString(s):
		if i, err := strconv.ParseInt(s[2:], 8, 64); err == nil {
			return i
		}
		f, _ := strconv.ParseFloat(s[2:], 64)
		return f
	case reCoreInt.MatchString(s):
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
		f, _ := strconv.ParseFloat(s, 64)
		return f
	case reCoreIntHex.MatchString(s):
		if i, err := strconv.ParseInt(s[2:], 16, 64); err == nil {
			return i
		}
		return s
	case reCoreFloatNaN.MatchString(s):
		if strings.Contains(strings.ToLower(s), "nan") {
			return math.NaN()
		}
		if strings.HasPrefix(s, "-") {
			return math.Inf(-1)
		}
		return math.Inf(1)
	case reCoreFloatExp.MatchString(s), reCoreFloat.MatchString(s):
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s
		}
		return normalizeFloat(f)
	}
	return s
}

// ---------- emitting (mimics the npm "yaml" package, v2 defaults, lineWidth 0) ----------

const yamlIndentStep = "  "

// EmitYAML renders v as a YAML document (with a trailing newline).
func EmitYAML(v any) string {
	var b strings.Builder
	switch t := v.(type) {
	case *OMap:
		if t.Len() == 0 {
			b.WriteString("{}\n")
		} else {
			emitMapEntries(&b, t, "", false)
		}
	case []any:
		if len(t) == 0 {
			b.WriteString("[]\n")
		} else {
			emitSeqItems(&b, t, "", false)
		}
	default:
		b.WriteString(emitScalar(v, "", false))
		b.WriteByte('\n')
	}
	return b.String()
}

// emitMapEntries writes "key: value" lines at indent. When inline is true the first
// entry continues the current line (after "- ").
func emitMapEntries(b *strings.Builder, m *OMap, indent string, inline bool) {
	for i, k := range m.keys {
		if i > 0 || !inline {
			b.WriteString(indent)
		}
		b.WriteString(emitString(k, indent+yamlIndentStep, true))
		v := m.vals[k]
		switch t := v.(type) {
		case *OMap:
			if t.Len() == 0 {
				b.WriteString(": {}\n")
			} else {
				b.WriteString(":\n")
				emitMapEntries(b, t, indent+yamlIndentStep, false)
			}
		case []any:
			if len(t) == 0 {
				b.WriteString(": []\n")
			} else {
				b.WriteString(":\n")
				emitSeqItems(b, t, indent+yamlIndentStep, false)
			}
		default:
			b.WriteString(": ")
			b.WriteString(emitScalar(v, indent+yamlIndentStep, false))
			b.WriteByte('\n')
		}
	}
}

func emitSeqItems(b *strings.Builder, s []any, indent string, inline bool) {
	for i, v := range s {
		if i > 0 || !inline {
			b.WriteString(indent)
		}
		b.WriteString("- ")
		switch t := v.(type) {
		case *OMap:
			if t.Len() == 0 {
				b.WriteString("{}\n")
			} else {
				emitMapEntries(b, t, indent+yamlIndentStep, true)
			}
		case []any:
			if len(t) == 0 {
				b.WriteString("[]\n")
			} else {
				emitSeqItems(b, t, indent+yamlIndentStep, true)
			}
		default:
			b.WriteString(emitScalar(v, indent+yamlIndentStep, false))
			b.WriteByte('\n')
		}
	}
}

func emitScalar(v any, indent string, implicitKey bool) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		switch {
		case math.IsNaN(t):
			return ".nan"
		case math.IsInf(t, 1):
			return ".inf"
		case math.IsInf(t, -1):
			return "-.inf"
		}
		return formatNumber(t)
	case string:
		return emitString(t, indent, implicitKey)
	default:
		data, _ := MarshalJSONNoEscape(v)
		return emitString(string(data), indent, implicitKey)
	}
}

func formatNumber(f float64) string {
	data, err := MarshalJSONNoEscape(f)
	if err != nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return string(data)
}

var (
	reNeedsQuote = regexp.MustCompile("^[\n\t ,\\[\\]{}#&*!|>'\"%@`]|^[?-]$|^[?-][ \t]|[\n:][ \t]|[ \t]\n|[\n\t ]#|[\n\t :]$")
	// control characters, and characters a YAML 1.1 reader (yaml.v3) takes as line
	// breaks or a byte order mark; they are written double-quoted and escaped
	reControl     = regexp.MustCompile("[\x00-\x08\x0b-\x1f\x7f-\u009f\u2028\u2029\ufeff]")
	reDocMarker   = regexp.MustCompile(`(?m)^(?:%|---|\.\.\.)`)
	reBlockWSTail = regexp.MustCompile("\n[\t ]+$")
)

// emitString renders a string scalar. indent is the indentation of block content
// (the "ctx.indent" of the npm implementation).
func emitString(v, indent string, implicitKey bool) string {
	if reControl.MatchString(v) {
		return doubleQuoted(v, implicitKey)
	}
	return plainString(v, indent, implicitKey)
}

func plainString(v, indent string, implicitKey bool) string {
	if implicitKey && strings.Contains(v, "\n") {
		return quotedString(v, indent, implicitKey)
	}
	if reNeedsQuote.MatchString(v) {
		if implicitKey || !strings.Contains(v, "\n") {
			return quotedString(v, indent, implicitKey)
		}
		return blockString(v, indent, implicitKey)
	}
	if !implicitKey && strings.Contains(v, "\n") {
		return blockString(v, indent, implicitKey)
	}
	if reDocMarker.MatchString(v) {
		if indent == "" {
			return blockString(v, yamlIndentStep, implicitKey)
		} else if implicitKey && indent == yamlIndentStep {
			return quotedString(v, indent, implicitKey)
		}
	}
	for _, re := range coreTests {
		if re.MatchString(v) {
			return quotedString(v, indent, implicitKey)
		}
	}
	return v
}

func quotedString(v, indent string, implicitKey bool) string {
	hasDouble := strings.Contains(v, `"`)
	hasSingle := strings.Contains(v, "'")
	if hasDouble && !hasSingle {
		return singleQuoted(v, indent, implicitKey)
	}
	return doubleQuoted(v, implicitKey)
}

var reWSAroundNewline = regexp.MustCompile("[ \t]\n|\n[ \t]")

func singleQuoted(v, indent string, implicitKey bool) string {
	if (implicitKey && strings.Contains(v, "\n")) || reWSAroundNewline.MatchString(v) {
		return doubleQuoted(v, implicitKey)
	}
	if indent == "" && reDocMarker.MatchString(v) {
		indent = yamlIndentStep
	}
	s := strings.ReplaceAll(v, "'", "''")
	s = replaceNewlineRuns(s, func(run string) string { return run + "\n" + indent })
	return "'" + s + "'"
}

// doubleQuoted follows the npm implementation for single-line strings; line breaks
// stay as "\n" escapes.
// yamlEscapes are runes JSON leaves as they are but a YAML reader must not see raw.
func yamlEscape(r rune) (string, bool) {
	switch {
	case r == 0x85:
		return `\N`, true
	case r == 0x2028:
		return `\L`, true
	case r == 0x2029:
		return `\P`, true
	case r == 0xfeff:
		return `\uFEFF`, true
	case r >= 0x7f && r <= 0x9f:
		return fmt.Sprintf(`\x%02x`, r), true
	}
	return "", false
}

func doubleQuoted(v string, implicitKey bool) string {
	var escaped strings.Builder
	for _, r := range JSONString(v) {
		if e, ok := yamlEscape(r); ok {
			escaped.WriteString(e)
		} else {
			escaped.WriteRune(r)
		}
	}
	json := escaped.String()
	var b strings.Builder
	for i := 0; i < len(json); i++ {
		ch := json[i]
		if ch == ' ' && i+2 < len(json) && json[i+1] == '\\' && json[i+2] == 'n' {
			b.WriteString(`\ `)
			continue
		}
		if ch == '\\' && i+1 < len(json) {
			switch json[i+1] {
			case 'u':
				code := json[i+2 : i+6]
				switch code {
				case "0000":
					b.WriteString(`\0`)
				case "0007":
					b.WriteString(`\a`)
				case "000b":
					b.WriteString(`\v`)
				case "001b":
					b.WriteString(`\e`)
				case "0085":
					b.WriteString(`\N`)
				case "00a0":
					b.WriteString(`\_`)
				case "2028":
					b.WriteString(`\L`)
				case "2029":
					b.WriteString(`\P`)
				default:
					if strings.HasPrefix(code, "00") {
						b.WriteString(`\x` + code[2:])
					} else {
						b.WriteString(json[i : i+6])
					}
				}
				i += 5
				continue
			default:
				b.WriteString(json[i : i+2])
				i++
				continue
			}
		}
		b.WriteByte(ch)
	}
	_ = implicitKey
	return b.String()
}

func isAllSpace(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && r != '\ufeff' {
			return false
		}
	}
	return true
}

func blockString(v, indent string, implicitKey bool) string {
	if reBlockWSTail.MatchString(v) || isAllSpace(v) {
		return quotedString(v, indent, implicitKey)
	}
	if indent == "" && reDocMarker.MatchString(v) {
		indent = yamlIndentStep
	}
	// chomping from the whitespace at the end
	endStart := len(v)
	for endStart > 0 {
		ch := v[endStart-1]
		if ch != '\n' && ch != '\t' && ch != ' ' {
			break
		}
		endStart--
	}
	end := v[endStart:]
	chomp := ""
	endNl := strings.IndexByte(end, '\n')
	switch {
	case endNl == -1:
		chomp = "-"
	case v == end || endNl != len(end)-1:
		chomp = "+"
	default:
		chomp = ""
	}
	if end != "" {
		v = v[:len(v)-len(end)]
		if strings.HasSuffix(end, "\n") {
			end = end[:len(end)-1]
		}
		end = indentNewlineRunsNotAtEnd(end, indent)
	}
	// indentation indicator from the whitespace at the start
	startWithSpace := false
	startEnd := 0
	startNl := -1
	for startEnd = 0; startEnd < len(v); startEnd++ {
		ch := v[startEnd]
		if ch == ' ' {
			startWithSpace = true
		} else if ch == '\n' {
			startNl = startEnd
		} else {
			break
		}
	}
	cut := startEnd
	if startNl < startEnd {
		cut = startNl + 1
	}
	start := v[:cut]
	if start != "" {
		v = v[len(start):]
		start = replaceNewlineRuns(start, func(run string) string { return run + indent })
	}
	indentSize := "1"
	if indent != "" {
		indentSize = "2"
	}
	header := chomp
	if startWithSpace {
		header = indentSize + chomp
	}
	body := replaceNewlineRuns(v, func(run string) string { return run + indent })
	return "|" + header + "\n" + indent + start + body + end
}

// replaceNewlineRuns replaces every maximal run of "\n" using f (like .replace(/\n+/g, …)).
func replaceNewlineRuns(s string, f func(run string) string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\n' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '\n' {
			j++
		}
		b.WriteString(f(s[i:j]))
		i = j
	}
	return b.String()
}

// indentNewlineRunsNotAtEnd adds indent after each newline run that is not at the end
// (/\n+(?!\n|$)/g → "$&" + indent).
func indentNewlineRunsNotAtEnd(s, indent string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\n' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '\n' {
			j++
		}
		b.WriteString(s[i:j])
		if j < len(s) {
			b.WriteString(indent)
		}
		i = j
	}
	return b.String()
}
