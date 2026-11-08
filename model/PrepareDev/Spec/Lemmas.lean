import PrepareDev.Spec.Step

/-!
Basic lemmas about the derived state and the stage functions.
-/

namespace PrepareDev.Spec

/-! ## Journal -/

@[simp] theorem mem_intentsOf {j : List JournalEntry} {i : Intent} :
    i ∈ intentsOf j ↔ JournalEntry.intent i ∈ j := by
  simp only [intentsOf, List.mem_filterMap]
  constructor
  · rintro ⟨e, he, h⟩
    cases e <;> simp at h
    subst h; exact he
  · intro h; exact ⟨_, h, rfl⟩

@[simp] theorem intentsOf_cons_intent (i : Intent) (j : List JournalEntry) :
    intentsOf (.intent i :: j) = i :: intentsOf j := by
  simp [intentsOf]

@[simp] theorem intentsOf_cons_commit (tx : TxId) (j : List JournalEntry) :
    intentsOf (.commit tx :: j) = intentsOf j := by
  simp [intentsOf]

@[simp] theorem intentsOf_cons_abort (tx : TxId) (j : List JournalEntry) :
    intentsOf (.abort tx :: j) = intentsOf j := by
  simp [intentsOf]

@[simp] theorem hasCommit_cons_intent (i : Intent) (j : List JournalEntry) (tx : TxId) :
    hasCommit (.intent i :: j) tx ↔ hasCommit j tx := by simp [hasCommit]
@[simp] theorem hasCommit_cons_abort (t : TxId) (j : List JournalEntry) (tx : TxId) :
    hasCommit (.abort t :: j) tx ↔ hasCommit j tx := by simp [hasCommit]
@[simp] theorem hasCommit_cons_commit (t : TxId) (j : List JournalEntry) (tx : TxId) :
    hasCommit (.commit t :: j) tx ↔ tx = t ∨ hasCommit j tx := by simp [hasCommit]
@[simp] theorem hasAbort_cons_intent (i : Intent) (j : List JournalEntry) (tx : TxId) :
    hasAbort (.intent i :: j) tx ↔ hasAbort j tx := by simp [hasAbort]
@[simp] theorem hasAbort_cons_commit (t : TxId) (j : List JournalEntry) (tx : TxId) :
    hasAbort (.commit t :: j) tx ↔ hasAbort j tx := by simp [hasAbort]
@[simp] theorem hasAbort_cons_abort (t : TxId) (j : List JournalEntry) (tx : TxId) :
    hasAbort (.abort t :: j) tx ↔ tx = t ∨ hasAbort j tx := by simp [hasAbort]

@[simp] theorem hasCommit_nil (tx : TxId) : ¬ hasCommit [] tx := by simp [hasCommit]
@[simp] theorem hasAbort_nil (tx : TxId) : ¬ hasAbort [] tx := by simp [hasAbort]

@[simp] theorem isCommitted_eq_true (s : State) (tx : TxId) :
    s.isCommitted tx = true ↔ hasCommit s.journal tx := by simp [State.isCommitted]
@[simp] theorem isAborted_eq_true (s : State) (tx : TxId) :
    s.isAborted tx = true ↔ hasAbort s.journal tx := by simp [State.isAborted]
@[simp] theorem isCommitted_eq_false (s : State) (tx : TxId) :
    s.isCommitted tx = false ↔ ¬ hasCommit s.journal tx := by simp [State.isCommitted]
@[simp] theorem isAborted_eq_false (s : State) (tx : TxId) :
    s.isAborted tx = false ↔ ¬ hasAbort s.journal tx := by simp [State.isAborted]

theorem committed_eq_true_iff (s : State) (a : AnswerId) :
    s.committed a = true ↔ ∃ i ∈ s.intents, i.answer = a ∧ hasCommit s.journal i.txid := by
  simp [State.committed, List.any_eq_true]

theorem committed_eq_false_iff (s : State) (a : AnswerId) :
    s.committed a = false ↔ ∀ i ∈ s.intents, i.answer = a → ¬ hasCommit s.journal i.txid := by
  simp [State.committed, List.any_eq_false]

theorem committedAs_eq_none_iff (s : State) (a : AnswerId) :
    s.committedAs a = none ↔ ∀ i ∈ s.intents, i.answer = a → ¬ hasCommit s.journal i.txid := by
  rw [State.committedAs, Option.map_eq_none_iff, List.find?_eq_none]
  simp [State.isCommitted]

