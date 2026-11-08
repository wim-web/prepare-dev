import PrepareDev.Spec.Step
import PrepareDev.Gen.Json

/-!
# SPEC-0001 Section 4: trace generation

Deterministic depth-first enumeration of action sequences over a small fixed
alphabet, printing one JSON line per step.

Alphabet: records `R1`, `R2`; questions `Q1` (subjects `[R1]`) and `Q2`
(subjects `[R1, R2]`); answers `A1` (for `Q1`) and `A2` (for `Q2`).

Pruning (only sequences that are obviously pointless are dropped):
* `create_question` only for a question that does not exist yet;
* `edit` only of a record that is a subject of an existing question, and not
  twice in a row for the same record;
* `save_answer` only for an existing question and a not-yet-saved answer;
* `ack` only for an existing answer that is not yet `received`;
* `close` only for an existing answer that is not yet `closed`;
* `apply` only for an existing answer; the `crash_after` variants are
  generated only when the non-crashing `apply` would actually reach stage a;
* while `inflight`, only `edit` and `recover` are generated (Section 4);
  `recover` is generated only while `inflight`.
-/

namespace PrepareDev.Gen

open PrepareDev.Spec

structure Config where
  depth : Nat
  maxTraces : Nat := 2000
  seed : Nat := 0

def recordIds : List RecordId := ["R1", "R2"]

def questionAlphabet : List (QuestionId × List RecordId) :=
  [("Q1", ["R1"]), ("Q2", ["R1", "R2"])]

def answerAlphabet : List (AnswerId × QuestionId) :=
  [("A1", "Q1"), ("A2", "Q2")]

def crashAlphabet : List (Option CrashAfter) :=
  [none, some .intent, some .decision, some .question]

/-- The initial state of every trace. -/
def initialState : State := init recordIds

/-! ## Candidate actions -/

def editCandidates (s : State) (prev : Option Action) : List Action :=
  recordIds.filterMap fun r =>
    let inQuestion := s.questions.any fun q => q.subjects.any fun sub => sub.record == r
    let repeated := prev == some (.edit r)
    if inQuestion && !repeated then some (.edit r) else none

def candidates (s : State) (prev : Option Action) : List Action :=
  if s.inflight then
    editCandidates s prev ++ [.recover]
  else
    let creates := questionAlphabet.filterMap fun (q, subs) =>
      if (s.findQuestion q).isNone then some (.createQuestion q subs) else none
    let saves := answerAlphabet.filterMap fun (a, q) =>
      if (s.findQuestion q).isSome && (s.findAnswer a).isNone then some (.saveAnswer q a) else none
    let acks := answerAlphabet.filterMap fun (a, _) =>
      match s.findAnswer a with
      | some ans => if ans.received then none else some (.ack a)
      | none => none
    let closes := answerAlphabet.filterMap fun (a, _) =>
      match s.findAnswer a with
      | some ans => if ans.closed then none else some (.close a)
      | none => none
    let applies := answerAlphabet.flatMap fun (a, _) =>
      if (s.findAnswer a).isSome then
        let reaches := match (step s (.apply a none)).2 with
          | .applied _ => true
          | _ => false
        crashAlphabet.filterMap fun c =>
          if c.isNone || reaches then some (.apply a c) else none
      else []
    creates ++ editCandidates s prev ++ saves ++ acks ++ closes ++ applies

/-- Rotate a list by `n` positions (used by `--seed`). -/
def rotate (n : Nat) (xs : List Action) : List Action :=
  if xs.isEmpty then xs
  else
    let k := n % xs.length
    xs.drop k ++ xs.take k

/-! ## Observation -/

def decisionName (d : DecisionId) : String := "D" ++ toString d

def insertSorted (x : String) : List String → List String
  | [] => [x]
  | y :: ys => if x < y then x :: y :: ys else y :: insertSorted x ys

def sortStrings (xs : List String) : List String := xs.foldr insertSorted []

