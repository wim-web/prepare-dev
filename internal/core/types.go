package core

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SubjectRef pins a record version.
type SubjectRef struct {
	ID     string `json:"id"`
	Rev    int64  `json:"rev"`
	SHA256 string `json:"sha256"`
}

// ---------- questions and answers ----------

var QuestionKinds = []string{"single_choice", "multi_choice", "free_text", "review"}
var QuestionStates = []string{"open", "answered", "received", "applied", "cancelled", "closed"}

type QuestionOption struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Summary     *string `json:"summary,omitempty"`
	Recommended *bool   `json:"recommended,omitempty"`
	Reason      *string `json:"reason,omitempty"`
}

type AppliedAs struct {
	DecisionID string `json:"decision_id"`
	TxID       string `json:"txid"`
}

type ClosedInfo struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
}

type Answer struct {
	AnswerID         string       `json:"answer_id"`
	AnsweredAt       string       `json:"answered_at"`
	SubjectsAtAnswer []SubjectRef `json:"subjects_at_answer"`
	Stale            bool         `json:"stale"`
	Choice           []string     `json:"choice"` // nil is null
	Text             *string      `json:"text"`
	ReceivedAt       *string      `json:"received_at"`
	AppliedAs        *AppliedAs   `json:"applied_as"`
	Closed           *ClosedInfo  `json:"closed"`
}

type CreatedBy struct {
	Host    *string `json:"host,omitempty"`
	Session *string `json:"session,omitempty"`
}

type Cancelled struct {
	At         string  `json:"at"`
	Reason     string  `json:"reason"`
	ReplacedBy *string `json:"replaced_by"`
}

type Question struct {
	ID            string           `json:"id"`
	Rev           int64            `json:"rev"`
	State         string           `json:"state"`
	Kind          string           `json:"kind"`
	CreatedAt     string           `json:"created_at"`
	CreatedBy     CreatedBy        `json:"created_by"`
	Subjects      []SubjectRef     `json:"subjects"`
	Prompt        string           `json:"prompt"`
	Options       []QuestionOption `json:"options"`
	AllowFreeText bool             `json:"allow_free_text"`
	// UI is legacy display data kept on old questions; it is never rendered.
	UI        json.RawMessage `json:"ui,omitempty"`
	Answers   []Answer        `json:"answers"`
	Cancelled *Cancelled      `json:"cancelled"`
}

// UnmarshalJSON applies the schema defaults (allow_free_text defaults to true).
func (q *Question) UnmarshalJSON(data []byte) error {
	type plain Question
	aux := struct {
		*plain
		AllowFreeText *bool `json:"allow_free_text"`
	}{plain: (*plain)(q)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	q.AllowFreeText = aux.AllowFreeText == nil || *aux.AllowFreeText
	q.normalize()
	return nil
}

func (q *Question) normalize() {
	if q.Subjects == nil {
		q.Subjects = []SubjectRef{}
	}
	if q.Options == nil {
		q.Options = []QuestionOption{}
	}
	if q.Answers == nil {
		q.Answers = []Answer{}
	}
	for i := range q.Answers {
		if q.Answers[i].SubjectsAtAnswer == nil {
			q.Answers[i].SubjectsAtAnswer = []SubjectRef{}
		}
	}
}

// Validate checks what the decoder cannot: enums, revisions and ids.
func (q *Question) Validate() error {
	q.normalize()
	var v validator
	if q.ID == "" {
		v.add("id", "expected string")
	}
	if q.Rev < 1 {
		v.add("rev", "too small: expected number to be >=1")
	}
	if !contains(QuestionStates, q.State) {
		v.add("state", "invalid state %q", q.State)
	}
	if !contains(QuestionKinds, q.Kind) {
		v.add("kind", "invalid kind %q", q.Kind)
	}
	for i, s := range q.Subjects {
		if s.Rev < 1 {
			v.add(fmt.Sprintf("subjects.%d.rev", i), "too small: expected number to be >=1")
		}
	}
	if len(v.issues) > 0 {
		return &ValidationError{Issues: v.issues}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Clone deep-copies a question through JSON.
func (q *Question) Clone() *Question {
	data, _ := json.Marshal(q)
	var out Question
	_ = json.Unmarshal(data, &out)
	return &out
}

// ---------- decisions ----------

type DecisionDraft struct {
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	Chosen       *string  `json:"chosen,omitempty"`
	Alternatives []string `json:"alternatives"`
}

func (d *DecisionDraft) UnmarshalJSON(data []byte) error {
	type plain DecisionDraft
	if err := json.Unmarshal(data, (*plain)(d)); err != nil {
		return err
	}
	if d.Alternatives == nil {
		d.Alternatives = []string{}
	}
	return nil
}

// ---------- verification runs ----------

var VerifyMethods = []string{"proof", "model_check", "bounded_check", "simulation", "test", "static_analysis"}
var VerifyResults = []string{"verified", "failed", "unknown", "timeout", "tool_error", "unsupported"}

type InputFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Role   string `json:"role"`
}

type RunNative struct {
	Ref       string  `json:"ref"`
	Statement *string `json:"statement"`
}

type RunClaim struct {
	Spec        SubjectRef  `json:"spec"`
	Claim       string      `json:"claim"`
	Requirement *SubjectRef `json:"requirement"`
	Native      RunNative   `json:"native"`
	Result      string      `json:"result"`
}

type NameVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type RunTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Extra   *OMap  `json:"extra"`
}

type RunInputs struct {
	Files  []InputFile `json:"files"`
	Config *OMap       `json:"config"`
	// Listings record which files a directory held, so that adding or removing a file
	// there (a new source file in a package, a new matching file of a pattern) makes
	// the evidence stale too. Runs written before listings existed have none.
	Listings []InputListing `json:"listings,omitempty"`
}

// InputListing is the digest of the file names under a directory (relative to root).
type InputListing struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
	SHA256    string `json:"sha256"`
}

