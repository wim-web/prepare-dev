/-!
# 取り消した実行から遅れて届いた回答の扱い

## ルール
- 取消は実行ごとに記録する。
- 届いた回答は、取消の有無にかかわらず、その実行の履歴へ残す。
- 取り消していない実行の、対象の現在の内容に対する回答だけを、現在の結果に採用する。

## 証明する主張
1. 実行を取り消したあとに届くその実行の回答は、どんな順番のあとでも現在の結果を変えない。
2. 取り消していない実行の、現在の内容に対する回答は、どんな順番のあとでも現在の結果に採用される。
3. 届いた回答は、どんな順番のあとでも履歴に残る。
4. 古い内容に対する回答は、どんな順番のあとでも現在の結果を変えない。

## 前提（このモデルでは確かめていないこと）
- 回答を受け取ってから現在の結果を更新するまでの間に、別の出来事は割り込まない。
- 履歴への保存は失敗しない。
- 回答が現在の内容に対するものかどうか（`fresh`）は、正しく判定されて渡される。

## 対象外
- 取り消していない実行が複数あるときに、どの回答を現在の結果とするか。
  このモデルでは、あとから届いた回答が現在の結果になる。
- 採用したあとで実行を取り消した場合に、現在の結果を取り下げるかどうか。
  このモデルでは取り下げない。
-/

abbrev RunId := Nat

/-- レビューの回答。中身は区別できれば足りるので、二種類だけ置く。 -/
inductive Answer where
  | noFindings
  | finding
deriving Repr, DecidableEq

/-- 起きる出来事。`fresh` は、回答が対象の現在の内容に対するものかどうか。 -/
inductive Event where
  | start  (r : RunId)
  | cancel (r : RunId)
  | answer (r : RunId) (a : Answer) (fresh : Bool)
deriving Repr, DecidableEq

structure State where
  /-- 取り消した実行。 -/
  cancelled : List RunId
  /-- 届いた回答の履歴。新しいものが先頭。 -/
  history   : List (RunId × Answer)
  /-- 現在の結果と、それを返した実行。 -/
  current   : Option (RunId × Answer)
deriving Repr, DecidableEq

def init : State := { cancelled := [], history := [], current := none }

/-- 出来事を一つ処理する。ルールの本体。 -/
def step (s : State) : Event → State
  | .start _ => s
  | .cancel r => { s with cancelled := r :: s.cancelled }
  | .answer r a fresh =>
      if r ∈ s.cancelled ∨ fresh = false then
        { s with history := (r, a) :: s.history }
      else
        { s with history := (r, a) :: s.history, current := some (r, a) }

/-- 出来事の並びを、先頭から順に処理する。 -/
def run (s : State) (es : List Event) : State := es.foldl step s

/-! ## 出来事一つについての補題 -/

theorem late_answer_keeps_current (s : State) (r : RunId) (a : Answer) (fresh : Bool)
    (h : r ∈ s.cancelled) : (step s (.answer r a fresh)).current = s.current := by
  simp [step, h]

theorem stale_answer_keeps_current (s : State) (r : RunId) (a : Answer) :
    (step s (.answer r a false)).current = s.current := by
  simp [step]

theorem live_answer_adopted (s : State) (r : RunId) (a : Answer)
    (h : r ∉ s.cancelled) : (step s (.answer r a true)).current = some (r, a) := by
  simp [step, h]

theorem answer_saved (s : State) (r : RunId) (a : Answer) (fresh : Bool) :
    (r, a) ∈ (step s (.answer r a fresh)).history := by
  by_cases h : r ∈ s.cancelled <;> cases fresh <;> simp [step, h]

