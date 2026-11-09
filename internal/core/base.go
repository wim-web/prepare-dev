package core

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------- markdown with YAML frontmatter ----------

var frontmatterRE = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?(.*)\z`)

// ParseMarkdown splits a record file into frontmatter and body. A file without
// frontmatter has an empty map and the whole text as body.
func ParseMarkdown(text string) (*OMap, string, error) {
	m := frontmatterRE.FindStringSubmatch(text)
	if m == nil {
		return NewOMap(), text, nil
	}
	v, err := ParseYAML(m[1])
	if err != nil {
		return nil, "", err
	}
	fm, ok := v.(*OMap)
	if !ok || fm == nil {
		fm = NewOMap()
	}
	return fm, m[2], nil
}

// SerializeMarkdown renders a record: "---\n<yaml>\n---\n" and a body that starts and
// ends with a newline.
func SerializeMarkdown(fm *OMap, body string) string {
	y := strings.TrimRight(EmitYAML(fm), " \t\r\n")
	if !strings.HasPrefix(body, "\n") {
		body = "\n" + body
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return "---\n" + y + "\n---\n" + body
}

// SerializeMarkdownChecked serializes a record and reads it back; it fails instead of
// returning text whose frontmatter would read differently from fm.
func SerializeMarkdownChecked(fm *OMap, body string) (string, error) {
	text := SerializeMarkdown(fm, body)
	back, _, err := ParseMarkdown(text)
	if err != nil {
		return "", fmt.Errorf("the serialized frontmatter cannot be read back: %w", err)
	}
	want, err1 := MarshalJSONNoEscape(fm)
	got, err2 := MarshalJSONNoEscape(back)
	if err1 != nil || err2 != nil || !bytes.Equal(want, got) {
		return "", fmt.Errorf("the serialized frontmatter reads back differently; refusing to write it")
	}
	return text, nil
}

// ---------- ids ----------

// IDPrefixes maps record kinds (and question/run) to their id prefixes.
var IDPrefixes = map[string]string{
	"discovery":   "DIS",
	"requirement": "REQ",
	"decision":    "DEC",
	"design":      "DES",
	"spec":        "SPEC",
	"review":      "REV",
	"record":      "REC",
	"proposal":    "PRO",
	"question":    "Q",
	"run":         "RUN",
}

var idRE = regexp.MustCompile(`^([A-Z]+)-(\d{4,})$`)

// ParseID splits "REQ-0001" into prefix and number.
func ParseID(id string) (string, int, bool) {
	m := idRE.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// FormatID pads the number to four digits.
func FormatID(prefix string, n int) string { return fmt.Sprintf("%s-%04d", prefix, n) }

// NextID returns max+1 for prefix among used ids.
func NextID(prefix string, used []string) string {
	max := 0
	for _, id := range used {
		if p, n, ok := ParseID(id); ok && p == prefix && n > max {
			max = n
		}
	}
	return FormatID(prefix, max+1)
}

// AnswerID formats "ANS-0001-n" for question "Q-0001".
func AnswerID(questionID string, n int) string {
	return fmt.Sprintf("ANS-%s-%d", strings.TrimPrefix(questionID, "Q-"), n)
}

// ValidQuestionID reports whether id looks like "Q-0001".
func ValidQuestionID(id string) bool {
	p, _, ok := ParseID(id)
	return ok && p == "Q"
}

// ---------- hashing, time, random ----------

// SHA256Hex hashes bytes (a string is hashed as UTF-8).
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256File hashes the raw bytes of a file.
func SHA256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return SHA256Hex(data), nil
}

// Now is replaceable in tests.
var Now = time.Now

// NowISO is Date.prototype.toISOString: UTC with milliseconds, fixed width.
func NowISO() string { return FormatISO(Now()) }

// FormatISO formats t like toISOString.
func FormatISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// TodayISO is the UTC date.
func TodayISO() string { return Now().UTC().Format("2006-01-02") }

// RandomID returns n random bytes as lowercase hex.
func RandomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// RandomUUID returns a random version-4 UUID.
func RandomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// ---------- errors ----------

// RuntimeError carries a code that maps to an HTTP status.
type RuntimeError struct {
	Code    string
	Message string
	Details *OMap
}

func (e *RuntimeError) Error() string { return e.Message }

// HTTPStatus maps the error code like the original implementation.
func (e *RuntimeError) HTTPStatus() int {
	switch e.Code {
	case "not_found":
		return 404
	case "conflict", "stale", "closed", "inflight", "question_rev":
		return 409
	case "invalid":
		return 400
	case "forbidden":
		return 403
	case "dead":
		return 503
	case "unsupported":
		return 501
	}
	return 500
}

// Errf builds a RuntimeError; details are alternating keys and values.
func Errf(code, message string, details ...any) *RuntimeError {
	d := OM(details...)
	return &RuntimeError{Code: code, Message: message, Details: d}
}

// SimulatedCrash is returned by test hooks to simulate the daemon dying between stages.
type SimulatedCrash struct{ Stage string }

func (e *SimulatedCrash) Error() string { return "simulated crash after stage " + e.Stage }
