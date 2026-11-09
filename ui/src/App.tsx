import { useCallback, useEffect, useRef, useState } from "react";
import { api, errorMessage, type Answer, type PendingAnswer, type Question } from "./api";
import { QuestionView, questionTitle, Markdown } from "./QuestionView";
import { SubjectRecords } from "./SubjectRecords";

const KIND_LABEL: Record<string, string> = {
  single_choice: "単一選択",
  multi_choice: "複数選択",
  free_text: "自由記述",
  review: "レビュー",
};

function idFromPath(pathname: string): string | null {
  const m = /^\/q\/([^/]+)\/?$/.exec(pathname);
  if (!m) return null;
  try {
    return decodeURIComponent(m[1]!);
  } catch {
    return m[1]!; // malformed escape: kept as is, so the question is reported as not found
  }
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("ja-JP");
}

function answerStatus(a: Answer): string {
  if (a.closed) return `クローズ (${a.closed.reason})`;
  if (a.applied_as) return "判断に反映済み";
  if (a.received_at) return "エージェントが受領";
  return "エージェント処理待ち";
}

export function App() {
  const [questions, setQuestions] = useState<Question[] | null>(null);
  const [pending, setPending] = useState<PendingAnswer[]>([]);
  const [listError, setListError] = useState<string | null>(null);
  const [pendingError, setPendingError] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);

  const [selectedId, setSelectedId] = useState<string | null>(() => idFromPath(window.location.pathname));
  const [selected, setSelected] = useState<{ question: Question } | null>(null);
  const [selectError, setSelectError] = useState<string | null>(null);
  const selectedIdRef = useRef(selectedId);
  selectedIdRef.current = selectedId;
  const selectionRequest = useRef(0);

  const refresh = useCallback(async () => {
    const id = selectedIdRef.current;
    const request = ++selectionRequest.current;
    await Promise.all([
      api.questionsNeedingAnswer()
        .then((qs) => { setQuestions(qs); setListError(null); })
        .catch((e: unknown) => setListError(errorMessage(e))),
      api.pending()
        .then((ps) => { setPending(ps); setPendingError(null); })
        .catch((e: unknown) => setPendingError(errorMessage(e))),
      id ? api.question(id).then((question) => {
        if (selectedIdRef.current === id && request === selectionRequest.current) {
          setSelected({ question });
          setSelectError(null);
        }
      }).catch((e: unknown) => {
        if (selectedIdRef.current === id && request === selectionRequest.current) {
          setSelected(null);
          setSelectError(errorMessage(e));
        }
      }) : Promise.resolve(),
    ]);
  }, []);

  // Initial load + live updates over SSE.
  useEffect(() => {
    void refresh();
    let es: EventSource;
    let retry: ReturnType<typeof setTimeout> | undefined;
    const connect = () => {
      es = new EventSource("/events");
      es.onopen = () => {
        setConnected(true);
        void refresh();
      };
      es.onerror = () => {
        setConnected(false);
        es.close();
        retry = setTimeout(connect, 1500);
      };
      es.addEventListener("question", (ev) => {
        void refresh();
        try {
          const q = JSON.parse((ev as MessageEvent<string>).data) as Question;
          if (q?.id && q.id === selectedIdRef.current) {
            setSelected({ question: q });
            setSelectError(null);
          }
        } catch {
          // ignore malformed payloads
        }
      });
    };
    connect();
    return () => { clearTimeout(retry); es.close(); };
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

  // Load the selected question.
  useEffect(() => {
    if (!selectedId) {
      setSelected(null);
      return;
    }
    let cancelled = false;
    const request = ++selectionRequest.current;
    setSelectError(null);
    api.question(selectedId)
      .then((question) => {
        if (!cancelled && request === selectionRequest.current) setSelected({ question });
      })
      .catch((e: unknown) => {
        if (!cancelled && request === selectionRequest.current) {
          setSelected(null);
          setSelectError(errorMessage(e));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [selectedId]);

  const onSubmitted = useCallback(() => { void refresh(); }, [refresh]);

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
        <span className={`conn${connected ? " on" : ""}`} title="回答はこのプロジェクトに保存されます">
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
                    <span className="question-prompt">{questionTitle(q.prompt)}</span>
                    <span className="meta">
                      {KIND_LABEL[q.kind] ?? q.kind}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="panel">
          <h2>エージェント処理待ちの回答</h2>
          {pendingError ? <p className="notice error">回答一覧の取得に失敗しました: {pendingError}</p> : null}
          {pending.length === 0 ? (
            <p className="muted">ありません。</p>
          ) : (
            <ul className="pending-list">
              {pending.map((p) => (
                <li key={p.answer_id}>
                  <button type="button" className={`question-item${p.question_id === selectedId ? " active" : ""}`} onClick={() => select(p.question_id)}>
                    <span className="question-prompt">{questionTitle(p.prompt)}</span>
                    <span className="meta">
                      回答を保存済み
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
        {selectedId && (!selected || selected.question.id !== selectedId) && !selectError ? <p className="muted">読み込み中…</p> : null}
        {selected && selected.question.id === selectedId ? (
          <>
            {selected.question.cancelled ? (
              <section className="panel">
                <p className="notice">
                  この質問は取り消されました（{fmtTime(selected.question.cancelled.at)}）: {selected.question.cancelled.reason}
                </p>
                {selected.question.cancelled.replaced_by ? (
                  <p>
                    置き換え先:{" "}
                    <a
                      href={`/q/${encodeURIComponent(selected.question.cancelled.replaced_by)}`}
                      onClick={(e) => {
                        e.preventDefault();
                        select(selected.question.cancelled!.replaced_by);
                      }}
                    >
                      {selected.question.cancelled.replaced_by}
                    </a>
                  </p>
                ) : null}
                <Markdown>{selected.question.prompt}</Markdown>
              </section>
            ) : (
              <QuestionView key={selected.question.id} question={selected.question} onSubmitted={onSubmitted} />
            )}

            {selected.question.answers.length > 0 ? (
              <section className="panel">
                <h3>この質問への回答</h3>
                <ul className="answer-list">
                  {selected.question.answers.map((a) => (
                    <li key={a.answer_id}>
                      <span className="answer-choice">{a.choice?.map((key) => selected.question.options.find((o) => o.key === key)?.label ?? key).join("、") || "自由記述"}</span>
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
