import PrepareDev.Spec.Lemmas
import PrepareDev.Spec.Reachable

/-!
# The inductive invariant

`Inv s` is a conjunction of properties preserved by every `step`.  The
named theorems of SPEC-0001 Section 3 are derived from it in
`PrepareDev.Spec.Theorems`.
-/

namespace PrepareDev.Spec

/-- What holds of an intent `i` that has not been aborted: its answer and
question exist, the question's snapshot equals `i.subjects`, the answer is
neither closed nor stale, and the applied mark / decision file agree with the
journal (fully present once committed, at most provisional before). -/
def LiveIntent (s : State) (i : Intent) : Prop :=
  ∃ a ∈ s.answers, a.id = i.answer ∧ a.closed = false ∧ a.stale = false ∧
    (∃ q ∈ s.questions, q.id = a.question ∧ q.subjects = i.subjects) ∧
    (hasCommit s.journal i.txid →
      a.appliedAs = some i.decisionId ∧ (⟨i.decisionId, i.txid⟩ : Decision) ∈ s.decisions) ∧
    (¬ hasCommit s.journal i.txid → a.appliedAs = none ∨ a.appliedAs = some i.decisionId)

structure Inv (s : State) : Prop where
  answersUniq : s.answers.Pairwise fun x y => x.id ≠ y.id
  questionsUniq : s.questions.Pairwise fun x y => x.id ≠ y.id
  txUniq : s.intents.Pairwise fun x y => x.txid ≠ y.txid
  txLt : ∀ i ∈ s.intents, i.txid < s.nextTx
  decUniq : s.intents.Pairwise fun x y => x.decisionId ≠ y.decisionId
  decLt : ∀ i ∈ s.intents, i.decisionId < s.nextDecision
  commitRef : ∀ tx, hasCommit s.journal tx → ∃ i ∈ s.intents, i.txid = tx
  abortRef : ∀ tx, hasAbort s.journal tx → ∃ i ∈ s.intents, i.txid = tx
  commitAbort : ∀ tx, hasCommit s.journal tx → ¬ hasAbort s.journal tx
  live : ∀ i ∈ s.intents, ¬ hasAbort s.journal i.txid → LiveIntent s i
  ansOnce : ∀ i ∈ s.intents, ∀ j ∈ s.intents, ¬ hasAbort s.journal i.txid →
    ¬ hasAbort s.journal j.txid → i.answer = j.answer → i.txid = j.txid
  appliedRef : ∀ a ∈ s.answers, ∀ d, a.appliedAs = some d →
    ∃ i ∈ s.intents, i.answer = a.id ∧ i.decisionId = d ∧ ¬ hasAbort s.journal i.txid
  decRef : ∀ d ∈ s.decisions,
    ∃ i ∈ s.intents, i.txid = d.txid ∧ i.decisionId = d.id ∧ ¬ hasAbort s.journal i.txid

/-! ## Consequences of `Inv` -/

theorem Inv.tx_inj {s : State} (h : Inv s) :
    ∀ i ∈ s.intents, ∀ j ∈ s.intents, i.txid = j.txid → i = j :=
  pairwise_key_unique Intent.txid h.txUniq

theorem Inv.dec_inj {s : State} (h : Inv s) :
    ∀ i ∈ s.intents, ∀ j ∈ s.intents, i.decisionId = j.decisionId → i = j :=
  pairwise_key_unique Intent.decisionId h.decUniq

theorem Inv.answer_inj {s : State} (h : Inv s) :
    ∀ a ∈ s.answers, ∀ b ∈ s.answers, a.id = b.id → a = b :=
  pairwise_key_unique Answer.id h.answersUniq

theorem Inv.question_inj {s : State} (h : Inv s) :
    ∀ a ∈ s.questions, ∀ b ∈ s.questions, a.id = b.id → a = b :=
  pairwise_key_unique Question.id h.questionsUniq

/-- When not inflight, every intent is committed or aborted. -/
theorem settled_of_not_inflight {s : State} (hq : s.inflight = false) :
    ∀ i ∈ s.intents, hasCommit s.journal i.txid ∨ hasAbort s.journal i.txid :=
  (inflight_eq_false_iff s).1 hq

/-- When not inflight, a non-aborted intent is committed. -/
theorem committed_of_live {s : State} (hq : s.inflight = false) {i : Intent}
    (hi : i ∈ s.intents) (hna : ¬ hasAbort s.journal i.txid) : hasCommit s.journal i.txid := by
  rcases settled_of_not_inflight hq i hi with hc | ha
  · exact hc
  · exact absurd ha hna

