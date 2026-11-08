export const ID_PREFIXES = {
  discovery: "DIS",
  requirement: "REQ",
  decision: "DEC",
  design: "DES",
  spec: "SPEC",
  review: "REV",
  record: "REC",
  proposal: "PRO",
  question: "Q",
  run: "RUN",
} as const;

export type IdPrefix = (typeof ID_PREFIXES)[keyof typeof ID_PREFIXES];

const ID_RE = /^([A-Z]+)-(\d{4,})$/;

export function parseId(id: string): { prefix: string; n: number } | null {
  const m = ID_RE.exec(id);
  if (!m) return null;
  return { prefix: m[1]!, n: Number(m[2]) };
}

export function formatId(prefix: string, n: number): string {
  return `${prefix}-${String(n).padStart(4, "0")}`;
}

/** Next id for a prefix given the ids already in use (any prefix accepted; others ignored). */
export function nextId(prefix: string, used: Iterable<string>): string {
  let max = 0;
  for (const id of used) {
    const p = parseId(id);
    if (p && p.prefix === prefix && p.n > max) max = p.n;
  }
  return formatId(prefix, max + 1);
}

export function answerId(questionId: string, n: number): string {
  return `ANS-${questionId.replace(/^Q-/, "")}-${n}`;
}

export function kindOfId(id: string): string | null {
  const p = parseId(id);
  if (!p) return null;
  for (const [kind, prefix] of Object.entries(ID_PREFIXES)) {
    if (prefix === p.prefix) return kind;
  }
  return null;
}