def insertSortedNat (x : Nat) : List Nat → List Nat
  | [] => [x]
  | y :: ys => if x < y then x :: y :: ys else y :: insertSortedNat x ys

def sortNats (xs : List Nat) : List Nat := xs.foldr insertSortedNat []

def resultName : Result → String
  | .ok => "ok"
  | .applied _ => "applied"
  | .alreadyApplied _ => "already_applied"
  | .rejectedStale => "rejected_stale"
  | .rejectedClosed => "rejected_closed"
  | .rejectedInflight => "rejected_inflight"
  | .crashed => "crashed"
  | .recoveredForward => "recovered_forward"
  | .recoveredAbort => "recovered_abort"
  | .noop => "noop"

def observe (s : State) (r : Result) : Json :=
  let ids := sortStrings (s.answers.map (·.id))
  let committed := ids.filterMap fun a =>
    match s.committedAs a with
    | some d => some (a, Json.str (decisionName d))
    | none => none
  let closed := ids.filter fun a =>
    match s.findAnswer a with
    | some ans => ans.closed
    | none => false
  let stale := ids.filter fun a =>
    match s.findAnswer a with
    | some ans => ans.stale
    | none => false
  let pub := (sortNats (s.publicDecisions.map (·.id))).map decisionName
  .obj
    [ ("inflight", .bool s.inflight)
    , ("pending", Json.strs (sortStrings s.pendingFromJournal))
    , ("committed", .obj committed)
    , ("closed", Json.strs closed)
    , ("stale", Json.strs stale)
    , ("public_decisions", Json.strs pub)
    , ("result", .str (resultName r)) ]

def crashName : Option CrashAfter → Json
  | none => .null
  | some .intent => .str "intent"
  | some .decision => .str "decision"
  | some .question => .str "question"

def actionJson : Action → Json
  | .createQuestion q subs =>
    .obj [("type", .str "create_question"), ("question", .str q), ("subjects", Json.strs subs)]
  | .edit r => .obj [("type", .str "edit"), ("record", .str r)]
  | .saveAnswer q a =>
    .obj [("type", .str "save_answer"), ("question", .str q), ("answer", .str a)]
  | .ack a => .obj [("type", .str "ack"), ("answer", .str a)]
  | .close a => .obj [("type", .str "close"), ("answer", .str a)]
  | .apply a c => .obj [("type", .str "apply"), ("answer", .str a), ("crash_after", crashName c)]
  | .recover => .obj [("type", .str "recover")]

def lineJson (trace stepIdx : Nat) (act : Action) (s : State) (r : Result) : String :=
  Json.render <| .obj
    [ ("trace", .num trace)
    , ("step", .num stepIdx)
    , ("action", actionJson act)
    , ("observe", observe s r) ]

/-! ## Enumeration -/

structure GenState where
  traces : Nat := 0
  lines : Array String := #[]

abbrev GenM := StateM GenState

def emit (steps : Array (Action × Result × State)) : GenM Unit := do
  if steps.isEmpty then return
  let t := (← get).traces
  let mut i := 0
  for (act, r, s) in steps do
    modify fun g => { g with lines := g.lines.push (lineJson t i act s r) }
    i := i + 1
  modify fun g => { g with traces := g.traces + 1 }

def dfs (cfg : Config) :
    Nat → State → Option Action → Array (Action × Result × State) → GenM Unit
  | 0, _, _, steps => emit steps
  | fuel + 1, s, prev, steps => do
    if (← get).traces ≥ cfg.maxTraces then return
    let cands := rotate cfg.seed (candidates s prev)
    if cands.isEmpty then
      emit steps
    else
      for act in cands do
        if (← get).traces ≥ cfg.maxTraces then return
        let (s', r) := step s act
        dfs cfg fuel s' (some act) (steps.push (act, r, s'))

/-- All trace lines for the configuration, in order. -/
def generate (cfg : Config) : Array String :=
  let (_, g) := (dfs cfg cfg.depth initialState none #[]).run {}
  g.lines

end PrepareDev.Gen
