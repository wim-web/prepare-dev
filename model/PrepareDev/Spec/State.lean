/-!
# SPEC-0001 Section 1: persisted state

Model of the persisted state of the runtime.  Every collection is an
association list (newest element first) so that the invariants in
`PrepareDev.Spec.Invariant` can be stated with plain list membership.

Simplifications with respect to SPEC-0001:

* a record version `{rev, sha256}` is collapsed into a single `Nat` that
  `edit` increments;
* decision ids are natural numbers (`D1` is `1`, `D2` is `2`, …); the
  trace generator renders them as `"D<n>"`.
-/

namespace PrepareDev.Spec

abbrev RecordId := String
abbrev QuestionId := String
abbrev AnswerId := String
abbrev DecisionId := Nat
abbrev TxId := Nat

/-- A snapshot `{id, version}` of one record, as stored in a question. -/
structure Subject where
  record : RecordId
  version : Nat
  deriving DecidableEq, Repr

structure Question where
  id : QuestionId
  subjects : List Subject
  deriving DecidableEq, Repr

structure Answer where
  id : AnswerId
  question : QuestionId
  subjectsAtAnswer : List Subject
  stale : Bool
  received : Bool
  closed : Bool
  /-- the applied mark on the file: a copy of the commit -/
  appliedAs : Option DecisionId
  deriving DecidableEq, Repr

/-- A decision file `{id, txid}`. -/
structure Decision where
  id : DecisionId
  txid : TxId
  deriving DecidableEq, Repr

/-- `intent {txid, answer, decision_id, subjects}` -/
structure Intent where
  txid : TxId
  answer : AnswerId
  decisionId : DecisionId
  subjects : List Subject
  deriving DecidableEq, Repr

inductive JournalEntry where
  | intent (i : Intent)
  | commit (txid : TxId)
  | abort (txid : TxId)
  deriving DecidableEq, Repr

/-- The persisted state.  `journal` is stored newest-first: appending an
entry is `cons`. -/
structure State where
  records : List (RecordId × Nat)
  questions : List Question
  answers : List Answer
  decisions : List Decision
  journal : List JournalEntry
  /-- next transaction id to allocate -/
  nextTx : TxId
  /-- next decision id to allocate (`D<nextDecision>`) -/
  nextDecision : Nat
  deriving Repr

/-- Initial state: the given records at version `0`, nothing else.
Decision ids start at `1` so that the first decision is `D1`. -/
def init (records : List RecordId) : State :=
  { records := records.map fun r => (r, 0)
    questions := []
    answers := []
    decisions := []
    journal := []
    nextTx := 0
    nextDecision := 1 }

/-! ## Lookups -/

def State.versionOf (s : State) (r : RecordId) : Option Nat :=
  (s.records.find? fun p => p.1 == r).map (·.2)

def State.findQuestion (s : State) (q : QuestionId) : Option Question :=
  s.questions.find? fun x => x.id == q

def State.findAnswer (s : State) (a : AnswerId) : Option Answer :=
  s.answers.find? fun x => x.id == a

/-- Current versions of the given records (`none` if one does not exist). -/
def State.snapshot (s : State) : List RecordId → Option (List Subject)
  | [] => some []
  | r :: rs =>
    match s.versionOf r, s.snapshot rs with
    | some v, some subs => some (⟨r, v⟩ :: subs)
    | _, _ => none

/-- Every subject's version equals the record's current version. -/
def State.subjectsMatch (s : State) (subs : List Subject) : Bool :=
  subs.all fun sub => s.versionOf sub.record == some sub.version

/-! ## Journal-derived state (Section 1, 導出する状態) -/

/-- All intents of a journal. -/
def intentsOf (j : List JournalEntry) : List Intent :=
  j.filterMap fun e => match e with
    | .intent i => some i
    | _ => none

