import fs from "node:fs";
import path from "node:path";
import { type SubjectRef, nowIso } from "@prepare-dev/core";
import { stringify as stringifyYaml } from "yaml";
import { RuntimeError } from "./errors.js";
import type { RecordView, Store } from "./store.js";
import { writeFileAtomic } from "./store.js";

export interface BundleOptions {
  /** Also include public decisions whose reviewed subjects or rationale_for mention one of the ids. */
  expand?: boolean;
  /** Write the markdown under .prepare-dev/reviews/ and return its path. */
  out?: boolean;
  title?: string;
}

export interface BundleResult {
  inputs: SubjectRef[];
  markdown: string;
  path: string | null;
}

/**
 * A review bundle is what a fresh-context reviewer (Goldfish) gets: the exact versions of
 * the records under review plus their related decisions, nothing from the conversation.
 */
export function buildBundle(store: Store, ids: string[], isPublic: (d: RecordView) => boolean, opts: BundleOptions = {}): BundleResult {
  if (!ids.length) throw new RuntimeError("invalid", "bundle needs at least one record id");
  const records: RecordView[] = [];
  const seen = new Set<string>();
  const push = (r: RecordView): void => {
    if (seen.has(r.id)) return;
    seen.add(r.id);
    records.push(r);
  };
  for (const id of ids) {
    const r = store.readRecord(id);
    if (r.kind === "decision" && !isPublic(r)) throw new RuntimeError("not_found", `decision ${id} is provisional`);
    push(r);
  }
  if (opts.expand !== false) {
    for (const d of store.listRecords("decision")) {
      if (!isPublic(d)) continue;
      const fm = d.frontmatter as { reviewed?: SubjectRef[]; links?: Record<string, string[]> };
      const mentioned = (fm.reviewed ?? []).some((s) => ids.includes(s.id)) || (fm.links?.rationale_for ?? []).some((x) => ids.includes(x));
      if (mentioned) push(d);
    }
  }
  const inputs: SubjectRef[] = records.map((r) => ({ id: r.id, rev: r.rev, sha256: r.sha256 }));
  const lines: string[] = [];
  lines.push(`# ${opts.title ?? "Review bundle"}`);
  lines.push("");
  lines.push(`generated: ${nowIso()}`);
  lines.push("");
  lines.push("この資料は prepare-dev の記録を版を固定してまとめたもの。会話の履歴は含まない。レビューはこの資料だけを根拠にする。");
  lines.push("");
  lines.push("## 含まれる記録");
  lines.push("");
  for (const r of records) lines.push(`- ${r.id} rev ${r.rev} sha256 ${r.sha256} — ${r.frontmatter.title} (${r.kind}, ${r.frontmatter.status})`);
  lines.push("");
  for (const r of records) {
    lines.push("---");
    lines.push("");
    lines.push(`## ${r.id} — ${r.frontmatter.title}`);
    lines.push("");
    lines.push(`kind: ${r.kind} · rev ${r.rev} · status ${r.frontmatter.status} · sha256 ${r.sha256}`);
    lines.push("");
    const { body: _b, ...fm } = { ...r.frontmatter } as Record<string, unknown>;
    void _b;
    for (const k of ["id", "kind", "title", "rev", "created", "updated", "status"]) delete fm[k];
    if (Object.keys(fm).length) {
      lines.push("```yaml");
      lines.push(stringifyYaml(fm, { lineWidth: 0 }).trimEnd());
      lines.push("```");
      lines.push("");
    }
    lines.push(r.body.trim());
    lines.push("");
  }
  const markdown = `${lines.join("\n")}\n`;
  let rel: string | null = null;
  if (opts.out) {
    const stamp = nowIso().replace(/[-:]/g, "").replace(/\.\d+Z$/, "Z");
    const name = `bundle-${stamp}-${ids.slice(0, 3).join("+")}.md`;
    const abs = path.join(store.stateDir, "reviews", name);
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    writeFileAtomic(abs, markdown);
    rel = path.relative(store.root, abs);
  }
  return { inputs, markdown, path: rel };
}
