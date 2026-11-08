import fs from "node:fs";
import path from "node:path";
import { EventEmitter } from "node:events";
import {
  BaseFrontmatter,
  DecisionFrontmatter,
  Event,
  type EventOf,
  ID_PREFIXES,
  ProjectConfig,
  Question,
  RunRecord,
  SpecFrontmatter,
  type RecordKind,
  nextId,
  nowIso,
  parseMarkdown,
  randomId,
  serializeMarkdown,
  sha256Hex,
  todayIso,
} from "@prepare-dev/core";
import { RuntimeError } from "./errors.js";

export const KIND_DIRS: Record<RecordKind, string> = {
  discovery: "discovery",
  requirement: "requirements",
  decision: "decisions",
  design: "designs",
  spec: "specs",
  review: "reviews",
  record: "records",
  proposal: "proposals",
};

export interface IndexEntry {
  id: string;
  path: string; // relative to root
  rev: number;
  sha256: string;
  removed?: boolean;
}

export interface RecordView {
  id: string;
  kind: RecordKind;
  path: string;
  rev: number;
  sha256: string;
  frontmatter: BaseFrontmatter & Record<string, unknown>;
  body: string;
}

export interface ExpectedVersion {
  rev: number;
  sha256: string;
}

/**
 * Single-writer persistent store: markdown records, question/run JSON files and the
 * append-only events journal. All file writes are atomic (temp file + rename); journal
 * appends are fsynced. The index is a cache rebuilt from the journal.
 */
export class Store {
  readonly root: string;
  readonly config: ProjectConfig;
  readonly recordsDir: string;
  readonly stateDir: string;
  readonly eventsFile: string;
  readonly indexFile: string;
  readonly questionsDir: string;
  readonly runsDir: string;
  readonly emitter = new EventEmitter();
  private index = new Map<string, IndexEntry>();
  private events: Event[] = [];
  private dead = false;
  /** Files under the records dir that could not be read as records (bad frontmatter etc.). */
  problems: Array<{ path: string; message: string }> = [];

  private constructor(root: string, config: ProjectConfig) {
    this.root = root;
    this.config = config;
    this.recordsDir = path.join(root, config.records_dir);
    this.stateDir = path.join(root, ".prepare-dev");
    this.eventsFile = path.join(this.recordsDir, "events.jsonl");
    this.indexFile = path.join(this.stateDir, "index.json");
    this.questionsDir = path.join(this.recordsDir, "questions");
    this.runsDir = path.join(this.recordsDir, "runs");
  }

  static loadConfig(root: string): ProjectConfig {
    const file = path.join(root, ".prepare-dev", "config.json");
    if (!fs.existsSync(file)) return ProjectConfig.parse({});
    return ProjectConfig.parse(JSON.parse(fs.readFileSync(file, "utf8")));
  }

  static open(root: string): Store {
    const store = new Store(path.resolve(root), Store.loadConfig(root));
    store.ensureDirs();
    store.loadEvents();
    store.rebuildIndex();
    store.scanAll();
    return store;
  }

  // ---------- lifecycle ----------

  private ensureDirs(): void {
    for (const d of [this.recordsDir, this.stateDir, this.questionsDir, this.runsDir, ...Object.values(KIND_DIRS).map((k) => path.join(this.recordsDir, k))]) {
      fs.mkdirSync(d, { recursive: true });
    }
    if (!fs.existsSync(this.eventsFile)) fs.writeFileSync(this.eventsFile, "");
  }

  /** Mark this instance unusable (simulated crash). A new Store must be opened. */
  kill(): void {
    this.dead = true;
  }

  assertAlive(): void {
    if (this.dead) throw new RuntimeError("dead", "store instance is dead; reopen it");
  }

  // ---------- journal ----------

  private loadEvents(): void {
    const text = fs.readFileSync(this.eventsFile, "utf8");
    this.events = [];
    for (const line of text.split("\n")) {
      if (!line.trim()) continue;
      this.events.push(Event.parse(JSON.parse(line)));
    }
  }

  allEvents(): readonly Event[] {
    return this.events;
  }