/-- 一度記録した取消は、どの出来事でも消えない。 -/
theorem cancelled_stays (s : State) (e : Event) (r : RunId)
    (h : r ∈ s.cancelled) : r ∈ (step s e).cancelled := by
  cases e with
  | start r' => simpa [step] using h
  | cancel r' => simp [step, h]
  | answer r' a fresh =>
      by_cases h' : r' ∈ s.cancelled <;> cases fresh <;> simp [step, h', h]

/-! ## 出来事の並びについての補題 -/

theorem cancelled_stays_run (es : List Event) (s : State) (r : RunId)
    (h : r ∈ s.cancelled) : r ∈ (run s es).cancelled := by
  induction es generalizing s with
  | nil => simpa [run] using h
  | cons e es ih =>
      simp only [run, List.foldl_cons]
      exact ih (step s e) (cancelled_stays s e r h)

/-- 取消の出来事があれば、取消は記録されている。 -/
theorem cancel_recorded (pre : List Event) (s : State) (r : RunId)
    (h : Event.cancel r ∈ pre) : r ∈ (run s pre).cancelled := by
  induction pre generalizing s with
  | nil => simp at h
  | cons e es ih =>
      simp only [run, List.foldl_cons]
      rcases List.mem_cons.mp h with h | h
      · subst h
        exact cancelled_stays_run es _ r (by simp [step])
      · exact ih (step s e) h

/-- 取消が記録されているなら、最初から記録されていたか、取消の出来事があった。 -/
theorem cancelled_only_by_cancel (pre : List Event) (s : State) (r : RunId)
    (h : r ∈ (run s pre).cancelled) : r ∈ s.cancelled ∨ Event.cancel r ∈ pre := by
  induction pre generalizing s with
  | nil => exact Or.inl (by simpa [run] using h)
  | cons e es ih =>
      simp only [run, List.foldl_cons] at h
      rcases ih (step s e) h with h' | h'
      · cases e with
        | start r' => exact Or.inl (by simpa [step] using h')
        | cancel r' =>
            simp [step] at h'
            rcases h' with h' | h'
            · exact Or.inr (by simp [h'])
            · exact Or.inl h'
        | answer r' a fresh =>
            refine Or.inl ?_
            by_cases hc : r' ∈ s.cancelled <;> cases fresh <;> simpa [step, hc] using h'
      · exact Or.inr (List.mem_cons_of_mem _ h')

/-! ## 主張 -/

/-- 主張1：実行 r を取り消したあとに届く r の回答は、現在の結果を変えない。 -/
theorem cancelled_run_never_updates_current
    (pre : List Event) (r : RunId) (a : Answer) (fresh : Bool)
    (h : Event.cancel r ∈ pre) :
    (run init (pre ++ [Event.answer r a fresh])).current = (run init pre).current := by
  have hc := cancel_recorded pre init r h
  simp only [run, List.foldl_append, List.foldl_cons, List.foldl_nil]
  exact late_answer_keeps_current _ r a fresh hc

/-- 主張2：取り消していない実行 r の、現在の内容に対する回答は採用される。 -/
theorem live_run_answer_is_adopted
    (pre : List Event) (r : RunId) (a : Answer)
    (h : Event.cancel r ∉ pre) :
    (run init (pre ++ [Event.answer r a true])).current = some (r, a) := by
  have hc : r ∉ (run init pre).cancelled := by
    intro hin
    rcases cancelled_only_by_cancel pre init r hin with h' | h'
    · simp [init] at h'
    · exact h h'
  simp only [run, List.foldl_append, List.foldl_cons, List.foldl_nil]
  exact live_answer_adopted _ r a hc

/-- 主張3：届いた回答は履歴に残る。 -/
theorem answer_is_always_saved
    (pre : List Event) (r : RunId) (a : Answer) (fresh : Bool) :
    (r, a) ∈ (run init (pre ++ [Event.answer r a fresh])).history := by
  simp only [run, List.foldl_append, List.foldl_cons, List.foldl_nil]
  exact answer_saved _ r a fresh

/-- 主張4：古い内容に対する回答は、現在の結果を変えない。 -/
theorem stale_answer_never_updates_current
    (pre : List Event) (r : RunId) (a : Answer) :
    (run init (pre ++ [Event.answer r a false])).current = (run init pre).current := by
  simp only [run, List.foldl_append, List.foldl_cons, List.foldl_nil]
  exact stale_answer_keeps_current _ r a

/-! ## 具体例 -/

/-- 1回目を取り消し、2回目が指摘を返し、そのあとに1回目の「指摘なし」が届く。 -/
def retryScenario : List Event :=
  [.start 1, .cancel 1, .start 2, .answer 2 .finding true, .answer 1 .noFindings true]

/-- 現在の結果は、2回目の指摘のまま。 -/
theorem example_retry_keeps_second_result :
    (run init retryScenario).current = some (2, .finding) := by decide

/-- 1回目の回答も、履歴には残っている。 -/
theorem example_retry_first_answer_in_history :
    (1, Answer.noFindings) ∈ (run init retryScenario).history := by decide

/-- 取り消す前に届いた回答は採用される。 -/
theorem example_answer_before_cancel_is_adopted :
    (run init [.start 1, .answer 1 .finding true]).current = some (1, .finding) := by
  decide

/-- 古い内容に対する回答は採用されない。 -/
theorem example_stale_answer_is_not_adopted :
    (run init [.start 1, .answer 1 .finding false]).current = none := by decide
