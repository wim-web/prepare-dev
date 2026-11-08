/-!
Persisted abort-cleanup steps for one uncommitted transaction whose subjects
changed. Each step is one durable write. A crash leaves the disk unchanged;
the next process resumes by inspecting that disk, without volatile state.
The answer id represents the saved answer, which cleanup must retain.
-/
namespace PrepareDev.Recovery

structure Disk where
  answer : Nat
  aborted : Bool
  decision : Bool
  applied : Bool
  stale : Bool
  deriving DecidableEq, Repr

def ready (s : Disk) : Bool :=
  s.aborted && !s.decision && !s.applied && s.stale

def resume (s : Disk) : Disk :=
  if !s.aborted then { s with aborted := true }
  else if s.decision then { s with decision := false }
  else { s with applied := false, stale := true }

inductive Action where
  | resume
  | crash
  deriving DecidableEq, Repr

def step (s : Disk) : Action → Disk
  | .resume => resume s
  | .crash => s

def run (s : Disk) : List Action → Disk
  | [] => s
  | a :: rest => run (step s a) rest

def finish (s : Disk) : Disk := resume (resume (resume s))

theorem resume_preserves_answer (s : Disk) : (resume s).answer = s.answer := by
  unfold resume
  split <;> first | rfl | split <;> rfl

theorem run_preserves_answer (s : Disk) (actions : List Action) :
    (run s actions).answer = s.answer := by
  induction actions generalizing s with
  | nil => rfl
  | cons a rest ih =>
    rw [run, ih]
    cases a
    · exact resume_preserves_answer s
    · rfl

theorem finish_ready (s : Disk) : ready (finish s) = true := by
  rcases s with ⟨a, b, d, p, t⟩
  cases b <;> cases d <;> cases p <;> cases t <;> rfl

theorem ready_consistent (s : Disk) (h : ready s = true) :
    s.aborted = true ∧ s.decision = false ∧ s.applied = false ∧ s.stale = true := by
  simpa [ready, Bool.and_eq_true, and_assoc] using h

theorem ready_stable (s : Disk) (h : ready s = true) : resume s = s := by
  rcases ready_consistent s h with ⟨hb, hd, hp, ht⟩
  rcases s with ⟨a, b, d, p, t⟩
  simp_all [resume]

/-- C5: any finite pattern of crashes and recovery steps retains the answer;
three uninterrupted writes finish cleanup; ready states have no provisional
files and resuming them is a no-op. -/
theorem recovery_safe (s : Disk) (actions : List Action) :
    (run s actions).answer = s.answer ∧
    ready (finish (run s actions)) = true ∧
    (ready (run s actions) = true →
      (run s actions).aborted = true ∧ (run s actions).decision = false ∧
      (run s actions).applied = false ∧ (run s actions).stale = true) ∧
    (ready (run s actions) = true → resume (run s actions) = run s actions) := by
  exact ⟨run_preserves_answer s actions, finish_ready _, ready_consistent _, ready_stable _⟩

end PrepareDev.Recovery
