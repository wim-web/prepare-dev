import { useCallback, useEffect, useRef, useState } from "react";
import type { Spec } from "@json-render/core";
import { api, errorMessage, type Answer, type PendingAnswer, type Question } from "./api";
import { QuestionView } from "./QuestionView";
import { SubjectRecords } from "./SubjectRecords";

const KIND_LABEL: Record<string, string> = {
  single_choice: "単一選択",
  multi_choice: "複数選択",
  free_text: "自由記述",
  review: "レビュー",
};

function idFromPath(pathname: string): string | null {
  const m = /^\/q\/([^/]+)\/?$/.exec(pathname);
  return m ? decodeURIComponent(m[1]!) : null;
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("ja-JP");
}

function answerStatus(a: Answer): string {
  if (a.closed) return `クローズ (${a.closed.reason})`;
  if (a.applied_as) return `反映済み → ${a.applied_as.decision_id}`;
  if (a.received_at) return "エージェントが受領";
  return "エージェント処理待ち";
}

export function App() {
  const [questions, setQuestions] = useState<Question[] | null>(null);
  const [pending, setPending] = useState<PendingAnswer[]>([]);
  const [listError, setListError] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);

  const [selectedId, setSelectedId] = useState<string | null>(() => idFromPath(window.location.pathname));
  const [selected, setSelected] = useState<{ question: Question; spec: Spec } | null>(null);
  const [selectError, setSelectError] = useState<string | null>(null);
  const selectedIdRef = useRef(selectedId);
  selectedIdRef.current = selectedId;

  const refresh = useCallback(async () => {
    try {
      const [qs, ps] = await Promise.all([api.questionsNeedingAnswer(), api.pending()]);
      setQuestions(qs);
      setPending(ps);
      setListError(null);
    } catch (e) {
      setListError(errorMessage(e));
    }
  }, []);

  // Initial load + live updates over SSE.
  useEffect(() => {
    void refresh();
    const es = new EventSource("/events");
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    es.addEventListener("question", (ev) => {
      void refresh();
      try {
        const q = JSON.parse((ev as MessageEvent<string>).data) as Question;
        if (q?.id && q.id === selectedIdRef.current) {
          setSelected((cur) => (cur && cur.question.id === q.id ? { ...cur, question: q } : cur));
        }
      } catch {
        // ignore malformed payloads
      }
    });
    return () => es.close();
  }, [refresh]);

  // Browser back/forward.
  useEffect(() => {
    const onPop = () => setSelectedId(idFromPath(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const select = useCallback((id: string | null) => {
    setSelectedId(id);
    const path = id ? `/q/${encodeURIComponent(id)}` : "/";
    if (window.location.pathname !== path) window.history.pushState(null, "", path);
  }, []);

  // Nothing deep-linked: show the first question needing an answer.
  useEffect(() => {
    if (selectedId === null && questions && questions.length > 0) {
      const first = questions[0]!;
      setSelectedId(first.id);
      window.history.replaceState(null, "", `/q/${encodeURIComponent(first.id)}`);
    }
  }, [selectedId, questions]);

  // Load the selected question and its json-render spec.
  useEffect(() => {
    if (!selectedId) {
      setSelected(null);
      return;
    }
    let cancelled = false;
    setSelectError(null);
    Promise.all([api.question(selectedId), api.questionUi(selectedId)])
      .then(([question, spec]) => {
        if (!cancelled) setSelected({ question, spec });
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setSelected(null);
          setSelectError(errorMessage(e));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [selectedId]);

  const onSubmitted = useCallback(() => {
    void refresh();
    const id = selectedIdRef.current;
    if (id) {
      api
        .question(id)
        .then((q) => setSelected((cur) => (cur && cur.question.id === q.id ? { ...cur, question: q } : cur)))
        .catch(() => undefined);
    }
  }, [refresh]);

  return (
    <div className="app">
      <header className="topbar">
        <h1>
          <a
            href="/"
            onClick={(e) => {
              e.preventDefault();
              select(null);
            }}
          >
            prepare-dev
          </a>
        </h1>
        <span className={`conn${connected ? " on" : ""}`} title="SSE (/events)">
          {connected ? "接続中" : "未接続"}
        </span>
      </header>

      <aside className="sidebar">
        <section className="panel">
          <h2>回答待ちの質問</h2>
          {listError ? <p className="notice error">一覧の取得に失敗しました: {listError}</p> : null}
          {questions === null ? (
            <p className="muted">読み込み中…</p>
          ) : questions.length === 0 ? (
            <p className="muted">回答待ちの質問はありません。</p>
          ) : (
            <ul className="question-list">
              {questions.map((q) => (
                <li key={q.id}>
                  <button type="button" className={`question-item${q.id === selectedId ? " active" : ""}`} onClick={() => select(q.id)}>
                    <span className="question-prompt">{q.prompt}</span>
                    <span className="meta">
                      {q.id} · {KIND_LABEL[q.kind] ?? q.kind} · {q.subjects.length} 件の対象
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="panel">
          <h2>エージェント処理待ちの回答</h2>
          {pending.length === 0 ? (
            <p className="muted">ありません。</p>
          ) : (
            <ul className="pending-list">
              {pending.map((p) => (
                <li key={p.answer_id}>
                  <button type="button" className={`question-item${p.question_id === selectedId ? " active" : ""}`} onClick={() => select(p.question_id)}>
                    <span className="question-prompt">{p.prompt}</span>
                    <span className="meta">
                      {p.question_id} · {p.choice && p.choice.length ? p.choice.join(", ") : "選択なし"}
                      {p.text ? ` · ${p.text}` : ""}
                    </span>
                    <span className="meta">
                      {fmtTime(p.answered_at)}
                      {p.received ? " · 受領済み" : ""}
                      {p.stale ? " · 対象が更新済み" : ""}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      </aside>

      <main className="main">
        {selectError ? <p className="notice error">質問を読み込めませんでした: {selectError}</p> : null}
        {!selectedId && !selectError ? <p className="muted">左の一覧から質問を選んでください。</p> : null}
        {selectedId && !selected && !selectError ? <p className="muted">読み込み中…</p> : null}
        {selected ? (
          <>
            <QuestionView key={selected.question.id} spec={selected.spec} onSubmitted={onSubmitted} />

            {selected.question.answers.length > 0 ? (
              <section className="panel">
                <h3>この質問への回答</h3>
                <ul className="answer-list">
                  {selected.question.answers.map((a) => (
                    <li key={a.answer_id}>
                      <span className="answer-choice">{a.choice && a.choice.length ? a.choice.join(", ") : "選択なし"}</span>
                      {a.text ? <span className="answer-text">{a.text}</span> : null}
                      <span className="meta">
                        {fmtTime(a.answered_at)} · {answerStatus(a)}
                        {a.stale ? " · 対象が更新済み" : ""}
                      </span>
                    </li>
                  ))}
                </ul>
              </section>
            ) : null}

            <SubjectRecords subjects={selected.question.subjects} />
          </>
        ) : null}
      </main>
    </div>
  );
}
