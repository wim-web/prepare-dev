import PrepareDev.Spec.Recovery
import PrepareDev.Gen.Json

namespace PrepareDev.Gen

private def recoveryObservation (s : Recovery.Disk) : Json := .obj
  [("answer", .num s.answer), ("aborted", .bool s.aborted),
   ("decision", .bool s.decision), ("applied", .bool s.applied),
   ("stale", .bool s.stale), ("ready", .bool (Recovery.ready s))]

private def recoveryActions : Nat → List (List Recovery.Action)
  | 0 => [[]]
  | n + 1 => (recoveryActions n).flatMap fun xs => [.resume :: xs, .crash :: xs]

private def recoverySteps (s : Recovery.Disk) : List Recovery.Action → List Json
  | [] => []
  | a :: rest =>
    let next := Recovery.step s a
    .obj [("action", .str (match a with | .resume => "resume" | .crash => "crash")),
          ("observe", recoveryObservation next)] :: recoverySteps next rest

/-- Each apply persistence cut, every four-action interruption pattern, then
three writes to finish and a repeated resume to check idempotence. -/
def recoveryTraces : List String :=
  ([ ("intent", false, false), ("decision", true, false), ("question", true, true) ]).flatMap fun (stage, d, p) =>
    (recoveryActions 4).map fun actions =>
      let s : Recovery.Disk := ⟨1, false, d, p, false⟩
      (Json.obj [("stage", .str stage), ("initial", recoveryObservation s),
        ("steps", .arr (recoverySteps s (actions ++ [.resume, .resume, .resume, .resume])))]).render

end PrepareDev.Gen