/-- `commit {txid}` is in the journal. -/
def hasCommit (j : List JournalEntry) (tx : TxId) : Prop := JournalEntry.commit tx ∈ j
/-- `abort {txid}` is in the journal. -/
def hasAbort (j : List JournalEntry) (tx : TxId) : Prop := JournalEntry.abort tx ∈ j

instance (j : List JournalEntry) (tx : TxId) : Decidable (hasCommit j tx) :=
  inferInstanceAs (Decidable (_ ∈ _))
instance (j : List JournalEntry) (tx : TxId) : Decidable (hasAbort j tx) :=
  inferInstanceAs (Decidable (_ ∈ _))

def State.intents (s : State) : List Intent := intentsOf s.journal

def State.isCommitted (s : State) (tx : TxId) : Bool := decide (hasCommit s.journal tx)
def State.isAborted (s : State) (tx : TxId) : Bool := decide (hasAbort s.journal tx)

/-- Intents whose transaction is committed. -/
def State.committedIntents (s : State) : List Intent :=
  s.intents.filter fun i => s.isCommitted i.txid

/-- Committed intents for one answer. -/
def State.committedIntentsOf (s : State) (a : AnswerId) : List Intent :=
  s.intents.filter fun i => i.answer == a && s.isCommitted i.txid

/-- `committed(a)`: an `intent(tx, a)` and a `commit(tx)` exist. -/
def State.committed (s : State) (a : AnswerId) : Bool :=
  s.intents.any fun i => i.answer == a && s.isCommitted i.txid

/-- The decision id `a` was committed as, if any. -/
def State.committedAs (s : State) (a : AnswerId) : Option DecisionId :=
  (s.intents.find? fun i => i.answer == a && s.isCommitted i.txid).map (·.decisionId)

/-- Intents with neither `commit` nor `abort`. -/
def State.openIntents (s : State) : List Intent :=
  s.intents.filter fun i => !s.isCommitted i.txid && !s.isAborted i.txid

/-- `inflight`: an intent without `commit` or `abort` exists. -/
def State.inflight (s : State) : Bool := !s.openIntents.isEmpty

/-- The intent to recover (the unique open one, when `inflight`). -/
def State.inflightIntent (s : State) : Option Intent := s.openIntents.head?

/-- `public_decisions`: decisions whose `txid` is committed. -/
def State.publicDecisions (s : State) : List Decision :=
  s.decisions.filter fun d => s.isCommitted d.txid

/-- `pending` computed from the journal: saved answers that are neither
`committed` nor `closed`. -/
def State.pendingFromJournal (s : State) : List AnswerId :=
  (s.answers.filter fun a => !a.closed && !s.committed a.id).map (·.id)

/-- `pending` computed from the files alone: saved answers that are neither
`closed` nor carry an `applied_as` mark. -/
def State.pendingFromFiles (s : State) : List AnswerId :=
  (s.answers.filter fun a => !a.closed && a.appliedAs.isNone).map (·.id)

/-! ## Actions and results (Section 2 / Section 4) -/

/-- Where `apply` crashes: after stage a (`intent`), b (`decision`) or
c (`question`). -/
inductive CrashAfter where
  | intent
  | decision
  | question
  deriving DecidableEq, Repr

inductive Action where
  | createQuestion (q : QuestionId) (subjects : List RecordId)
  | edit (r : RecordId)
  | saveAnswer (q : QuestionId) (a : AnswerId)
  | ack (a : AnswerId)
  | close (a : AnswerId)
  | apply (a : AnswerId) (crashAfter : Option CrashAfter)
  | recover
  deriving DecidableEq, Repr

inductive Result where
  | ok
  | applied (d : DecisionId)
  | alreadyApplied (d : DecisionId)
  | rejectedStale
  | rejectedClosed
  | rejectedInflight
  | crashed
  | recoveredForward
  | recoveredAbort
  | noop
  deriving DecidableEq, Repr

end PrepareDev.Spec