type RunEnvironment struct {
	Platform   string  `json:"platform"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
	DurationMS float64 `json:"duration_ms"`
}

type RunScope struct {
	Bounded  bool     `json:"bounded"`
	Bounds   *OMap    `json:"bounds"`
	Excluded []string `json:"excluded"`
	Covered  []string `json:"covered"`
}

type Artifact struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type RunRecord struct {
	ID          string         `json:"id"`
	Plugin      NameVersion    `json:"plugin"`
	Tool        RunTool        `json:"tool"`
	Method      string         `json:"method"`
	Claims      []RunClaim     `json:"claims"`
	Targets     []InputFile    `json:"targets"`
	Inputs      RunInputs      `json:"inputs"`
	Environment RunEnvironment `json:"environment"`
	Result      string         `json:"result"`
	Assumptions []string       `json:"assumptions"`
	Scope       RunScope       `json:"scope"`
	Artifacts   []Artifact     `json:"artifacts"`
	Summary     string         `json:"summary"`
}

// Normalize fills the schema defaults.
func (r *RunRecord) Normalize() {
	if r.Tool.Extra == nil {
		r.Tool.Extra = NewOMap()
	}
	if r.Claims == nil {
		r.Claims = []RunClaim{}
	}
	if r.Targets == nil {
		r.Targets = []InputFile{}
	}
	if r.Inputs.Files == nil {
		r.Inputs.Files = []InputFile{}
	}
	if r.Inputs.Config == nil {
		r.Inputs.Config = NewOMap()
	}
	if r.Assumptions == nil {
		r.Assumptions = []string{}
	}
	if r.Scope.Excluded == nil {
		r.Scope.Excluded = []string{}
	}
	if r.Scope.Covered == nil {
		r.Scope.Covered = []string{}
	}
	if r.Artifacts == nil {
		r.Artifacts = []Artifact{}
	}
}

// Validate checks enums after normalizing.
func (r *RunRecord) Validate() error {
	r.Normalize()
	var v validator
	if r.ID == "" {
		v.add("id", "expected string")
	}
	if !contains(VerifyMethods, r.Method) {
		v.add("method", "invalid method %q", r.Method)
	}
	if !contains(VerifyResults, r.Result) {
		v.add("result", "invalid result %q", r.Result)
	}
	for i, c := range r.Claims {
		if !contains(VerifyResults, c.Result) {
			v.add(fmt.Sprintf("claims.%d.result", i), "invalid result %q", c.Result)
		}
	}
	for i, a := range r.Artifacts {
		if !contains([]string{"log", "counterexample", "generated_test", "report", "other"}, a.Kind) {
			v.add(fmt.Sprintf("artifacts.%d.kind", i), "invalid artifact kind %q", a.Kind)
		}
	}
	if len(v.issues) > 0 {
		return &ValidationError{Issues: v.issues}
	}
	return nil
}

// ---------- journal events ----------

// Event is one line of events.jsonl. Only the fields of its type are written.
type Event struct {
	TS   string
	Type string

	ID, Path   string
	Rev        int64
	SHA256     string
	Reason     string
	RevFrom    int64
	RevTo      int64
	SHA256From string
	SHA256To   string

	QuestionID string
	AnswerID   string
	Subjects   []SubjectRef
	Stale      bool
	ReplacedBy *string

	TxID       string
	DecisionID string
	Decision   *DecisionDraft

	RunID  string
	Result string
}

type kv struct {
	k string
	v any
}

func (e Event) pairs() ([]kv, error) {
	p := []kv{{"ts", e.TS}, {"type", e.Type}}
	switch e.Type {
	case "record.write", "record.discover":
		p = append(p, kv{"id", e.ID}, kv{"path", e.Path}, kv{"rev", e.Rev}, kv{"sha256", e.SHA256})
	case "record.remove":
		p = append(p, kv{"id", e.ID}, kv{"path", e.Path}, kv{"reason", e.Reason})
	case "external_change":
		p = append(p, kv{"id", e.ID}, kv{"path", e.Path}, kv{"rev_from", e.RevFrom}, kv{"rev_to", e.RevTo}, kv{"sha256_from", e.SHA256From}, kv{"sha256_to", e.SHA256To})
	case "question.create":
		p = append(p, kv{"question_id", e.QuestionID}, kv{"subjects", nonNilRefs(e.Subjects)})
	case "answer.save":
		p = append(p, kv{"question_id", e.QuestionID}, kv{"answer_id", e.AnswerID}, kv{"stale", e.Stale})
	case "answer.ack":
		p = append(p, kv{"question_id", e.QuestionID}, kv{"answer_id", e.AnswerID})
	case "answer.close":
		p = append(p, kv{"question_id", e.QuestionID}, kv{"answer_id", e.AnswerID}, kv{"reason", e.Reason})
	case "question.cancel":
		p = append(p, kv{"question_id", e.QuestionID}, kv{"reason", e.Reason}, kv{"replaced_by", e.ReplacedBy})
	case "apply.intent":
		d := e.Decision
		if d == nil {
			d = &DecisionDraft{Alternatives: []string{}}
		}
		p = append(p, kv{"txid", e.TxID}, kv{"question_id", e.QuestionID}, kv{"answer_id", e.AnswerID}, kv{"decision_id", e.DecisionID}, kv{"subjects", nonNilRefs(e.Subjects)}, kv{"decision", d})
	case "apply.commit":
		p = append(p, kv{"txid", e.TxID})
	case "apply.abort":
		p = append(p, kv{"txid", e.TxID}, kv{"reason", e.Reason})
	case "run.record":
		p = append(p, kv{"run_id", e.RunID}, kv{"result", e.Result})
	default:
		return nil, fmt.Errorf("unknown event type %q", e.Type)
	}
	return p, nil
}

func nonNilRefs(r []SubjectRef) []SubjectRef {
	if r == nil {
		return []SubjectRef{}
	}
	return r
}

// MarshalJSON writes "ts", "type" and then the fields of the type, in schema order.
func (e Event) MarshalJSON() ([]byte, error) {
	p, err := e.pairs()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, x := range p {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(&b, x.k)
		b.WriteByte(':')
		data, err := MarshalJSONNoEscape(x.v)
		if err != nil {
			return nil, err
		}
		b.Write(data)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

var eventRequired = map[string][]string{
	"record.write":    {"id", "path", "rev", "sha256"},
	"record.discover": {"id", "path", "rev", "sha256"},
	"record.remove":   {"id", "path", "reason"},
	"external_change": {"id", "path", "rev_from", "rev_to", "sha256_from", "sha256_to"},
	"question.create": {"question_id", "subjects"},
	"answer.save":     {"question_id", "answer_id", "stale"},
	"answer.ack":      {"question_id", "answer_id"},
	"answer.close":    {"question_id", "answer_id", "reason"},
	"question.cancel": {"question_id", "reason", "replaced_by"},
	"apply.intent":    {"txid", "question_id", "answer_id", "decision_id", "subjects", "decision"},
	"apply.commit":    {"txid"},
	"apply.abort":     {"txid", "reason"},
	"run.record":      {"run_id", "result"},
}

// UnmarshalJSON validates the type and its required fields.
func (e *Event) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var aux struct {
		TS         string         `json:"ts"`
		Type       string         `json:"type"`
		ID         string         `json:"id"`
		Path       string         `json:"path"`
		Rev        int64          `json:"rev"`
		SHA256     string         `json:"sha256"`
		Reason     string         `json:"reason"`
		RevFrom    int64          `json:"rev_from"`
		RevTo      int64          `json:"rev_to"`
		SHA256From string         `json:"sha256_from"`
		SHA256To   string         `json:"sha256_to"`
		QuestionID string         `json:"question_id"`
		AnswerID   string         `json:"answer_id"`
		Subjects   []SubjectRef   `json:"subjects"`
		Stale      bool           `json:"stale"`
		ReplacedBy *string        `json:"replaced_by"`
		TxID       string         `json:"txid"`
		DecisionID string         `json:"decision_id"`
		Decision   *DecisionDraft `json:"decision"`
		RunID      string         `json:"run_id"`
		Result     string         `json:"result"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	req, ok := eventRequired[aux.Type]
	if !ok {
		return fmt.Errorf("unknown event type %q", aux.Type)
	}
	if _, ok := raw["ts"]; !ok {
		return fmt.Errorf("event %s: ts is required", aux.Type)
	}
	for _, k := range req {
		if _, ok := raw[k]; !ok {
			return fmt.Errorf("event %s: %s is required", aux.Type, k)
		}
	}
	if aux.Type == "run.record" && !contains(VerifyResults, aux.Result) {
		return fmt.Errorf("event run.record: invalid result %q", aux.Result)
	}
	*e = Event{
		TS: aux.TS, Type: aux.Type, ID: aux.ID, Path: aux.Path, Rev: aux.Rev, SHA256: aux.SHA256,
		Reason: aux.Reason, RevFrom: aux.RevFrom, RevTo: aux.RevTo, SHA256From: aux.SHA256From, SHA256To: aux.SHA256To,
		QuestionID: aux.QuestionID, AnswerID: aux.AnswerID, Subjects: aux.Subjects, Stale: aux.Stale, ReplacedBy: aux.ReplacedBy,
		TxID: aux.TxID, DecisionID: aux.DecisionID, Decision: aux.Decision, RunID: aux.RunID, Result: aux.Result,
	}
	return nil
}

