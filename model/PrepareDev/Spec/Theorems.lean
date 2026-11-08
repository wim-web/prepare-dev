import PrepareDev.Spec.Invariant

/-!
# SPEC-0001 Section 3: claims C1–C3

All theorems are derived from the inductive invariant `Inv` of
`PrepareDev.Spec.Invariant`, which holds of every reachable state.
-/

namespace PrepareDev.Spec

theorem mem_committedIntents {s : State} {i : Intent} :
    i ∈ s.committedIntents ↔ i ∈ s.intents ∧ hasCommit s.journal i.txid := by
  simp [State.committedIntents]

theorem mem_committedIntentsOf {s : State} {a : AnswerId} {i : Intent} :
    i ∈ s.committedIntentsOf a ↔ i ∈ s.intents ∧ i.answer = a ∧ hasCommit s.journal i.txid := by
  simp [State.committedIntentsOf]

theorem mem_publicDecisions {s : State} {d : Decision} :
    d ∈ s.publicDecisions ↔ d ∈ s.decisions ∧ hasCommit s.journal d.txid := by
  simp [State.publicDecisions]

theorem length_le_one_of_pairwise {α : Type} {R : α → α → Prop} {l : List α}
    (hp : l.Pairwise R) (hall : ∀ x ∈ l, ∀ y ∈ l, ¬ R x y) : l.length ≤ 1 := by
  cases l with
  | nil => simp
  | cons x t =>
    cases t with
    | nil => simp
    | cons y rest =>
      exfalso
      rw [List.pairwise_cons] at hp
      exact hall x List.mem_cons_self y (List.mem_cons_of_mem _ List.mem_cons_self)
        (hp.1 y List.mem_cons_self)

/-- **C1.** In every reachable state, each answer has at most one committed
intent, and `public_decisions` corresponds one-to-one to the committed
intents: every public decision has exactly one committed intent (same
`txid`, same decision id), every committed intent's decision is public,
distinct committed intents have distinct decision ids, and a decision file is
public iff its `txid` is committed. -/
theorem applied_at_most_once (s : State) (h : Reachable s) :
    (∀ a : AnswerId, (s.committedIntentsOf a).length ≤ 1) ∧
    (∀ d ∈ s.publicDecisions, ∃ i ∈ s.committedIntents, i.txid = d.txid ∧ i.decisionId = d.id ∧
        ∀ j ∈ s.committedIntents, j.txid = d.txid → j = i) ∧
    (∀ i ∈ s.committedIntents, (⟨i.decisionId, i.txid⟩ : Decision) ∈ s.publicDecisions) ∧
    (∀ i ∈ s.committedIntents, ∀ j ∈ s.committedIntents, i.decisionId = j.decisionId → i = j) ∧
    (∀ d ∈ s.decisions, d ∈ s.publicDecisions ↔ hasCommit s.journal d.txid) := by
  have hI := inv_reachable h
  refine ⟨?_, ?_, ?_, ?_, ?_⟩
  · intro a
    have hp : (s.committedIntentsOf a).Pairwise fun x y => x.txid ≠ y.txid :=
      hI.txUniq.filter _
    refine length_le_one_of_pairwise hp ?_
    intro x hx y hy hne
    rw [mem_committedIntentsOf] at hx hy
    exact hne (hI.ansOnce x hx.1 y hy.1 (hI.commitAbort _ hx.2.2) (hI.commitAbort _ hy.2.2)
      (hx.2.1.trans hy.2.1.symm))
  · intro d hd
    rw [mem_publicDecisions] at hd
    obtain ⟨i, hi, hit, hid, -⟩ := hI.decRef d hd.1
    refine ⟨i, mem_committedIntents.2 ⟨hi, by rw [hit]; exact hd.2⟩, hit, hid, ?_⟩
    intro j hj hjt
    rw [mem_committedIntents] at hj
    exact hI.tx_inj j hj.1 i hi (hjt.trans hit.symm)
  · intro i hi
    rw [mem_committedIntents] at hi
    obtain ⟨a, -, -, -, -, -, hcom, -⟩ := hI.live i hi.1 (hI.commitAbort _ hi.2)
    rw [mem_publicDecisions]
    exact ⟨(hcom hi.2).2, hi.2⟩
  · intro i hi j hj heq
    rw [mem_committedIntents] at hi hj
    exact hI.dec_inj i hi.1 j hj.1 heq
  · intro d hd
    rw [mem_publicDecisions]
    exact ⟨fun h => h.2, fun h => ⟨hd, h⟩⟩

