import PrepareDev.Spec.State

/-!
# SPEC-0001 Section 2: operations

`step : State → Action → State × Result` is a total, executable transition
function.  `apply` is split into the stages a–d of Section 2.1 so that a
crash after any stage is a transition of the model, and `recover` reuses the
same stage functions for roll-forward.
-/

namespace PrepareDev.Spec

/-- Update every answer with the given id. -/
def State.updateAnswer (s : State) (a : AnswerId) (f : Answer → Answer) : State :=
  { s with answers := s.answers.map fun x => if x.id = a then f x else x }

/-- The intent allocated by stage a for answer `a` of question `q`. -/
def newIntent (s : State) (a : Answer) (q : Question) : Intent :=
  ⟨s.nextTx, a.id, s.nextDecision, q.subjects⟩

/-- Stage a: append the intent (ids are allocated by `newIntent`). -/
def stageA (s : State) (i : Intent) : State :=
  { s with journal := .intent i :: s.journal
           nextTx := s.nextTx + 1
           nextDecision := s.nextDecision + 1 }

/-- Stage b: write the decision file `{decision_id, txid}` (idempotent). -/
def stageB (s : State) (i : Intent) : State :=
  if (⟨i.decisionId, i.txid⟩ : Decision) ∈ s.decisions then s
  else { s with decisions := ⟨i.decisionId, i.txid⟩ :: s.decisions }

/-- Stage c: write `applied_as = decision_id` on the answer. -/
def stageC (s : State) (i : Intent) : State :=
  s.updateAnswer i.answer fun x => { x with appliedAs := some i.decisionId }

/-- Stage d: append `commit {txid}`. -/
def stageD (s : State) (i : Intent) : State :=
  { s with journal := .commit i.txid :: s.journal }

/-- Roll-forward: stages b, c, d (each idempotent) for the open intent. -/
def rollForward (s : State) (i : Intent) : State :=
  stageD (stageC (stageB s i) i) i

/-- Abort: append `abort {txid}`, remove the provisional decision file, clear
`applied_as` and mark the answer `stale`. -/
def abortTx (s : State) (i : Intent) : State :=
  State.updateAnswer
    { s with journal := .abort i.txid :: s.journal
             decisions := s.decisions.filter fun d => d.txid != i.txid }
    i.answer fun x => { x with appliedAs := none, stale := true }

/-- Stages b–d of `apply` after stage a, stopping after the stage named by
`crash` (the state is then `inflight`). -/
def runStages (s : State) (i : Intent) : Option CrashAfter → State × Result
  | some .intent => (s, .crashed)
  | some .decision => (stageB s i, .crashed)
  | some .question => (stageC (stageB s i) i, .crashed)
  | none => (rollForward s i, .applied i.decisionId)

/-- `create_question(q, subjects)`: copy the current versions of the records. -/
def stepCreateQuestion (s : State) (q : QuestionId) (subjects : List RecordId) :
    State × Result :=
  match s.findQuestion q with
  | some _ => (s, .noop)
  | none =>
    match s.snapshot subjects with
    | none => (s, .noop)
    | some subs => ({ s with questions := ⟨q, subs⟩ :: s.questions }, .ok)

/-- `edit(r)`: external edit, bumps the record's version. -/
def stepEdit (s : State) (r : RecordId) : State × Result :=
  match s.versionOf r with
  | none => (s, .noop)
  | some _ =>
    ({ s with records := s.records.map fun p => if p.1 = r then (p.1, p.2 + 1) else p }, .ok)

/-- `save_answer(q, a)`: `stale` iff some subject's current version differs
from the question's snapshot. -/
def stepSaveAnswer (s : State) (q : QuestionId) (a : AnswerId) : State × Result :=
  match s.findQuestion q with
  | none => (s, .noop)
  | some qq =>
    match s.findAnswer a with
    | some _ => (s, .noop)
    | none =>
      let ans : Answer :=
        { id := a
          question := q
          subjectsAtAnswer := (s.snapshot (qq.subjects.map (·.record))).getD []
          stale := !s.subjectsMatch qq.subjects
          received := false
          closed := false
          appliedAs := none }
      ({ s with answers := ans :: s.answers }, .ok)

/-- `ack(a)`: `received = true`. -/
def stepAck (s : State) (a : AnswerId) : State × Result :=
  match s.findAnswer a with
  | none => (s, .noop)
  | some _ => (s.updateAnswer a fun x => { x with received := true }, .ok)

/-- `close(a)`: only if `¬committed(a)`. -/
def stepClose (s : State) (a : AnswerId) : State × Result :=
  match s.findAnswer a with
  | none => (s, .noop)
  | some _ =>
    match s.committedAs a with
    | some d => (s, .alreadyApplied d)
    | none => (s.updateAnswer a fun x => { x with closed := true }, .ok)

/-- `apply(a)` (Section 2.1), crashing after the named stage if any. -/
def stepApply (s : State) (a : AnswerId) (crash : Option CrashAfter) : State × Result :=
  match s.findAnswer a with
  | none => (s, .noop)
  | some ans =>
    match s.committedAs a with
    | some d => (s, .alreadyApplied d)
    | none =>
      if ans.closed then (s, .rejectedClosed)
      else
        match s.findQuestion ans.question with
        | none => (s, .noop)
        | some q =>
          if ans.stale || !s.subjectsMatch q.subjects then
            (s.updateAnswer a fun x => { x with stale := true }, .rejectedStale)
          else
            let i := newIntent s ans q
            runStages (stageA s i) i crash

/-- `recover()` (Section 2.2). -/
def stepRecover (s : State) : State × Result :=
  match s.inflightIntent with
  | none => (s, .noop)
  | some i =>
    if s.subjectsMatch i.subjects then (rollForward s i, .recoveredForward)
    else (abortTx s i, .recoveredAbort)

/-- Normal operations are rejected while `inflight`. -/
def guardInflight (s : State) (k : State × Result) : State × Result :=
  if s.inflight then (s, .rejectedInflight) else k

/-- The transition function.  `edit` and `recover` are always accepted;
everything else is rejected with `rejected_inflight` while `inflight`. -/
def step (s : State) : Action → State × Result
  | .createQuestion q subjects => guardInflight s (stepCreateQuestion s q subjects)
  | .edit r => stepEdit s r
  | .saveAnswer q a => guardInflight s (stepSaveAnswer s q a)
  | .ack a => guardInflight s (stepAck s a)
  | .close a => guardInflight s (stepClose s a)
  | .apply a crash => guardInflight s (stepApply s a crash)
  | .recover => stepRecover s

end PrepareDev.Spec