theorem committedAs_eq_some (s : State) (a : AnswerId) (d : DecisionId)
    (h : s.committedAs a = some d) :
    ∃ i ∈ s.intents, i.answer = a ∧ hasCommit s.journal i.txid ∧ i.decisionId = d := by
  rw [State.committedAs, Option.map_eq_some_iff] at h
  obtain ⟨i, hf, rfl⟩ := h
  have hm := List.mem_of_find?_eq_some hf
  have hp := List.find?_some hf
  simp [State.isCommitted] at hp
  exact ⟨i, hm, hp.1, hp.2, rfl⟩

theorem committedAs_eq_none_of_committed_eq_false (s : State) (a : AnswerId)
    (h : s.committed a = false) : s.committedAs a = none := by
  rw [committedAs_eq_none_iff]; rwa [committed_eq_false_iff] at h

/-! ## inflight -/

theorem inflight_eq_false_iff (s : State) :
    s.inflight = false ↔ ∀ i ∈ s.intents, hasCommit s.journal i.txid ∨ hasAbort s.journal i.txid := by
  simp only [State.inflight, State.openIntents, State.isCommitted, State.isAborted]
  simp [List.filter_eq_nil_iff]
  constructor
  · intro h i hi
    by_cases hc : hasCommit s.journal i.txid
    · exact Or.inl hc
    · exact Or.inr (h i hi hc)
  · intro h i hi hc
    rcases h i hi with hc' | ha
    · exact absurd hc' hc
    · exact ha

theorem inflightIntent_some (s : State) (i : Intent) (h : s.inflightIntent = some i) :
    i ∈ s.intents ∧ ¬ hasCommit s.journal i.txid ∧ ¬ hasAbort s.journal i.txid := by
  have hm : i ∈ s.openIntents :=
    List.mem_of_mem_head? (by simpa [Option.mem_def, State.inflightIntent] using h)
  simp only [State.openIntents, List.mem_filter] at hm
  simpa [State.isCommitted, State.isAborted] using hm

theorem inflightIntent_eq_none_iff (s : State) :
    s.inflightIntent = none ↔ s.inflight = false := by
  simp [State.inflightIntent, State.inflight, List.head?_eq_none_iff, List.isEmpty_iff]

/-! ## Key uniqueness from `Pairwise` -/

theorem pairwise_key_unique {α κ : Type} (key : α → κ) {l : List α}
    (h : l.Pairwise fun x y => key x ≠ key y) :
    ∀ x ∈ l, ∀ y ∈ l, key x = key y → x = y := by
  induction l with
  | nil => simp
  | cons a l ih =>
    rw [List.pairwise_cons] at h
    intro x hx y hy hk
    simp only [List.mem_cons] at hx hy
    rcases hx with rfl | hx <;> rcases hy with rfl | hy
    · rfl
    · exact absurd hk (h.1 y hy)
    · exact absurd hk.symm (h.1 x hx)
    · exact ih h.2 x hx y hy hk

theorem pairwise_key_of_map {α κ : Type} (key : α → κ) (f : α → α)
    (hf : ∀ x, key (f x) = key x) {l : List α}
    (h : l.Pairwise fun x y => key x ≠ key y) :
    (l.map f).Pairwise fun x y => key x ≠ key y := by
  rw [List.pairwise_map]
  exact h.imp fun {x y} hxy => by simpa [hf] using hxy

/-! ## updateAnswer -/

theorem mem_updateAnswer {s : State} {a : AnswerId} {f : Answer → Answer} {y : Answer} :
    y ∈ (s.updateAnswer a f).answers ↔ ∃ x ∈ s.answers, y = (if x.id = a then f x else x) := by
  simp only [State.updateAnswer, List.mem_map]
  constructor
  · rintro ⟨x, hx, rfl⟩; exact ⟨x, hx, rfl⟩
  · rintro ⟨x, hx, rfl⟩; exact ⟨x, hx, rfl⟩

theorem updateAnswer_mem {s : State} {a : AnswerId} {f : Answer → Answer} {x : Answer}
    (hx : x ∈ s.answers) : (if x.id = a then f x else x) ∈ (s.updateAnswer a f).answers := by
  simp only [State.updateAnswer]
  exact List.mem_map_of_mem hx

@[simp] theorem updateAnswer_journal (s : State) (a : AnswerId) (f : Answer → Answer) :
    (s.updateAnswer a f).journal = s.journal := rfl
@[simp] theorem updateAnswer_intents (s : State) (a : AnswerId) (f : Answer → Answer) :
    (s.updateAnswer a f).intents = s.intents := rfl
@[simp] theorem updateAnswer_questions (s : State) (a : AnswerId) (f : Answer → Answer) :
    (s.updateAnswer a f).questions = s.questions := rfl
@[simp] theorem updateAnswer_decisions (s : State) (a : AnswerId) (f : Answer → Answer) :
    (s.updateAnswer a f).decisions = s.decisions := rfl