/-- **C2 (state part).** For every committed intent, its answer exists, is
not `stale`, and `intent.subjects` equals the `subjects` snapshot of the
intent's question (which exists and is unique). -/
theorem committed_matches_snapshot (s : State) (h : Reachable s) :
    ∀ i ∈ s.committedIntents, ∃ a ∈ s.answers, a.id = i.answer ∧ a.stale = false ∧
      (∃ q ∈ s.questions, q.id = a.question ∧ q.subjects = i.subjects) ∧
      (∀ q ∈ s.questions, q.id = a.question → q.subjects = i.subjects) := by
  have hI := inv_reachable h
  intro i hi
  rw [mem_committedIntents] at hi
  obtain ⟨a, ham, hid, -, hst, ⟨q, hqm, hqid, hqs⟩, -, -⟩ :=
    hI.live i hi.1 (hI.commitAbort _ hi.2)
  refine ⟨a, ham, hid, hst, ⟨q, hqm, hqid, hqs⟩, ?_⟩
  intro q' hq' hq'id
  rw [hI.question_inj q' hq' q hqm (hq'id.trans hqid.symm)]
  exact hqs

/-- **C2 (step part).** Whenever a `step` appends a `commit {txid}`, the
current record versions equal that intent's `subjects` (and the step does
not change the records). -/
theorem step_commit_guard {s s' : State} {act : Action} {r : Result}
    (hstep : step s act = (s', r)) (tx : TxId)
    (hnew : hasCommit s'.journal tx) (hold : ¬ hasCommit s.journal tx) :
    s'.records = s.records ∧
      ∃ i ∈ s'.intents, i.txid = tx ∧ s.subjectsMatch i.subjects = true := by
  cases act with
  | createQuestion q subs =>
    simp only [step, guardInflight] at hstep
    split at hstep
    · cases hstep; exact absurd hnew hold
    · unfold stepCreateQuestion at hstep
      split at hstep
      · cases hstep; exact absurd hnew hold
      · split at hstep <;> cases hstep <;> exact absurd hnew hold
  | edit r' =>
    simp only [step] at hstep
    unfold stepEdit at hstep
    split at hstep <;> cases hstep <;> exact absurd hnew hold
  | saveAnswer q a =>
    simp only [step, guardInflight] at hstep
    split at hstep
    · cases hstep; exact absurd hnew hold
    · unfold stepSaveAnswer at hstep
      split at hstep
      · cases hstep; exact absurd hnew hold
      · split at hstep <;> cases hstep <;> exact absurd hnew hold
  | ack a =>
    simp only [step, guardInflight] at hstep
    split at hstep
    · cases hstep; exact absurd hnew hold
    · unfold stepAck at hstep
      split at hstep <;> cases hstep <;> exact absurd hnew hold
  | close a =>
    simp only [step, guardInflight] at hstep
    split at hstep
    · cases hstep; exact absurd hnew hold
    · unfold stepClose at hstep
      split at hstep
      · cases hstep; exact absurd hnew hold
      · split at hstep <;> cases hstep <;> exact absurd hnew hold
  | apply a crash =>
    simp only [step, guardInflight] at hstep
    split at hstep
    · cases hstep; exact absurd hnew hold
    · unfold stepApply at hstep
      split at hstep
      · cases hstep; exact absurd hnew hold
      · next ans _ =>
        split at hstep
        · cases hstep; exact absurd hnew hold
        · split at hstep
          · cases hstep; exact absurd hnew hold
          · split at hstep
            · cases hstep; exact absurd hnew hold
            · next q _ =>
              split at hstep
              · cases hstep; exact absurd hnew hold
              · next hok =>
                have hmatch : s.subjectsMatch q.subjects = true := by
                  simp only [Bool.or_eq_true, Bool.not_eq_true', not_or] at hok
                  simpa using hok.2
                cases crash with
                | none =>
                  simp only [runStages] at hstep
                  cases hstep
                  refine ⟨by simp, newIntent s ans q, by simp, ?_⟩
                  rw [rollForward_journal, hasCommit_cons_commit, stageA_journal,
                    hasCommit_cons_intent] at hnew
                  rcases hnew with rfl | hnew
                  · exact ⟨rfl, hmatch⟩
                  · exact absurd hnew hold
                | some c =>
                  cases c <;> simp only [runStages] at hstep <;> cases hstep <;>
                    simp only [stageC_journal, stageB_journal, stageA_journal,
                      hasCommit_cons_intent] at hnew <;> exact absurd hnew hold
  | recover =>
    simp only [step] at hstep
    unfold stepRecover at hstep
    split at hstep
    · cases hstep; exact absurd hnew hold
    · rename_i i hi
      split at hstep
      · rename_i hmatch
        cases hstep
        refine ⟨by simp, i, by simpa using (inflightIntent_some s i hi).1, ?_⟩
        rw [rollForward_journal, hasCommit_cons_commit] at hnew
        rcases hnew with rfl | hnew
        · exact ⟨rfl, hmatch⟩
        · exact absurd hnew hold
      · cases hstep
        rw [abortTx_journal, hasCommit_cons_abort] at hnew
        exact absurd hnew hold

/-- **C3 (i).** `step` never removes an answer: every saved answer id is
still present after any step. -/
theorem answers_monotone (s : State) (act : Action) :
    ∀ a ∈ s.answers, ∃ a' ∈ (step s act).1.answers, a'.id = a.id := by
  have hupd : ∀ (t : State) (x : AnswerId) (f : Answer → Answer), (∀ y, (f y).id = y.id) →
      ∀ a ∈ t.answers, ∃ a' ∈ (t.updateAnswer x f).answers, a'.id = a.id := by
    intro t x f hf a ha
    refine ⟨_, updateAnswer_mem (a := x) (f := f) ha, ?_⟩
    split
    · exact hf a
    · rfl
  have hC : ∀ (t : State) (i : Intent), ∀ a ∈ t.answers,
      ∃ a' ∈ (stageC t i).answers, a'.id = a.id := by
    intro t i a ha
    unfold stageC
    refine hupd _ _ _ (fun y => ?_) a ha
    rfl
  have hstages : ∀ (t : State) (i : Intent) (crash : Option CrashAfter),
      ∀ a ∈ t.answers, ∃ a' ∈ (runStages t i crash).1.answers, a'.id = a.id := by
    intro t i crash a ha
    cases crash with
    | none =>
      simp only [runStages, rollForward_answers]
      exact hC _ _ a (by simpa using ha)
    | some c =>
      cases c with
      | intent => exact ⟨a, ha, rfl⟩
      | decision => simp only [runStages]; exact ⟨a, by simpa using ha, rfl⟩
      | question => simp only [runStages]; exact hC _ _ a (by simpa using ha)
  intro a ha
  cases act with
  | createQuestion q subs =>
    simp only [step, guardInflight]
    split
    · exact ⟨a, ha, rfl⟩
    · unfold stepCreateQuestion
      split
      · exact ⟨a, ha, rfl⟩
      · split <;> exact ⟨a, ha, rfl⟩
  | edit r =>
    simp only [step]; unfold stepEdit
    split <;> exact ⟨a, ha, rfl⟩
  | saveAnswer q a' =>
    simp only [step, guardInflight]
    split
    · exact ⟨a, ha, rfl⟩
    · unfold stepSaveAnswer
      split
      · exact ⟨a, ha, rfl⟩
      · split
        · exact ⟨a, ha, rfl⟩
        · exact ⟨a, List.mem_cons_of_mem _ ha, rfl⟩
  | ack a' =>
    simp only [step, guardInflight]
    split
    · exact ⟨a, ha, rfl⟩
    · unfold stepAck
      split
      · exact ⟨a, ha, rfl⟩
      · refine hupd _ _ _ (fun y => ?_) a ha
        rfl
  | close a' =>
    simp only [step, guardInflight]
    split
    · exact ⟨a, ha, rfl⟩
    · unfold stepClose
      split
      · exact ⟨a, ha, rfl⟩
      · split
        · exact ⟨a, ha, rfl⟩
        · refine hupd _ _ _ (fun y => ?_) a ha
          rfl
  | apply a' crash =>
    simp only [step, guardInflight]
    split
    · exact ⟨a, ha, rfl⟩
    · unfold stepApply
      split
      · exact ⟨a, ha, rfl⟩
      · split
        · exact ⟨a, ha, rfl⟩
        · split
          · exact ⟨a, ha, rfl⟩
          · split
            · exact ⟨a, ha, rfl⟩
            · split
              · refine hupd _ _ _ (fun y => ?_) a ha
                rfl
              · exact hstages _ _ crash a (by simpa using ha)
  | recover =>
    simp only [step]; unfold stepRecover
    split
    · exact ⟨a, ha, rfl⟩
    · split
      · simp only [rollForward_answers]
        exact hC _ _ a (by simpa using ha)
      · unfold abortTx
        refine hupd _ _ _ (fun y => ?_) a ha
        rfl

/-- **C3 (ii)–(iv).** In every reachable state that is not `inflight`:
(ii) an answer carries `applied_as` iff it is `committed`;
(iii) a decision file exists for a `txid` iff that `txid` is committed;
(iv) `pending` computed from the files equals `pending` computed from the
journal. -/
theorem quiescent_consistent (s : State) (h : Reachable s) (hq : s.inflight = false) :
    (∀ a ∈ s.answers, a.appliedAs ≠ none ↔ s.committed a.id = true) ∧
    (∀ tx : TxId, (∃ d ∈ s.decisions, d.txid = tx) ↔ hasCommit s.journal tx) ∧
    s.pendingFromFiles = s.pendingFromJournal := by
  have hI := inv_reachable h
  have hap : ∀ a ∈ s.answers, a.appliedAs ≠ none ↔ s.committed a.id = true := by
    intro a ha
    rw [committed_eq_true_iff]
    constructor
    · intro hne
      cases hd : a.appliedAs with
      | none => exact absurd hd hne
      | some d =>
        obtain ⟨i, hi, hia, -, hna⟩ := hI.appliedRef a ha d hd
        exact ⟨i, hi, hia, committed_of_live hq hi hna⟩
    · rintro ⟨i, hi, hia, hc⟩
      obtain ⟨a', ha', hid, -, -, -, hcom, -⟩ := hI.live i hi (hI.commitAbort _ hc)
      have : a' = a := hI.answer_inj a' ha' a ha (hid.trans hia)
      subst this
      rw [(hcom hc).1]
      exact Option.some_ne_none _
  refine ⟨hap, ?_, ?_⟩
  · intro tx
    constructor
    · rintro ⟨d, hd, rfl⟩
      obtain ⟨i, hi, hit, -, hna⟩ := hI.decRef d hd
      rw [← hit]
      exact committed_of_live hq hi hna
    · intro hc
      obtain ⟨i, hi, rfl⟩ := hI.commitRef tx hc
      obtain ⟨a, -, -, -, -, -, hcom, -⟩ := hI.live i hi (hI.commitAbort _ hc)
      exact ⟨_, (hcom hc).2, rfl⟩
  · unfold State.pendingFromFiles State.pendingFromJournal
    congr 1
    apply List.filter_congr
    intro a ha
    have := hap a ha
    cases hd : a.appliedAs <;> cases hc : s.committed a.id <;> simp_all

/-- C2 includes both the snapshot property and the guard at each new commit. -/
theorem committed_safety (s : State) (h : Reachable s) :
    (∀ i ∈ s.committedIntents, ∃ a ∈ s.answers, a.id = i.answer ∧ a.stale = false ∧
      (∃ q ∈ s.questions, q.id = a.question ∧ q.subjects = i.subjects) ∧
      (∀ q ∈ s.questions, q.id = a.question → q.subjects = i.subjects)) ∧
    (∀ (s' : State) (act : Action) (r : Result), step s act = (s', r) →
      ∀ tx, hasCommit s'.journal tx → ¬ hasCommit s.journal tx →
        s'.records = s.records ∧
        ∃ i ∈ s'.intents, i.txid = tx ∧ s.subjectsMatch i.subjects = true) := by
  exact ⟨committed_matches_snapshot s h,
    fun _ _ _ hstep tx hnew hold => step_commit_guard hstep tx hnew hold⟩

/-- C3 includes answer preservation as well as the completed-state invariant. -/
theorem quiescent_with_answers (s : State) (h : Reachable s) :
    (∀ act, ∀ a ∈ s.answers, ∃ a' ∈ (step s act).1.answers, a'.id = a.id) ∧
    (s.inflight = false →
      (∀ a ∈ s.answers, a.appliedAs ≠ none ↔ s.committed a.id = true) ∧
      (∀ tx : TxId, (∃ d ∈ s.decisions, d.txid = tx) ↔ hasCommit s.journal tx) ∧
      s.pendingFromFiles = s.pendingFromJournal) := by
  exact ⟨answers_monotone s, quiescent_consistent s h⟩

end PrepareDev.Spec
