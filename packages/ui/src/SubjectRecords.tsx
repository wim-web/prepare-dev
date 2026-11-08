import { useEffect, useState } from "react";
import { api, errorMessage, type RecordView, type SubjectRef } from "./api";

export function SubjectRecords({ subjects }: { subjects: SubjectRef[] }) {
  return (
    <section className="panel">
      <h3>対象レコード</h3>
      {subjects.length === 0 ? <p className="muted">対象レコードはありません。</p> : subjects.map((s) => <SubjectRecord key={s.id} subject={s} />)}
    </section>
  );
}

function SubjectRecord({ subject }: { subject: SubjectRef }) {
  const [open, setOpen] = useState(false);
  const [record, setRecord] = useState<RecordView | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open || record || error) return;
    let cancelled = false;
    api
      .record(subject.id)
      .then((r) => {
        if (!cancelled) setRecord(r);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(errorMessage(e));
      });
    return () => {
      cancelled = true;
    };
  }, [open, record, error, subject.id]);

  const title = record ? String(record.frontmatter.title ?? record.id) : null;
  const drifted = record !== null && record.rev !== subject.rev;

  return (
    <details className="record" open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>
        <code>{subject.id}</code>
        <span className="meta">rev {subject.rev}</span>
        {title ? <span className="record-summary-title">{title}</span> : null}
        {drifted ? <span className="badge warn">現在は rev {record.rev}</span> : null}
      </summary>
      {error ? <p className="notice error">読み込みに失敗しました: {error}</p> : null}
      {open && !record && !error ? <p className="muted">読み込み中…</p> : null}
      {record ? (
        <div className="record-detail">
          <p className="record-title">{title}</p>
          <p className="meta">
            {record.kind} · rev {record.rev}
            {record.frontmatter.status ? ` · ${record.frontmatter.status}` : ""} · {record.sha256.slice(0, 12)}
          </p>
          <pre className="record-body">{record.body.trim() ? record.body : "（本文なし）"}</pre>
        </div>
      ) : null}
    </details>
  );
}
