import { useId, useState, type FormEvent } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, errorMessage, type Question, type Answer } from "./api";

type Submission =
  | { kind: "idle" }
  | { kind: "sending" }
  | { kind: "sent"; answer: Answer }
  | { kind: "error"; message: string };

export function Markdown({ children }: { children: string }) {
  return <div className="markdown"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml>{children}</ReactMarkdown></div>;
}

export function questionTitle(prompt: string): string {
  return prompt.split("\n").find((line) => line.trim())?.replace(/^#{1,6}\s+/, "").trim() ?? "質問";
}

export function QuestionView({ question, onSubmitted }: { question: Question; onSubmitted?: () => void }) {
  const name = useId();
  const [choice, setChoice] = useState<string[]>([]);
  const [text, setText] = useState("");
  const [submission, setSubmission] = useState<Submission>({ kind: "idle" });
  const multiple = question.kind === "multi_choice";
  const busy = submission.kind === "sending";
  const answered = question.answers.some((a) => !a.closed && !a.stale);
  const submitted = submission.kind === "sent" ? question.answers.find((a) => a.answer_id === submission.answer.answer_id) ?? submission.answer : null;
  const done = answered || (submitted !== null && !submitted.closed && !submitted.stale);
  const stale = submitted?.stale || (!answered && question.answers.some((a) => a.stale && !a.closed));
  const canSend = choice.length > 0 || text.trim().length > 0;
  const toggle = (key: string) => setChoice((current) => multiple
    ? question.options.filter((o) => o.key === key ? !current.includes(key) : current.includes(o.key)).map((o) => o.key)
    : [key]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy || done || !canSend) return;
    setSubmission({ kind: "sending" });
    try {
      const result = await api.submitAnswer(question.id, { choice, text: text.trim() || null });
      setSubmission({ kind: "sent", answer: result.answer });
      onSubmitted?.();
    } catch (error) {
      setSubmission({ kind: "error", message: errorMessage(error) });
    }
  };

  return <section className="card decision-card">
    <header className="card-head">
      <p className="eyebrow">{done ? "回答済み" : "あなたに確認したいこと"}</p>
      <Markdown>{question.prompt}</Markdown>
    </header>
    <div className="card-body">
      {stale ? <p className="notice warning" role="status">資料が更新されています。回答は保存されますが、自動では判断に反映されません。エージェントによる再確認が必要です。</p> : null}
      {done ? <p className="notice ok" role="status">
        {question.answers.some((a) => a.applied_as) ? "回答が判断に反映されました。" : "回答を保存しました。エージェントが続きの作業を進めます。"}
      </p> : <form onSubmit={(event) => void submit(event)}>
        <fieldset disabled={busy} className="answer-fields">
          {question.options.length > 0 ? <>
            <legend>{multiple ? "当てはまるものを選んでください" : "一つ選んでください"}</legend>
            <div className="option-list">
              {question.options.map((option) => <label key={option.key} className={`option${choice.includes(option.key) ? " selected" : ""}`}>
                <input type={multiple ? "checkbox" : "radio"} name={name} value={option.key} checked={choice.includes(option.key)} onChange={() => toggle(option.key)} />
                <span className="option-main">
                  <span className="option-label">{option.label}{option.recommended ? <span className="badge">推奨</span> : null}</span>
                  {option.summary ? <span className="option-summary">{option.summary}</span> : null}
                  {option.reason ? <span className="option-reason">{option.recommended ? "推奨の理由：" : "判断材料："}{option.reason}</span> : null}
                </span>
              </label>)}
            </div>
          </> : null}
          {question.allow_free_text || question.kind === "free_text" ? <div className="field">
            <label htmlFor={`${name}-text`}>{question.kind === "free_text" ? "あなたの回答" : "補足・別の案（任意）"}</label>
            <textarea id={`${name}-text`} rows={4} value={text} onChange={(event) => setText(event.target.value)} placeholder={question.kind === "free_text" ? "考えや条件を記入してください" : "条件や気になる点があれば記入してください"} />
          </div> : null}
          <button className="btn btn-primary" type="submit" disabled={!canSend || busy}>{busy ? "保存しています…" : "この内容で回答する"}</button>
        </fieldset>
        {submission.kind === "error" ? <p className="notice error" role="alert">回答を保存できませんでした。{submission.message} 入力は残っています。接続を確認して再送してください。</p> : null}
      </form>}
    </div>
  </section>;
}