/-- A found answer with no commit and no inflight transaction carries no applied mark. -/
theorem appliedAs_none_of_not_committed {s : State} (h : Inv s) (hq : s.inflight = false)
    {ans : Answer} (hm : ans ∈ s.answers)
    (hnc : ∀ i ∈ s.intents, i.answer = ans.id → ¬ hasCommit s.journal i.txid) :
    ans.appliedAs = none := by
  cases hap : ans.appliedAs with
  | none => rfl
  | some d =>
    exfalso
    obtain ⟨i, hi, hia, -, hna⟩ := h.appliedRef ans hm d hap
    exact hnc i hi hia (committed_of_live hq hi hna)

/-! ## Transfer of `LiveIntent` between states -/

theorem LiveIntent.mono {s s' : State} {i : Intent} (h : LiveIntent s i)
    (ha : ∀ a ∈ s.answers, a.id = i.answer → ∃ a' ∈ s'.answers, a'.id = a.id ∧
      a'.closed = a.closed ∧ a'.stale = a.stale ∧ a'.question = a.question ∧
      a'.appliedAs = a.appliedAs)
    (hq : ∀ q ∈ s.questions, q ∈ s'.questions)
    (hd : (⟨i.decisionId, i.txid⟩ : Decision) ∈ s.decisions →
      (⟨i.decisionId, i.txid⟩ : Decision) ∈ s'.decisions)
    (hc : hasCommit s'.journal i.txid ↔ hasCommit s.journal i.txid) : LiveIntent s' i := by
  obtain ⟨a, hm, hid, hcl, hst, ⟨q, hqm, hqid, hqs⟩, hcom, hprov⟩ := h
  obtain ⟨a', hm', hid', hcl', hst', hqu', hap'⟩ := ha a hm hid
  refine ⟨a', hm', hid'.trans hid, hcl'.trans hcl, hst'.trans hst,
    ⟨q, hq q hqm, hqid.trans hqu'.symm, hqs⟩, ?_, ?_⟩
  · intro hc'
    obtain ⟨h1, h2⟩ := hcom (hc.1 hc')
    exact ⟨hap'.trans h1, hd h2⟩
  · intro hc'
    rw [hap']
    exact hprov (fun x => hc' (hc.2 x))

/-- `LiveIntent` when only fields other than
`id/closed/stale/question/appliedAs` of answers change (via `updateAnswer`). -/
theorem LiveIntent.updateAnswer_irrelevant {s : State} {i : Intent} (x : AnswerId)
    (f : Answer → Answer)
    (hf : ∀ a, (f a).id = a.id ∧ (f a).closed = a.closed ∧ (f a).stale = a.stale ∧
      (f a).question = a.question ∧ (f a).appliedAs = a.appliedAs)
    (h : LiveIntent s i) : LiveIntent (s.updateAnswer x f) i := by
  refine LiveIntent.mono h ?_ (fun q hq => hq) (fun hd => hd) Iff.rfl
  intro a hm _
  refine ⟨_, updateAnswer_mem hm, ?_⟩
  split
  · exact hf a
  · exact ⟨rfl, rfl, rfl, rfl, rfl⟩

/-! ## Initial state -/

theorem inv_init (records : List RecordId) : Inv (init records) := by
  constructor <;> simp [init, State.intents, intentsOf, hasCommit, hasAbort]

/-! ## Steps that do not touch the journal -/

/-- `Inv` for a state that differs from `s` only in `records`. -/
theorem inv_records {s : State} (h : Inv s) (rs : List (RecordId × Nat)) :
    Inv { s with records := rs } := by
  obtain ⟨h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13⟩ := h
  exact ⟨h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13⟩

theorem inv_stepEdit {s : State} (h : Inv s) (r : RecordId) : Inv (stepEdit s r).1 := by
  unfold stepEdit
  split
  · exact h
  · exact inv_records h _

theorem inv_stepCreateQuestion {s : State} (h : Inv s) (q : QuestionId)
    (subjects : List RecordId) : Inv (stepCreateQuestion s q subjects).1 := by
  unfold stepCreateQuestion
  split
  · exact h
  · rename_i hq
    split
    · exact h
    · obtain ⟨h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13⟩ := h
      refine ⟨h1, ?_, h3, h4, h5, h6, h7, h8, h9, ?_, h11, h12, h13⟩
      · rw [List.pairwise_cons]
        refine ⟨?_, h2⟩
        intro q' hq' hqq
        rw [State.findQuestion, List.find?_eq_none] at hq
        have := hq q' hq'
        simp at this
        exact this hqq.symm
      · intro i hi hna
        refine LiveIntent.mono (h10 i hi hna) ?_ ?_ (fun hd => hd) Iff.rfl
        · intro a hm _; exact ⟨a, hm, rfl, rfl, rfl, rfl, rfl⟩
        · intro q' hq'; exact List.mem_cons_of_mem _ hq'

theorem inv_stepSaveAnswer {s : State} (h : Inv s) (q : QuestionId) (a : AnswerId) :
    Inv (stepSaveAnswer s q a).1 := by
  unfold stepSaveAnswer
  split
  · exact h
  · split
    · exact h
    · rename_i qq _ hqq _ ha
      obtain ⟨h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13⟩ := h
      refine ⟨?_, h2, h3, h4, h5, h6, h7, h8, h9, ?_, h11, ?_, h13⟩
      · rw [List.pairwise_cons]
        refine ⟨?_, h1⟩
        intro a' ha' haa
        rw [State.findAnswer, List.find?_eq_none] at ha
        have := ha a' ha'
        simp at this
        exact this haa.symm
      · intro i hi hna
        refine LiveIntent.mono (h10 i hi hna) ?_ (fun q hq => hq) (fun hd => hd) Iff.rfl
        intro x hm _; exact ⟨x, List.mem_cons_of_mem _ hm, rfl, rfl, rfl, rfl, rfl⟩
      · intro x hx d hd
        simp only [List.mem_cons] at hx
        rcases hx with rfl | hx
        · simp at hd
        · exact h12 x hx d hd

/-- `appliedRef` survives an `updateAnswer` that keeps `id` and `appliedAs`. -/
theorem appliedRef_updateAnswer {s : State} (x : AnswerId) (f : Answer → Answer)
    (hf : ∀ a, (f a).id = a.id ∧ (f a).appliedAs = a.appliedAs)
    (h : ∀ a ∈ s.answers, ∀ d, a.appliedAs = some d →
      ∃ i ∈ s.intents, i.answer = a.id ∧ i.decisionId = d ∧ ¬ hasAbort s.journal i.txid) :
    ∀ a ∈ (s.updateAnswer x f).answers, ∀ d, a.appliedAs = some d →
      ∃ i ∈ (s.updateAnswer x f).intents, i.answer = a.id ∧ i.decisionId = d ∧
        ¬ hasAbort (s.updateAnswer x f).journal i.txid := by
  intro a ha d hd
  rw [mem_updateAnswer] at ha
  obtain ⟨y, hy, rfl⟩ := ha
  rw [updateAnswer_intents, updateAnswer_journal]
  by_cases hyx : y.id = x
  · rw [ite_eq_left hyx] at hd ⊢
    rw [(hf y).1]; rw [(hf y).2] at hd
    exact h y hy d hd
  · rw [ite_eq_right hyx] at hd ⊢
    exact h y hy d hd

theorem inv_stepAck {s : State} (h : Inv s) (a : AnswerId) : Inv (stepAck s a).1 := by
  unfold stepAck
  split
  · exact h
  · obtain ⟨h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13⟩ := h
    refine ⟨?_, h2, h3, h4, h5, h6, h7, h8, h9, ?_, h11, ?_, h13⟩
    · exact updateAnswer_pairwise s a _ (fun x => rfl) h1
    · intro i hi hna
      exact LiveIntent.updateAnswer_irrelevant a _ (fun x => ⟨rfl, rfl, rfl, rfl, rfl⟩)
        (h10 i hi hna)
    · exact appliedRef_updateAnswer a _ (fun x => ⟨rfl, rfl⟩) h12

/-- An `updateAnswer` of answer `a` when `a` is not committed and nothing is
inflight: no live intent refers to `a`, so `LiveIntent` is unaffected. -/
theorem inv_updateAnswer_uncommitted {s : State} (h : Inv s) (hq : s.inflight = false)
    (a : AnswerId) (hc : s.committedAs a = none) (f : Answer → Answer)
    (hf : ∀ x, (f x).id = x.id ∧ (f x).appliedAs = x.appliedAs) :
    Inv (s.updateAnswer a f) := by
  refine ⟨updateAnswer_pairwise s a f (fun x => (hf x).1) h.answersUniq, h.questionsUniq,
    h.txUniq, h.txLt, h.decUniq, h.decLt, h.commitRef, h.abortRef, h.commitAbort, ?_,
    h.ansOnce, appliedRef_updateAnswer a f hf h.appliedRef, h.decRef⟩
  intro i hi hna
  have hic := committed_of_live hq hi hna
  have hne : i.answer ≠ a := fun heq => (committedAs_eq_none_iff s a).1 hc i hi heq hic
  refine LiveIntent.mono (h.live i hi hna) ?_ (fun q hq => hq) (fun hd => hd) Iff.rfl
  intro x hm hxid
  refine ⟨_, updateAnswer_mem (f := f) hm, ?_⟩
  rw [ite_eq_right (by rw [hxid]; exact hne)]
  exact ⟨rfl, rfl, rfl, rfl, rfl⟩

theorem inv_stepClose {s : State} (h : Inv s) (hq : s.inflight = false) (a : AnswerId) :
    Inv (stepClose s a).1 := by
  unfold stepClose
  split
  · exact h
  · split
    · exact h
    · rename_i hc
      exact inv_updateAnswer_uncommitted h hq a hc _ (fun x => ⟨rfl, rfl⟩)

/-! ## Stage a -/

theorem stageA_fresh {s : State} (h : Inv s) (ans : Answer) (q : Question) :
    ¬ hasCommit (stageA s (newIntent s ans q)).journal (newIntent s ans q).txid ∧
    ¬ hasAbort (stageA s (newIntent s ans q)).journal (newIntent s ans q).txid := by
  simp only [stageA_journal, hasCommit_cons_intent, hasAbort_cons_intent, newIntent_txid]
  constructor
  · intro hc
    obtain ⟨j, hj, hjt⟩ := h.commitRef _ hc
    have := h.txLt j hj
    rw [hjt] at this; exact Nat.lt_irrefl _ this
  · intro ha
    obtain ⟨j, hj, hjt⟩ := h.abortRef _ ha
    have := h.txLt j hj
    rw [hjt] at this; exact Nat.lt_irrefl _ this

theorem inv_stageA {s : State} (h : Inv s) (hq : s.inflight = false)
    {ans : Answer} (hm : ans ∈ s.answers) (hcl : ans.closed = false) (hst : ans.stale = false)
    {q : Question} (hqm : q ∈ s.questions) (hqid : q.id = ans.question)
    (hnc : ∀ i ∈ s.intents, i.answer = ans.id → ¬ hasCommit s.journal i.txid) :
    Inv (stageA s (newIntent s ans q)) := by
  have hap : ans.appliedAs = none := appliedAs_none_of_not_committed h hq hm hnc
  have hother : ∀ j ∈ s.intents, ¬ hasAbort s.journal j.txid → j.answer ≠ ans.id := by
    intro j hj hna heq
    exact hnc j hj heq (committed_of_live hq hj hna)
  have hfresh := stageA_fresh h ans q
  constructor
  · simpa using h.answersUniq
  · simpa using h.questionsUniq
  · rw [stageA_intents, List.pairwise_cons]
    refine ⟨?_, h.txUniq⟩
    intro j hj
    have := h.txLt j hj
    rw [newIntent_txid]
    intro e; rw [← e] at this; exact Nat.lt_irrefl _ this
  · rw [stageA_intents]
    intro i hi
    simp only [List.mem_cons] at hi
    rcases hi with rfl | hi
    · rw [newIntent_txid, stageA_nextTx]; exact Nat.lt_succ_self _
    · rw [stageA_nextTx]; exact Nat.lt_succ_of_lt (h.txLt i hi)
  · rw [stageA_intents, List.pairwise_cons]
    refine ⟨?_, h.decUniq⟩
    intro j hj
    have := h.decLt j hj
    rw [newIntent_decisionId]
    intro e; rw [← e] at this; exact Nat.lt_irrefl _ this
  · rw [stageA_intents]
    intro i hi
    simp only [List.mem_cons] at hi
    rcases hi with rfl | hi
    · rw [newIntent_decisionId, stageA_nextDecision]; exact Nat.lt_succ_self _
    · rw [stageA_nextDecision]; exact Nat.lt_succ_of_lt (h.decLt i hi)
  · intro tx hc
    rw [stageA_journal, hasCommit_cons_intent] at hc
    obtain ⟨i, hi, rfl⟩ := h.commitRef tx hc
    exact ⟨i, by simp [hi], rfl⟩
  · intro tx ha
    rw [stageA_journal, hasAbort_cons_intent] at ha
    obtain ⟨i, hi, rfl⟩ := h.abortRef tx ha
    exact ⟨i, by simp [hi], rfl⟩
  · intro tx hc
    simp only [stageA_journal, hasCommit_cons_intent, hasAbort_cons_intent] at hc ⊢
    exact h.commitAbort tx hc
  · intro i hi hna
    rw [stageA_intents, List.mem_cons] at hi
    rcases hi with rfl | hi
    · refine ⟨ans, by simpa using hm, rfl, hcl, hst, ⟨q, by simpa using hqm, hqid, rfl⟩, ?_, ?_⟩
      · intro hc; exact absurd hc hfresh.1
      · intro _; exact Or.inl hap
    · rw [stageA_journal, hasAbort_cons_intent] at hna
      refine LiveIntent.mono (h.live i hi hna) ?_ ?_ ?_ ?_
      · intro a ha _; exact ⟨a, by simpa using ha, rfl, rfl, rfl, rfl, rfl⟩
      · intro q' hq'; simpa using hq'
      · intro hd; simpa using hd
      · simp
  · intro i hi j hj hni hnj heq
    rw [stageA_intents, List.mem_cons] at hi hj
    rw [stageA_journal, hasAbort_cons_intent] at hni hnj
    rcases hi with rfl | hi <;> rcases hj with rfl | hj
    · rfl
    · exact absurd heq.symm (hother j hj hnj)
    · exact absurd heq (hother i hi hni)
    · exact h.ansOnce i hi j hj hni hnj heq
  · intro a ha d hd
    obtain ⟨i, hi, h1, h2, h3⟩ := h.appliedRef a (by simpa using ha) d hd
    exact ⟨i, by simp [hi], h1, h2, by simpa using h3⟩
  · intro d hd
    obtain ⟨i, hi, h1, h2, h3⟩ := h.decRef d (by simpa using hd)
    exact ⟨i, by simp [hi], h1, h2, by simpa using h3⟩

/-! ## Stage b -/

theorem inv_stageB {s : State} (h : Inv s) {i : Intent} (hi : i ∈ s.intents)
    (hna : ¬ hasAbort s.journal i.txid) : Inv (stageB s i) := by
  constructor
  · simpa using h.answersUniq
  · simpa using h.questionsUniq
  · simpa using h.txUniq
  · simpa using h.txLt
  · simpa using h.decUniq
  · simpa using h.decLt
  · simpa using h.commitRef
  · simpa using h.abortRef
  · simpa using h.commitAbort
  · intro j hj hnaj
    rw [stageB_intents] at hj
    rw [stageB_journal] at hnaj
    refine LiveIntent.mono (h.live j hj hnaj) ?_ ?_ stageB_decisions_mono (by simp)
    · intro a ha _; exact ⟨a, by simpa using ha, rfl, rfl, rfl, rfl, rfl⟩
    · intro q hq; simpa using hq
  · simpa using h.ansOnce
  · simpa using h.appliedRef
  · intro d hd
    rw [mem_stageB_decisions] at hd
    rcases hd with hd | rfl
    · simpa using h.decRef d hd
    · exact ⟨i, by simpa using hi, rfl, rfl, by simpa using hna⟩

/-! ## Stage c -/

theorem inv_stageC {s : State} (h : Inv s) {i : Intent} (hi : i ∈ s.intents)
    (hna : ¬ hasAbort s.journal i.txid) (hnc : ¬ hasCommit s.journal i.txid) :
    Inv (stageC s i) := by
  have hf : ∀ x : Answer, ({ x with appliedAs := some i.decisionId } : Answer).id = x.id :=
    fun x => rfl
  constructor
  · exact updateAnswer_pairwise s i.answer _ hf h.answersUniq
  · simpa using h.questionsUniq
  · simpa using h.txUniq
  · simpa using h.txLt
  · simpa using h.decUniq
  · simpa using h.decLt
  · simpa using h.commitRef
  · simpa using h.abortRef
  · simpa using h.commitAbort
  · intro j hj hnaj
    rw [stageC_intents] at hj
    rw [stageC_journal] at hnaj
    by_cases hja : j.answer = i.answer
    · have hjt := h.ansOnce j hj i hi hnaj hna hja
      have hji : j = i := h.tx_inj j hj i hi hjt
      subst hji
      obtain ⟨a, ham, hid, hcl, hst, hqq, -, -⟩ := h.live j hj hnaj
      refine ⟨{ a with appliedAs := some j.decisionId }, ?_, hid, hcl, hst, hqq, ?_, ?_⟩
      · have := updateAnswer_mem (a := j.answer)
          (f := fun x => { x with appliedAs := some j.decisionId }) ham
        rw [ite_eq_left hid] at this
        exact this
      · intro hc; exact absurd hc hnc
      · intro _; exact Or.inr rfl
    · refine LiveIntent.mono (h.live j hj hnaj) ?_ (fun q hq => hq) (fun hd => hd) Iff.rfl
      intro a ha hid
      refine ⟨_, updateAnswer_mem (f := fun x => { x with appliedAs := some i.decisionId }) ha, ?_⟩
      rw [ite_eq_right (by rw [hid]; exact hja)]
      exact ⟨rfl, rfl, rfl, rfl, rfl⟩
  · simpa using h.ansOnce
  · intro a ha d hd
    rw [mem_stageC_answers] at ha
    obtain ⟨y, hy, rfl⟩ := ha
    rw [stageC_intents, stageC_journal]
    by_cases hyx : y.id = i.answer
    · rw [ite_eq_left hyx] at hd ⊢
      simp only [Option.some.injEq] at hd
      exact ⟨i, hi, hyx.symm, hd, hna⟩
    · rw [ite_eq_right hyx] at hd ⊢
      exact h.appliedRef y hy d hd
  · simpa using h.decRef

/-! ## Stage d -/

theorem inv_stageD {s : State} (h : Inv s) {i : Intent} (hi : i ∈ s.intents)
    (hna : ¬ hasAbort s.journal i.txid) (hnc : ¬ hasCommit s.journal i.txid)
    (hready : ∀ a ∈ s.answers, a.id = i.answer → a.appliedAs = some i.decisionId)
    (hdec : (⟨i.decisionId, i.txid⟩ : Decision) ∈ s.decisions) : Inv (stageD s i) := by
  constructor
  · simpa using h.answersUniq
  · simpa using h.questionsUniq
  · simpa using h.txUniq
  · simpa using h.txLt
  · simpa using h.decUniq
  · simpa using h.decLt
  · intro tx hc
    rw [stageD_journal, hasCommit_cons_commit] at hc
    rw [stageD_intents]
    rcases hc with rfl | hc
    · exact ⟨i, hi, rfl⟩
    · exact h.commitRef tx hc
  · simpa using h.abortRef
  · intro tx hc
    rw [stageD_journal, hasCommit_cons_commit] at hc
    rw [stageD_journal, hasAbort_cons_commit]
    rcases hc with rfl | hc
    · exact hna
    · exact h.commitAbort tx hc
  · intro j hj hnaj
    rw [stageD_intents] at hj
    rw [stageD_journal, hasAbort_cons_commit] at hnaj
    by_cases hjt : j.txid = i.txid
    · have hji : j = i := h.tx_inj j hj i hi hjt
      subst hji
      obtain ⟨a, ham, hid, hcl, hst, hqq, -, -⟩ := h.live j hj hnaj
      refine ⟨a, by simpa using ham, hid, hcl, hst, by simpa using hqq, ?_, ?_⟩
      · intro _
        exact ⟨hready a ham hid, by simpa using hdec⟩
      · intro hc
        exfalso
        exact hc (by rw [stageD_journal, hasCommit_cons_commit]; exact Or.inl rfl)
    · refine LiveIntent.mono (h.live j hj hnaj) ?_ (fun q hq => by simpa using hq)
        (fun hd => by simpa using hd) ?_
      · intro a ha _; exact ⟨a, by simpa using ha, rfl, rfl, rfl, rfl, rfl⟩
      · rw [stageD_journal, hasCommit_cons_commit]
        constructor
        · rintro (h' | h')
          · exact absurd h' hjt
          · exact h'
        · exact Or.inr
  · simpa using h.ansOnce
  · simpa using h.appliedRef
  · simpa using h.decRef

/-! ## Roll-forward (stages b, c, d) -/

theorem inv_rollForward {s : State} (h : Inv s) {i : Intent} (hi : i ∈ s.intents)
    (hna : ¬ hasAbort s.journal i.txid) (hnc : ¬ hasCommit s.journal i.txid) :
    Inv (rollForward s i) := by
  unfold rollForward
  have hB := inv_stageB h hi hna
  have hiB : i ∈ (stageB s i).intents := by simpa using hi
  have hnaB : ¬ hasAbort (stageB s i).journal i.txid := by simpa using hna
  have hncB : ¬ hasCommit (stageB s i).journal i.txid := by simpa using hnc
  have hC := inv_stageC hB hiB hnaB hncB
  have hiC : i ∈ (stageC (stageB s i) i).intents := by simpa using hi
  have hnaC : ¬ hasAbort (stageC (stageB s i) i).journal i.txid := by simpa using hna
  have hncC : ¬ hasCommit (stageC (stageB s i) i).journal i.txid := by simpa using hnc
  refine inv_stageD hC hiC hnaC hncC ?_ ?_
  · intro a ha hid
    rw [mem_stageC_answers] at ha
    obtain ⟨x, hx, rfl⟩ := ha
    by_cases hxi : x.id = i.answer
    · rw [ite_eq_left hxi]
    · rw [ite_eq_right hxi] at hid
      exact absurd hid hxi
  · rw [stageC_decisions]
    exact stageB_decision_mem s i

theorem inv_runStages {s : State} (h : Inv s) {i : Intent} (hi : i ∈ s.intents)
    (hna : ¬ hasAbort s.journal i.txid) (hnc : ¬ hasCommit s.journal i.txid)
    (crash : Option CrashAfter) : Inv (runStages s i crash).1 := by
  cases crash with
  | none => exact inv_rollForward h hi hna hnc
  | some c =>
    cases c with
    | intent => exact h
    | decision => exact inv_stageB h hi hna
    | question =>
      have hB := inv_stageB h hi hna
      exact inv_stageC hB (by simpa using hi) (by simpa using hna) (by simpa using hnc)

/-! ## Abort -/

theorem inv_abortTx {s : State} (h : Inv s) {i : Intent} (hi : i ∈ s.intents)
    (hna : ¬ hasAbort s.journal i.txid) (hnc : ¬ hasCommit s.journal i.txid) :
    Inv (abortTx s i) := by
  -- a non-aborted intent other than `i` has a different txid and a different answer
  have hother : ∀ j ∈ s.intents, ¬ hasAbort s.journal j.txid → j.txid ≠ i.txid →
      j.answer ≠ i.answer := by
    intro j hj hnaj hjt hja
    exact hjt (h.ansOnce j hj i hi hnaj hna hja)
  constructor
  · simp only [abortTx, State.updateAnswer]
    exact pairwise_key_of_map Answer.id _ (fun x => by split <;> rfl) h.answersUniq
  · simpa using h.questionsUniq
  · simpa using h.txUniq
  · simpa using h.txLt
  · simpa using h.decUniq
  · simpa using h.decLt
  · simpa using h.commitRef
  · intro tx ha
    rw [abortTx_journal, hasAbort_cons_abort] at ha
    rw [abortTx_intents]
    rcases ha with rfl | ha
    · exact ⟨i, hi, rfl⟩
    · exact h.abortRef tx ha
  · intro tx hc
    rw [abortTx_journal, hasCommit_cons_abort] at hc
    rw [abortTx_journal, hasAbort_cons_abort]
    rintro (rfl | ha)
    · exact hnc hc
    · exact h.commitAbort tx hc ha
  · intro j hj hnaj
    rw [abortTx_intents] at hj
    rw [abortTx_journal, hasAbort_cons_abort] at hnaj
    have hjt : j.txid ≠ i.txid := fun e => hnaj (Or.inl e)
    have hnaj' : ¬ hasAbort s.journal j.txid := fun e => hnaj (Or.inr e)
    refine LiveIntent.mono (h.live j hj hnaj') ?_ (fun q hq => by simpa using hq) ?_ ?_
    · intro a ha hid
      refine ⟨_, (mem_abortTx_answers).2 ⟨a, ha, rfl⟩, ?_⟩
      rw [ite_eq_right (by rw [hid]; exact hother j hj hnaj' hjt)]
      exact ⟨rfl, rfl, rfl, rfl, rfl⟩
    · intro hd
      rw [abortTx_decisions, List.mem_filter]
      exact ⟨hd, by simpa using hjt⟩
    · simp
  · intro j hj k hk hnaj hnak heq
    rw [abortTx_intents] at hj hk
    rw [abortTx_journal, hasAbort_cons_abort] at hnaj hnak
    exact h.ansOnce j hj k hk (fun e => hnaj (Or.inr e)) (fun e => hnak (Or.inr e)) heq
  · intro a ha d hd
    rw [mem_abortTx_answers] at ha
    obtain ⟨y, hy, rfl⟩ := ha
    rw [abortTx_intents, abortTx_journal]
    by_cases hyx : y.id = i.answer
    · rw [ite_eq_left hyx] at hd
      simp at hd
    · rw [ite_eq_right hyx] at hd ⊢
      obtain ⟨k, hk, hka, hkd, hnak⟩ := h.appliedRef y hy d hd
      refine ⟨k, hk, hka, hkd, ?_⟩
      rw [hasAbort_cons_abort]
      rintro (hkt | hkt)
      · have hki : k = i := h.tx_inj k hk i hi hkt
        subst hki
        exact hyx (hka.symm)
      · exact hnak hkt
  · intro d hd
    rw [abortTx_decisions, List.mem_filter] at hd
    obtain ⟨hd, hdt⟩ := hd
    obtain ⟨k, hk, hkt, hkd, hnak⟩ := h.decRef d hd
    refine ⟨k, by simpa using hk, hkt, hkd, ?_⟩
    rw [abortTx_journal, hasAbort_cons_abort]
    rintro (hki | hki)
    · simp at hdt
      exact hdt (hkt.symm.trans hki)
    · exact hnak hki

/-! ## recover / apply / step -/

theorem inv_stepRecover {s : State} (h : Inv s) : Inv (stepRecover s).1 := by
  unfold stepRecover
  split
  · exact h
  · rename_i i hi
    obtain ⟨hmem, hnc, hna⟩ := inflightIntent_some s i hi
    split
    · exact inv_rollForward h hmem hna hnc
    · exact inv_abortTx h hmem hna hnc

theorem inv_stepApply {s : State} (h : Inv s) (hq : s.inflight = false) (a : AnswerId)
    (crash : Option CrashAfter) : Inv (stepApply s a crash).1 := by
  unfold stepApply
  split
  · exact h
  · rename_i ans hans
    split
    · exact h
    · rename_i hc
      split
      · exact h
      · rename_i hcl
        split
        · exact h
        · rename_i q hqf
          split
          · exact inv_updateAnswer_uncommitted h hq a hc _ (fun x => ⟨rfl, rfl⟩)
          · rename_i hok
            have hm := List.mem_of_find?_eq_some hans
            have hid : ans.id = a := by simpa using List.find?_some hans
            have hqm := List.mem_of_find?_eq_some hqf
            have hqid : q.id = ans.question := by simpa using List.find?_some hqf
            have hcl' : ans.closed = false := by simpa using hcl
            have hst : ans.stale = false := by
              simp only [Bool.or_eq_true, Bool.not_eq_true', not_or] at hok
              simpa using hok.1
            have hnc : ∀ i ∈ s.intents, i.answer = ans.id → ¬ hasCommit s.journal i.txid := by
              rw [hid]; exact (committedAs_eq_none_iff s a).1 hc
            have hA := inv_stageA h hq hm hcl' hst hqm hqid hnc
            have hfresh := stageA_fresh h ans q
            exact inv_runStages hA (by simp) hfresh.2 hfresh.1 crash

theorem inv_step {s : State} (h : Inv s) (act : Action) : Inv (step s act).1 := by
  cases act with
  | createQuestion q subs =>
    simp only [step, guardInflight]; split
    · exact h
    · exact inv_stepCreateQuestion h q subs
  | edit r => exact inv_stepEdit h r
  | saveAnswer q a =>
    simp only [step, guardInflight]; split
    · exact h
    · exact inv_stepSaveAnswer h q a
  | ack a =>
    simp only [step, guardInflight]; split
    · exact h
    · exact inv_stepAck h a
  | close a =>
    simp only [step, guardInflight]; split
    · exact h
    · rename_i hq; exact inv_stepClose h (by simpa using hq) a
  | apply a crash =>
    simp only [step, guardInflight]; split
    · exact h
    · rename_i hq; exact inv_stepApply h (by simpa using hq) a crash
  | recover => exact inv_stepRecover h

/-- Every reachable state satisfies the invariant. -/
theorem inv_reachable {s : State} (h : Reachable s) : Inv s := by
  induction h with
  | init records => exact inv_init records
  | step act _ ih => exact inv_step ih act

end PrepareDev.Spec
