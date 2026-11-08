import { useEffect, useMemo, useRef } from "react";
import type { Spec, StateModel } from "@json-render/core";
import { createStateStore, JSONUIProvider, Renderer, useStateValue, type SetState } from "@json-render/react";
import { createHandlers, registry, SUBMIT_STATE_PATH, type SubmitStatus } from "./registry";

interface Props {
  spec: Spec;
  onSubmitted?: (status: Extract<SubmitStatus, { phase: "sent" }>) => void;
}

/**
 * Renders one question's json-render spec. State is owned by a controlled
 * `createStateStore` seeded from `spec.state` so the submit_answer action can
 * write its result back (at /submit) through json-render's own state model.
 */
export function QuestionView({ spec, onSubmitted }: Props) {
  const store = useMemo(() => createStateStore((spec.state as StateModel | undefined) ?? {}), [spec]);
  const storeRef = useRef(store);
  storeRef.current = store;

  // ActionProvider captures handlers at mount, so handlers reach the store via a ref.
  const handlers = useMemo(() => {
    const setState: SetState = (updater) => {
      const s = storeRef.current;
      const prev = s.getSnapshot();
      const next = updater(prev);
      const updates: Record<string, unknown> = {};
      for (const [key, value] of Object.entries(next)) {
        if (!Object.is(value, prev[key])) updates[`/${key}`] = value;
      }
      s.update(updates);
    };
    return createHandlers(
      () => setState,
      () => storeRef.current.getSnapshot(),
    );
  }, []);

  return (
    <JSONUIProvider registry={registry} store={store} handlers={handlers}>
      <Renderer spec={spec} registry={registry} />
      <SubmitStatusView onSubmitted={onSubmitted} />
    </JSONUIProvider>
  );
}

function SubmitStatusView({ onSubmitted }: Pick<Props, "onSubmitted">) {
  const status = useStateValue<SubmitStatus>(SUBMIT_STATE_PATH);
  const notified = useRef<string | null>(null);
  useEffect(() => {
    if (status?.phase === "sent" && notified.current !== status.answer_id) {
      notified.current = status.answer_id;
      onSubmitted?.(status);
    }
  }, [status, onSubmitted]);

  if (!status) return null;
  if (status.phase === "sending") {
    return (
      <p className="notice" role="status">
        送信中…
      </p>
    );
  }
  if (status.phase === "sent") {
    return (
      <p className="notice ok" role="status">
        回答を送信しました（{status.answer_id}）。エージェントの処理を待っています。
      </p>
    );
  }
  const details = status.details;
  const hasDetails = details !== undefined && details !== null && (typeof details !== "object" || Object.keys(details).length > 0);
  return (
    <div className="notice error" role="alert">
      <p>送信に失敗しました: {status.message}</p>
      {hasDetails ? <pre>{JSON.stringify(details, null, 2)}</pre> : null}
    </div>
  );
}
