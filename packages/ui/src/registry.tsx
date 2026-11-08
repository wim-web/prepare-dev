import { useId } from "react";
import { defineRegistry, useActions, useBoundProp } from "@json-render/react";
import { api, ApiError } from "./api";
import { catalog } from "./catalog";

/** Written by the submit_answer action to json-render state at SUBMIT_STATE_PATH. */
export type SubmitStatus =
  | { phase: "sending" }
  | { phase: "sent"; question_id: string; answer_id: string }
  | { phase: "error"; message: string; details?: unknown };

export const SUBMIT_STATE_PATH = "/submit";

function toChoice(value: unknown): string[] | null {
  if (Array.isArray(value)) return value.filter((v): v is string => typeof v === "string");
  if (typeof value === "string") return [value];
  return null;
}

export const { registry, handlers: createHandlers } = defineRegistry(catalog, {
  components: {
    Card: ({ props, children }) => (
      <section className="card">
        <header className="card-head">
          <h2 className="card-title">{props.title}</h2>
          {props.subtitle ? <p className="card-subtitle">{props.subtitle}</p> : null}
        </header>
        <div className="card-body">{children}</div>
      </section>
    ),

    Text: ({ props }) => <p className={`text tone-${props.tone ?? "default"}`}>{props.text}</p>,

    SubjectList: ({ props }) =>
      props.subjects.length === 0 ? null : (
        <ul className="subject-list" aria-label="対象レコード">
          {props.subjects.map((s) => (
            <li key={s.id}>
              <code>{s.id}</code>
              <span className="meta">
                rev {s.rev} · {s.sha256.slice(0, 8)}
              </span>
            </li>
          ))}
        </ul>
      ),

    OptionList: ({ props, bindings }) => {
      const [value, setValue] = useBoundProp<string[] | string | null>(props.value, bindings?.value);
      const groupName = useId();
      const selected = new Set(toChoice(value) ?? []);
      const toggle = (key: string) => {
        if (props.multiple) {
          const next = new Set(selected);
          if (next.has(key)) next.delete(key);
          else next.add(key);
          setValue(props.options.filter((o) => next.has(o.key)).map((o) => o.key));
        } else {
          setValue([key]);
        }
      };
      return (
        <div className="option-list" role={props.multiple ? "group" : "radiogroup"}>
          {props.options.map((o) => (
            <label key={o.key} className={`option${selected.has(o.key) ? " selected" : ""}`}>
              <input
                type={props.multiple ? "checkbox" : "radio"}
                name={groupName}
                value={o.key}
                checked={selected.has(o.key)}
                onChange={() => toggle(o.key)}
              />
              <span className="option-main">
                <span className="option-label">
                  {o.label}
                  {o.recommended ? <span className="badge">推奨</span> : null}
                  <code className="option-key">{o.key}</code>
                </span>
                {o.summary ? <span className="option-summary">{o.summary}</span> : null}
                {o.reason ? <span className="option-reason">{o.reason}</span> : null}
              </span>
            </label>
          ))}
        </div>
      );
    },

    TextInput: ({ props, bindings }) => {
      const [value, setValue] = useBoundProp<string | null>(props.value, bindings?.value);
      const id = useId();
      return (
        <div className="field">
          <label htmlFor={id}>{props.label}</label>
          {props.multiline ? (
            <textarea id={id} rows={4} value={value ?? ""} onChange={(e) => setValue(e.target.value)} />
          ) : (
            <input id={id} type="text" value={value ?? ""} onChange={(e) => setValue(e.target.value)} />
          )}
        </div>
      );
    },

    Button: ({ props, emit }) => {
      const { loadingActions } = useActions();
      const busy = loadingActions.size > 0;
      return (
        <button type="button" className={`btn btn-${props.variant ?? "secondary"}`} disabled={busy} onClick={() => emit("press")}>
          {busy ? "送信中…" : props.label}
        </button>
      );
    },
  },

  actions: {
    // params arrive with `$state` references already resolved by the renderer.
    submit_answer: async (params, setState) => {
      if (!params) return;
      const choice = toChoice(params.choice);
      const text = typeof params.text === "string" && params.text.trim() ? params.text : null;
      const put = (submit: SubmitStatus) => setState((prev) => ({ ...prev, submit }));
      put({ phase: "sending" });
      try {
        const r = await api.submitAnswer(params.question_id, { choice, text });
        put({ phase: "sent", question_id: r.question.id, answer_id: r.answer.answer_id });
      } catch (e) {
        put({
          phase: "error",
          message: e instanceof ApiError ? `${e.body.message} (${e.body.error})` : e instanceof Error ? e.message : String(e),
          details: e instanceof ApiError ? e.body.details : undefined,
        });
      }
    },
  },
});