// ---------- configuration and identity ----------

// ToolIdentity is the version and build (sha256 of the executable) of a binary; Path
// is where it was started from, for diagnosis. Root (the directory of the executable)
// and Mode ("package") are written only so that the TypeScript version, which requires
// them, can read daemon.json and refuse a daemon of another build instead of starting a
// second writer; this version does not use them.
type ToolIdentity struct {
	Version string `json:"version"`
	Build   string `json:"build"`
	Path    string `json:"path"`
	Root    string `json:"root"`
	Mode    string `json:"mode"`
}

type ProjectConfig struct {
	RecordsDir string `json:"records_dir"`
	Wait       struct {
		DefaultTimeoutMS int64 `json:"default_timeout_ms"`
	} `json:"wait"`
	Plugins *OMap `json:"plugins"`
}

// DefaultConfig is the configuration of a project without config.json.
func DefaultConfig() ProjectConfig {
	var c ProjectConfig
	c.RecordsDir = "prepare"
	c.Wait.DefaultTimeoutMS = 50000
	c.Plugins = NewOMap()
	return c
}

// ParseConfig reads config.json with defaults for missing keys.
func ParseConfig(data []byte) (ProjectConfig, error) {
	c := DefaultConfig()
	v, err := DecodeJSON(data)
	if err != nil {
		return c, err
	}
	m, ok := v.(*OMap)
	if !ok {
		return c, fmt.Errorf("config.json must be an object")
	}
	if s, ok := m.Get("records_dir"); ok {
		str, ok := s.(string)
		if !ok {
			return c, fmt.Errorf("records_dir must be a string")
		}
		c.RecordsDir = str
	}
	if w, ok := m.Map("wait"); ok {
		if t, ok := w.Get("default_timeout_ms"); ok {
			i, ok := AsInt(t)
			if !ok {
				return c, fmt.Errorf("wait.default_timeout_ms must be an integer")
			}
			c.Wait.DefaultTimeoutMS = i
		}
	}
	if p, ok := m.Get("plugins"); ok {
		pm, ok := p.(*OMap)
		if !ok {
			return c, fmt.Errorf("plugins must be an object")
		}
		for _, k := range pm.Keys() {
			v, _ := pm.Get(k)
			if _, ok := v.(*OMap); !ok {
				return c, fmt.Errorf("plugins.%s must be an object", k)
			}
		}
		c.Plugins = pm
	}
	return c, nil
}
