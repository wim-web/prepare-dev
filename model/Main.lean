import PrepareDev
import PrepareDev.Gen.Trace

open PrepareDev.Gen

def usage : String :=
  "usage: prepare-model gen --depth N [--max-traces M] [--seed S]\n" ++
  "  Enumerates action sequences of length up to N (DFS, deterministic) and\n" ++
  "  prints one JSON line per step in the format of SPEC-0001 Section 4."

partial def parseOpts (cfg : Config) : List String → Except String Config
  | [] => .ok cfg
  | "--depth" :: v :: rest =>
    match v.toNat? with
    | some n => parseOpts { cfg with depth := n } rest
    | none => .error s!"--depth: not a number: {v}"
  | "--max-traces" :: v :: rest =>
    match v.toNat? with
    | some n => parseOpts { cfg with maxTraces := n } rest
    | none => .error s!"--max-traces: not a number: {v}"
  | "--seed" :: v :: rest =>
    match v.toNat? with
    | some n => parseOpts { cfg with seed := n } rest
    | none => .error s!"--seed: not a number: {v}"
  | arg :: _ => .error s!"unknown argument: {arg}"

def main (args : List String) : IO UInt32 := do
  match args with
  | "gen" :: rest =>
    match parseOpts { depth := 0 } rest with
    | .error e =>
      IO.eprintln e
      IO.eprintln usage
      return 1
    | .ok cfg =>
      let out ← IO.getStdout
      for line in generate cfg do
        out.putStrLn line
      out.flush
      return 0
  | _ =>
    IO.eprintln usage
    return 1
