/-!
CancelRule.lean の続きとして読み込む断片。単独では使わない。
検証したモデルをそのまま実行して、次の二つを書き出す。

- `vectors N`：長さ N までの出来事の並びすべてと、その結果の状態。コードとの突き合わせに使う。
- `edges`：実行一つについての状態と、その間の動き。図の元にする。
-/

def showAnswer : Answer → String
  | .noFindings => "N"
  | .finding => "F"

def showEvent : Event → String
  | .start r => s!"s{r}"
  | .cancel r => s!"c{r}"
  | .answer r a fresh => s!"a{r}{showAnswer a}{if fresh then "t" else "f"}"

def showEntry (e : RunId × Answer) : String := s!"{e.1}{showAnswer e.2}"

/-- 状態を一行にする。取消、履歴（新しいものが先頭）、現在の結果の順。 -/
def showState (s : State) : String :=
  let cancelled := ",".intercalate (([1, 2].filter (· ∈ s.cancelled)).map toString)
  let history := ",".intercalate (s.history.map showEntry)
  let current := match s.current with
    | none => "-"
    | some e => showEntry e
  s!"{cancelled}\t{history}\t{current}"

def vectorAlphabet : List Event :=
  [.start 1, .start 2, .cancel 1, .cancel 2] ++
  ([1, 2].flatMap fun r =>
    [Answer.noFindings, Answer.finding].flatMap fun a =>
      [true, false].map fun f => Event.answer r a f)

/-- ちょうど長さ n の並びすべて。 -/
def seqsOfLength : Nat → List (List Event)
  | 0 => [[]]
  | n + 1 => (seqsOfLength n).flatMap fun es => vectorAlphabet.map fun e => es ++ [e]

def printVectors (maxLen : Nat) : IO Unit := do
  for n in List.range (maxLen + 1) do
    for es in seqsOfLength n do
      IO.println s!"{" ".intercalate (es.map showEvent)}\t{showState (run init es)}"

/-! ## 図の元になる動き：実行 1 を始めたあとを、長さ 3 まで調べる -/

/-- 実行 1 が、利用者から見てどの状態にあるか。 -/
def phase (s : State) : String :=
  if 1 ∈ s.cancelled then "取消済み"
  else match s.current with
    | some (1, _) => "回答を採用した"
    | _ => "実行中"

/-- 採用されたかどうかが状態の差で見えるように、いまの結果と違う回答を届ける。 -/
def probeAnswer (s : State) : Answer :=
  match s.current with
  | some (_, .finding) => .noFindings
  | _ => .finding

def figureEvents (s : State) : List (Event × String) :=
  [ (.cancel 1, "取り消す"),
    (.answer 1 (probeAnswer s) true, "回答が届く"),
    (.answer 1 (probeAnswer s) false, "古い内容への回答が届く") ]

/-- 動きの結果を、利用者に見える言葉にする。 -/
def effect (before after : State) : String :=
  let cancelled := if after.cancelled.length > before.cancelled.length then "取消を記録" else ""
  let saved := if after.history.length > before.history.length then "履歴へ保存" else ""
  let adopted :=
    if after.current != before.current then "現在の結果に採用"
    else if after.history.length > before.history.length then "現在の結果は変えない"
    else ""
  "、".intercalate ([cancelled, saved, adopted].filter (· != ""))

def edgesFrom (s : State) : List String :=
  (figureEvents s).map fun (e, label) =>
    let s' := step s e
    s!"EDGE\t{phase s}\t{label}\t{effect s s'}\t{phase s'}"

def reachable : Nat → List State
  | 0 => [step init (.start 1)]
  | n + 1 => (reachable n).flatMap fun s => (figureEvents s).map fun (e, _) => step s e

def printEdges : IO Unit := do
  let states := (List.range 3).flatMap reachable
  let edges := (states.flatMap edgesFrom).eraseDups
  IO.println "BOUND\t実行一つを始めたあと、出来事三つまで"
  for e in edges do
    IO.println e

def main (args : List String) : IO UInt32 := do
  match args with
  | ["vectors", n] =>
      match n.toNat? with
      | some k => printVectors k; return 0
      | none => IO.eprintln "vectors の長さは数で渡す"; return 2
  | ["edges"] => printEdges; return 0
  | _ => IO.eprintln "使い方: vectors <長さ> | edges"; return 2