@[simp] theorem updateAnswer_records (s : State) (a : AnswerId) (f : Answer → Answer) :
    (s.updateAnswer a f).records = s.records := rfl
@[simp] theorem updateAnswer_nextTx (s : State) (a : AnswerId) (f : Answer → Answer) :
    (s.updateAnswer a f).nextTx = s.nextTx := rfl
@[simp] theorem updateAnswer_nextDecision (s : State) (a : AnswerId) (f : Answer → Answer) :
    (s.updateAnswer a f).nextDecision = s.nextDecision := rfl

theorem updateAnswer_pairwise (s : State) (a : AnswerId) (f : Answer → Answer)
    (hf : ∀ x, (f x).id = x.id)
    (h : s.answers.Pairwise fun x y => x.id ≠ y.id) :
    (s.updateAnswer a f).answers.Pairwise fun x y => x.id ≠ y.id := by
  simp only [State.updateAnswer]
  apply pairwise_key_of_map Answer.id _ _ h
  intro x; split <;> simp [hf]

/-! ## Stage functions: projections -/

@[simp] theorem newIntent_txid (s : State) (a : Answer) (q : Question) :
    (newIntent s a q).txid = s.nextTx := rfl
@[simp] theorem newIntent_answer (s : State) (a : Answer) (q : Question) :
    (newIntent s a q).answer = a.id := rfl
@[simp] theorem newIntent_decisionId (s : State) (a : Answer) (q : Question) :
    (newIntent s a q).decisionId = s.nextDecision := rfl
@[simp] theorem newIntent_subjects (s : State) (a : Answer) (q : Question) :
    (newIntent s a q).subjects = q.subjects := rfl

@[simp] theorem stageA_journal (s : State) (i : Intent) :
    (stageA s i).journal = .intent i :: s.journal := rfl
@[simp] theorem stageA_intents (s : State) (i : Intent) :
    (stageA s i).intents = i :: s.intents := by simp [State.intents, stageA]
@[simp] theorem stageA_answers (s : State) (i : Intent) : (stageA s i).answers = s.answers := rfl
@[simp] theorem stageA_questions (s : State) (i : Intent) :
    (stageA s i).questions = s.questions := rfl
@[simp] theorem stageA_decisions (s : State) (i : Intent) :
    (stageA s i).decisions = s.decisions := rfl
@[simp] theorem stageA_records (s : State) (i : Intent) : (stageA s i).records = s.records := rfl
@[simp] theorem stageA_nextTx (s : State) (i : Intent) : (stageA s i).nextTx = s.nextTx + 1 := rfl
@[simp] theorem stageA_nextDecision (s : State) (i : Intent) :
    (stageA s i).nextDecision = s.nextDecision + 1 := rfl

@[simp] theorem stageB_journal (s : State) (i : Intent) : (stageB s i).journal = s.journal := by
  unfold stageB; split <;> rfl
@[simp] theorem stageB_intents (s : State) (i : Intent) : (stageB s i).intents = s.intents := by
  simp [State.intents]
@[simp] theorem stageB_answers (s : State) (i : Intent) : (stageB s i).answers = s.answers := by
  unfold stageB; split <;> rfl
@[simp] theorem stageB_questions (s : State) (i : Intent) :
    (stageB s i).questions = s.questions := by
  unfold stageB; split <;> rfl
@[simp] theorem stageB_records (s : State) (i : Intent) : (stageB s i).records = s.records := by
  unfold stageB; split <;> rfl
@[simp] theorem stageB_nextTx (s : State) (i : Intent) : (stageB s i).nextTx = s.nextTx := by
  unfold stageB; split <;> rfl
@[simp] theorem stageB_nextDecision (s : State) (i : Intent) :
    (stageB s i).nextDecision = s.nextDecision := by
  unfold stageB; split <;> rfl
theorem mem_stageB_decisions {s : State} {i : Intent} {d : Decision} :
    d ∈ (stageB s i).decisions ↔ d ∈ s.decisions ∨ d = ⟨i.decisionId, i.txid⟩ := by
  unfold stageB; split
  · rename_i h
    constructor
    · exact Or.inl
    · rintro (hd | rfl) <;> assumption
  · simp [List.mem_cons, or_comm]
theorem stageB_decision_mem (s : State) (i : Intent) :
    (⟨i.decisionId, i.txid⟩ : Decision) ∈ (stageB s i).decisions := by
  rw [mem_stageB_decisions]; exact Or.inr rfl
theorem stageB_decisions_mono {s : State} {i : Intent} {d : Decision} (h : d ∈ s.decisions) :
    d ∈ (stageB s i).decisions := by
  rw [mem_stageB_decisions]; exact Or.inl h