  appendEvent<T extends Event["type"]>(e: Omit<EventOf<T>, "ts">): EventOf<T> {
    this.assertAlive();
    const ev = Event.parse({ ...e, ts: nowIso() }) as EventOf<T>;
    const fd = fs.openSync(this.eventsFile, "a");
    try {
      fs.writeSync(fd, `${JSON.stringify(ev)}\n`);
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    this.events.push(ev);
    this.applyEventToIndex(ev);
    this.writeIndexFile();
    this.emitter.emit("event", ev);
    return ev;
  }

  intents(): EventOf<"apply.intent">[] {
    return this.events.filter((e): e is EventOf<"apply.intent"> => e.type === "apply.intent");
  }

  committedTxids(): Set<string> {
    return new Set(this.events.filter((e) => e.type === "apply.commit").map((e) => (e as EventOf<"apply.commit">).txid));
  }

  abortedTxids(): Set<string> {
    return new Set(this.events.filter((e) => e.type === "apply.abort").map((e) => (e as EventOf<"apply.abort">).txid));
  }

  isCommitted(txid: string): boolean {
    return this.committedTxids().has(txid);
  }

  inflightIntents(): EventOf<"apply.intent">[] {
    const committed = this.committedTxids();
    const aborted = this.abortedTxids();
    return this.intents().filter((i) => !committed.has(i.txid) && !aborted.has(i.txid));
  }

  committedIntentFor(answerId: string): EventOf<"apply.intent"> | null {
    const committed = this.committedTxids();
    for (const i of this.intents()) {
      if (i.answer_id === answerId && committed.has(i.txid)) return i;
    }
    return null;
  }

  // ---------- index ----------

  private applyEventToIndex(ev: Event): void {
    switch (ev.type) {
      case "record.write":
      case "record.discover":
        this.index.set(ev.id, { id: ev.id, path: ev.path, rev: ev.rev, sha256: ev.sha256 });
        break;
      case "external_change":
        this.index.set(ev.id, { id: ev.id, path: ev.path, rev: ev.rev_to, sha256: ev.sha256_to });
        break;
      case "record.remove":
        this.index.set(ev.id, { id: ev.id, path: ev.path, rev: 0, sha256: "", removed: true });
        break;
      default:
        break;
    }
  }

  private rebuildIndex(): void {
    this.index.clear();
    for (const ev of this.events) this.applyEventToIndex(ev);
    this.writeIndexFile();
  }

  private writeIndexFile(): void {
    const data = { generated_at: nowIso(), records: Object.fromEntries(this.index) };
    try {
      writeFileAtomic(this.indexFile, JSON.stringify(data, null, 2));
    } catch {
      // the index is a cache; failing to write it must not fail the operation
    }
  }

  indexEntries(): IndexEntry[] {
    return [...this.index.values()].filter((e) => !e.removed);
  }

  hasRecord(id: string): boolean {
    const e = this.index.get(id);
    return !!e && !e.removed && fs.existsSync(path.join(this.root, e.path));
  }

  /** All ids ever seen (including removed), for allocation. */
  private usedIds(): string[] {
    const ids = new Set<string>(this.index.keys());
    for (const ev of this.events) {
      if (ev.type === "apply.intent") ids.add(ev.decision_id);
      if (ev.type === "run.record") ids.add(ev.run_id);
      if (ev.type === "question.create") ids.add(ev.question_id);
    }
    return [...ids];
  }

  // ---------- records (markdown) ----------

  recordPath(kind: RecordKind, id: string): string {
    return path.join(this.config.records_dir, KIND_DIRS[kind], `${id}.md`);
  }

  /** Walk the records dir, discover new files, and detect external changes. */
  scanAll(): void {
    this.assertAlive();
    this.problems = [];
    for (const dir of Object.values(KIND_DIRS)) {
      const abs = path.join(this.recordsDir, dir);
      if (!fs.existsSync(abs)) continue;
      for (const name of fs.readdirSync(abs)) {
        if (!name.endsWith(".md") || name.startsWith(".")) continue;
        const rel = path.join(this.config.records_dir, dir, name);
        try {
          this.scanFile(rel);
        } catch (e) {
          // a broken file must not take the daemon down; it is reported in status
          this.problems.push({ path: rel, message: e instanceof Error ? e.message.split("\n")[0]! : String(e) });
        }
      }
    }
    // files that disappeared outside the runtime: keep index entry; hasRecord() reports false
  }

  private scanFile(rel: string): IndexEntry | null {
    const abs = path.join(this.root, rel);
    const text = fs.readFileSync(abs, "utf8");
    const { frontmatter } = parseMarkdown<Record<string, unknown>>(text);
    const id = typeof frontmatter.id === "string" ? frontmatter.id : null;
    if (!id) return null;
    const sha = sha256Hex(text);
    const entry = this.index.get(id);
    if (!entry || entry.removed) {
      const rev = typeof frontmatter.rev === "number" && frontmatter.rev >= 1 ? frontmatter.rev : 1;
      this.appendEvent<"record.discover">({ type: "record.discover", id, path: rel, rev, sha256: sha });
      return this.index.get(id)!;
    }
    if (entry.path !== rel) {
      entry.path = rel; // moved outside the runtime; keep version bookkeeping
    }
    if (entry.sha256 !== sha) {
      return this.recordExternalChange(entry, text, frontmatter, sha);
    }
    return entry;
  }

  private recordExternalChange(entry: IndexEntry, text: string, fm: Record<string, unknown>, shaNow: string): IndexEntry {
    const fileRev = typeof fm.rev === "number" ? fm.rev : 0;
    const newRev = Math.max(entry.rev, fileRev) + 1;
    const { body } = parseMarkdown(text);
    const newFm = { ...fm, rev: newRev, updated: todayIso() };
    const newText = serializeMarkdown({ frontmatter: newFm, body });
    writeFileAtomic(path.join(this.root, entry.path), newText);
    const newSha = sha256Hex(newText);
    this.appendEvent<"external_change">({
      type: "external_change",
      id: entry.id,
      path: entry.path,
      rev_from: entry.rev,
      rev_to: newRev,
      sha256_from: entry.sha256,
      sha256_to: newSha,
    });
    void shaNow;
    return this.index.get(entry.id)!;
  }

  readRecord(id: string): RecordView {
    this.assertAlive();
    let entry = this.index.get(id);
    if (!entry || entry.removed) {
      this.scanAll();
      entry = this.index.get(id);
      if (!entry || entry.removed) throw new RuntimeError("not_found", `record ${id} not found`, { id });
    }
    const abs = path.join(this.root, entry.path);
    if (!fs.existsSync(abs)) throw new RuntimeError("not_found", `record file missing: ${entry.path}`, { id });
    try {
      entry = this.scanFile(entry.path) ?? entry;
    } catch (e) {
      if (e instanceof RuntimeError) throw e;
      throw new RuntimeError("invalid", `record ${id} cannot be parsed: ${e instanceof Error ? e.message.split("\n")[0] : String(e)}`, { id, path: entry.path });
    }
    const text = fs.readFileSync(abs, "utf8");
    const { frontmatter, body } = parseMarkdown<Record<string, unknown>>(text);
    const fm = BaseFrontmatter.parse(frontmatter) as BaseFrontmatter & Record<string, unknown>;
    return { id, kind: fm.kind, path: entry.path, rev: entry.rev, sha256: entry.sha256, frontmatter: fm, body };
  }

  listRecords(kind?: RecordKind): RecordView[] {
    this.scanAll();
    const out: RecordView[] = [];
    for (const e of this.indexEntries()) {
      if (!fs.existsSync(path.join(this.root, e.path))) continue;
      let v: RecordView;
      try {
        v = this.readRecord(e.id);
      } catch (err) {
        if (err instanceof RuntimeError && err.code === "invalid") continue; // listed in problems
        throw err;
      }
      if (!kind || v.kind === kind) out.push(v);
    }
    out.sort((a, b) => a.id.localeCompare(b.id));
    return out;
  }

  allocateId(kind: RecordKind | "question" | "run"): string {
    const prefix = ID_PREFIXES[kind];
    const used = new Set(this.usedIds());
    if (kind === "question") for (const f of this.listJsonIds(this.questionsDir)) used.add(f);
    if (kind === "run") for (const f of this.listJsonIds(this.runsDir)) used.add(f);
    return nextId(prefix, used);
  }

  private validateFrontmatter(fm: Record<string, unknown>): BaseFrontmatter & Record<string, unknown> {
    const base = BaseFrontmatter.parse(fm);
    if (base.kind === "decision") return DecisionFrontmatter.parse(fm) as BaseFrontmatter & Record<string, unknown>;
    if (base.kind === "spec") return SpecFrontmatter.parse(fm) as BaseFrontmatter & Record<string, unknown>;
    return base as BaseFrontmatter & Record<string, unknown>;
  }

  /**
   * Create or update a record. For updates, `expected` must match the current file
   * (rev AND sha256, checked against the actual file, not the index).
   */
  writeRecord(id: string, frontmatter: Record<string, unknown>, body: string, expected?: ExpectedVersion): RecordView {
    this.assertAlive();
    const exists = this.hasRecord(id);
    let rev = 1;
    let created = todayIso();
    let rel: string;
    if (exists) {
      const cur = this.readRecord(id);
      if (!expected) throw new RuntimeError("conflict", `record ${id} exists; expected_rev and expected_sha256 are required`, { id, rev: cur.rev, sha256: cur.sha256 });
      if (expected.rev !== cur.rev || expected.sha256 !== cur.sha256) {
        throw new RuntimeError("conflict", `record ${id} changed: expected rev ${expected.rev}, current rev ${cur.rev}`, {
          id,
          expected,
          current: { rev: cur.rev, sha256: cur.sha256 },
        });
      }
      rev = cur.rev + 1;
      created = String(cur.frontmatter.created ?? created);
      rel = cur.path;
      if (frontmatter.kind && frontmatter.kind !== cur.kind) throw new RuntimeError("invalid", "kind cannot change", { id });
      frontmatter = { ...cur.frontmatter, ...frontmatter };
    } else {
      const kind = frontmatter.kind as RecordKind | undefined;
      if (!kind || !(kind in KIND_DIRS)) throw new RuntimeError("invalid", `invalid kind for new record ${id}`, { id });
      rel = this.recordPath(kind, id);
      if (typeof frontmatter.created === "string") created = frontmatter.created;
    }
    const fm = this.validateFrontmatter({ ...frontmatter, id, rev, created, updated: todayIso() });
    const text = serializeMarkdown({ frontmatter: fm, body });
    const abs = path.join(this.root, rel);
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    writeFileAtomic(abs, text);
    const sha = sha256Hex(text);
    this.appendEvent<"record.write">({ type: "record.write", id, path: rel, rev, sha256: sha });
    return { id, kind: fm.kind, path: rel, rev, sha256: sha, frontmatter: fm, body };
  }

  createRecord(kind: RecordKind, frontmatter: Record<string, unknown>, body: string): RecordView {
    const id = this.allocateId(kind);
    return this.writeRecord(id, { ...frontmatter, kind }, body);
  }

  removeRecord(id: string, reason: string): void {
    this.assertAlive();
    const e = this.index.get(id);
    if (!e || e.removed) return;
    const abs = path.join(this.root, e.path);
    if (fs.existsSync(abs)) fs.rmSync(abs);
    this.appendEvent<"record.remove">({ type: "record.remove", id, path: e.path, reason });
  }

  // ---------- questions and runs (json) ----------

  private listJsonIds(dir: string): string[] {
    if (!fs.existsSync(dir)) return [];
    return fs
      .readdirSync(dir)
      .filter((n) => n.endsWith(".json") && !n.startsWith("."))
      .map((n) => n.replace(/\.json$/, ""));
  }

  listQuestionIds(): string[] {
    return this.listJsonIds(this.questionsDir).sort();
  }

  readQuestion(id: string): Question {
    this.assertAlive();
    const file = path.join(this.questionsDir, `${id}.json`);
    if (!fs.existsSync(file)) throw new RuntimeError("not_found", `question ${id} not found`, { id });
    return Question.parse(JSON.parse(fs.readFileSync(file, "utf8")));
  }

  writeQuestion(q: Question): void {
    this.assertAlive();
    const parsed = Question.parse(q);
    writeFileAtomic(path.join(this.questionsDir, `${q.id}.json`), `${JSON.stringify(parsed, null, 2)}\n`);
  }

  listRunIds(): string[] {
    return this.listJsonIds(this.runsDir).sort();
  }

  readRun(id: string): RunRecord {
    const file = path.join(this.runsDir, `${id}.json`);
    if (!fs.existsSync(file)) throw new RuntimeError("not_found", `run ${id} not found`, { id });
    return RunRecord.parse(JSON.parse(fs.readFileSync(file, "utf8")));
  }

  writeRun(run: RunRecord): void {
    this.assertAlive();
    const parsed = RunRecord.parse(run);
    writeFileAtomic(path.join(this.runsDir, `${run.id}.json`), `${JSON.stringify(parsed, null, 2)}\n`);
  }

  runArtifactDir(id: string): string {
    const dir = path.join(this.runsDir, id);
    fs.mkdirSync(dir, { recursive: true });
    return dir;
  }

  /** Current version of a file relative to root, or null if missing. */
  fileVersion(rel: string): string | null {
    const abs = path.join(this.root, rel);
    if (!fs.existsSync(abs)) return null;
    return sha256Hex(fs.readFileSync(abs));
  }
}

export function writeFileAtomic(file: string, content: string): void {
  const dir = path.dirname(file);
  fs.mkdirSync(dir, { recursive: true });
  const tmp = path.join(dir, `.${path.basename(file)}.${randomId(4)}.tmp`);
  const fd = fs.openSync(tmp, "w");
  try {
    fs.writeSync(fd, content);
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
  fs.renameSync(tmp, file);
}
