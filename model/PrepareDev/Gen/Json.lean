/-!
A minimal JSON value type and encoder (no external dependencies).
Objects keep insertion order; strings are escaped per RFC 8259.
-/

namespace PrepareDev.Gen

inductive Json where
  | null
  | bool (b : Bool)
  | num (n : Nat)
  | str (s : String)
  | arr (xs : List Json)
  | obj (kvs : List (String × Json))

namespace Json

private def hexDigit (n : Nat) : Char :=
  if n < 10 then Char.ofNat (48 + n) else Char.ofNat (87 + n)

private def escapeChar (c : Char) : String :=
  match c with
  | '"' => "\\\""
  | '\\' => "\\\\"
  | '\n' => "\\n"
  | '\r' => "\\r"
  | '\t' => "\\t"
  | '\x08' => "\\b"
  | '\x0c' => "\\f"
  | c =>
    if c.toNat < 0x20 then
      let n := c.toNat
      String.ofList ['\\', 'u', '0', '0', hexDigit (n / 16), hexDigit (n % 16)]
    else String.singleton c

def escape (s : String) : String :=
  "\"" ++ String.join (s.toList.map escapeChar) ++ "\""

partial def render : Json → String
  | .null => "null"
  | .bool true => "true"
  | .bool false => "false"
  | .num n => toString n
  | .str s => escape s
  | .arr xs => "[" ++ ", ".intercalate (xs.map render) ++ "]"
  | .obj kvs => "{" ++ ", ".intercalate (kvs.map fun (k, v) => escape k ++ ": " ++ render v) ++ "}"

def strs (xs : List String) : Json := .arr (xs.map .str)

end Json

end PrepareDev.Gen
