import PrepareDev.Spec.Step

namespace PrepareDev.Spec

/-- States reachable from an initial state (any set of records at version
`0`) by the transition function `step`. -/
inductive Reachable : State → Prop where
  | init (records : List RecordId) : Reachable (init records)
  | step {s : State} (act : Action) : Reachable s → Reachable (step s act).1

end PrepareDev.Spec