@[simp] theorem stageC_journal (s : State) (i : Intent) : (stageC s i).journal = s.journal := rfl
@[simp] theorem stageC_intents (s : State) (i : Intent) : (stageC s i).intents = s.intents := rfl
@[simp] theorem stageC_questions (s : State) (i : Intent) :
    (stageC s i).questions = s.questions := rfl
@[simp] theorem stageC_decisions (s : State) (i : Intent) :
    (stageC s i).decisions = s.decisions := rfl
@[simp] theorem stageC_records (s : State) (i : Intent) : (stageC s i).records = s.records := rfl
@[simp] theorem stageC_nextTx (s : State) (i : Intent) : (stageC s i).nextTx = s.nextTx := rfl
@[simp] theorem stageC_nextDecision (s : State) (i : Intent) :
    (stageC s i).nextDecision = s.nextDecision := rfl
theorem mem_stageC_answers {s : State} {i : Intent} {y : Answer} :
    y ∈ (stageC s i).answers ↔
      ∃ x ∈ s.answers, y = (if x.id = i.answer then { x with appliedAs := some i.decisionId } else x) :=
  mem_updateAnswer

@[simp] theorem stageD_journal (s : State) (i : Intent) :
    (stageD s i).journal = .commit i.txid :: s.journal := rfl
@[simp] theorem stageD_intents (s : State) (i : Intent) : (stageD s i).intents = s.intents := by
  simp [State.intents, stageD]
@[simp] theorem stageD_answers (s : State) (i : Intent) : (stageD s i).answers = s.answers := rfl
@[simp] theorem stageD_questions (s : State) (i : Intent) :
    (stageD s i).questions = s.questions := rfl
@[simp] theorem stageD_decisions (s : State) (i : Intent) :
    (stageD s i).decisions = s.decisions := rfl
@[simp] theorem stageD_records (s : State) (i : Intent) : (stageD s i).records = s.records := rfl
@[simp] theorem stageD_nextTx (s : State) (i : Intent) : (stageD s i).nextTx = s.nextTx := rfl
@[simp] theorem stageD_nextDecision (s : State) (i : Intent) :
    (stageD s i).nextDecision = s.nextDecision := rfl

@[simp] theorem rollForward_journal (s : State) (i : Intent) :
    (rollForward s i).journal = .commit i.txid :: s.journal := by simp [rollForward]
@[simp] theorem rollForward_intents (s : State) (i : Intent) :
    (rollForward s i).intents = s.intents := by simp [rollForward]
@[simp] theorem rollForward_records (s : State) (i : Intent) :
    (rollForward s i).records = s.records := by simp [rollForward]
@[simp] theorem rollForward_questions (s : State) (i : Intent) :
    (rollForward s i).questions = s.questions := by simp [rollForward]
theorem rollForward_answers (s : State) (i : Intent) :
    (rollForward s i).answers = (stageC (stageB s i) i).answers := rfl

@[simp] theorem abortTx_journal (s : State) (i : Intent) :
    (abortTx s i).journal = .abort i.txid :: s.journal := rfl
@[simp] theorem abortTx_intents (s : State) (i : Intent) : (abortTx s i).intents = s.intents := by
  simp [State.intents, abortTx, State.updateAnswer]
@[simp] theorem abortTx_questions (s : State) (i : Intent) :
    (abortTx s i).questions = s.questions := rfl
@[simp] theorem abortTx_decisions (s : State) (i : Intent) :
    (abortTx s i).decisions = s.decisions.filter fun d => d.txid != i.txid := rfl
@[simp] theorem abortTx_records (s : State) (i : Intent) : (abortTx s i).records = s.records := rfl
@[simp] theorem abortTx_nextTx (s : State) (i : Intent) : (abortTx s i).nextTx = s.nextTx := rfl
@[simp] theorem abortTx_nextDecision (s : State) (i : Intent) :
    (abortTx s i).nextDecision = s.nextDecision := rfl
theorem mem_abortTx_answers {s : State} {i : Intent} {y : Answer} :
    y ∈ (abortTx s i).answers ↔
      ∃ x ∈ s.answers,
        y = (if x.id = i.answer then { x with appliedAs := none, stale := true } else x) := by
  unfold abortTx
  exact mem_updateAnswer

/-! ## subjectsMatch only depends on records -/

theorem subjectsMatch_congr {s s' : State} (h : s'.records = s.records) (subs : List Subject) :
    s'.subjectsMatch subs = s.subjectsMatch subs := by
  simp [State.subjectsMatch, State.versionOf, h]

end PrepareDev.Spec
