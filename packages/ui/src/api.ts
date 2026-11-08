
// Mirrors the JSON the runtime daemon serves (see packages/core/src/schemas.ts and
// packages/runtime/src/server.ts). Only the fields the UI reads are listed.

export interface SubjectRef {
  id: string;
  rev: number;
  sha256: string;
}

export interface QuestionOption {
  key: string;
  label: string;
  summary?: string;
  recommended?: boolean;
  reason?: string;
}

export interface Answer {
  answer_id: string;
  answered_at: string;
  stale: boolean;
  choice: string[] | null;
  text: string | null;
  received_at: string | null;
  applied_as: { decision_id: string; txid: string } | null;
  closed: { at: string; reason: string } | null;
}

export type QuestionKind = "single_choice" | "multi_choice" | "free_text" | "review";

export interface Question {
  id: string;
  rev: number;
  state: string;
  kind: QuestionKind;
  created_at: string;
  prompt: string;
  options: QuestionOption[];
  allow_free_text: boolean;
  subjects: SubjectRef[];
  answers: Answer[];
  cancelled?: { at: string; reason: string; replaced_by: string | null } | null;
}

export interface PendingAnswer {
  question_id: string;
  answer_id: string;
  question_rev: number;
  kind: QuestionKind;
  prompt: string;
  subjects: SubjectRef[];
  answered_at: string;
  stale: boolean;
  received: boolean;
  choice: string[] | null;
  text: string | null;
}

export interface RecordView {
  id: string;
  kind: string;
  rev: number;
  sha256: string;
  frontmatter: Record<string, unknown> & { title?: string; status?: string };
  body: string;
}

export interface ApiErrorBody {
  error: string;
  message: string;
  details?: unknown;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly body: ApiErrorBody,
  ) {
    super(body.message);
    this.name = "ApiError";
  }
}

function isErrorBody(v: unknown): v is ApiErrorBody {
  return typeof v === "object" && v !== null && typeof (v as ApiErrorBody).error === "string" && typeof (v as ApiErrorBody).message === "string";
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = { accept: "application/json" };
  if (init.body !== undefined) headers["content-type"] = "application/json";
  const res = await fetch(path, { ...init, headers: { ...headers, ...(init.headers as Record<string, string> | undefined) } });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    throw new ApiError(res.status, isErrorBody(data) ? data : { error: `http_${res.status}`, message: text || res.statusText });
  }
  return data as T;
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return `${e.body.message} (${e.body.error})`;
  if (e instanceof Error) return e.message;
  return String(e);
}

const enc = encodeURIComponent;

export const api = {
  questionsNeedingAnswer: () => request<Question[]>("/api/questions?needs_answer=1"),
  question: (id: string) => request<Question>(`/api/questions/${enc(id)}`),
  pending: () => request<PendingAnswer[]>("/api/pending"),
  record: (id: string) => request<RecordView>(`/api/records/${enc(id)}`),
  submitAnswer: (id: string, body: { choice: string[] | string | null; text: string | null }) =>
    request<{ question: Question; answer: Answer }>(`/api/questions/${enc(id)}/answers`, { method: "POST", body: JSON.stringify(body) }),
};
