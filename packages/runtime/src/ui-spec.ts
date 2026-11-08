import type { Question } from "@prepare-dev/core";

/**
 * Default json-render spec for a question. The spec is derived display data; the
 * question JSON stays the source of truth. The catalog this targets is defined in
 * packages/ui (components: Card, Text, SubjectList, OptionList, TextInput, Button;
 * action: submit_answer).
 */
export function defaultQuestionSpec(q: Question): Record<string, unknown> {
  const multiple = q.kind === "multi_choice";
  const elements: Record<string, unknown> = {
    card: {
      type: "Card",
      props: { title: q.prompt, subtitle: `${q.id} · ${q.kind}` },
      children: ["subjects", ...(q.options.length ? ["options"] : []), ...(q.allow_free_text ? ["text"] : []), "submit"],
    },
    subjects: {
      type: "SubjectList",
      props: { subjects: q.subjects },
    },
    options: {
      type: "OptionList",
      props: {
        options: q.options,
        multiple,
        value: { $bindState: "/answer/choice" },
      },
    },
    text: {
      type: "TextInput",
      props: {
        label: q.kind === "free_text" ? "回答" : "補足（任意）",
        multiline: true,
        value: { $bindState: "/answer/text" },
      },
    },
    submit: {
      type: "Button",
      props: { label: "回答を送る", variant: "primary" },
      on: {
        press: {
          action: "submit_answer",
          params: {
            question_id: q.id,
            choice: { $state: "/answer/choice" },
            text: { $state: "/answer/text" },
          },
        },
      },
    },
  };
  return {
    root: "card",
    elements,
    state: { answer: { choice: multiple ? [] : null, text: "" } },
  };
}

export function questionSpec(q: Question): Record<string, unknown> {
  if (q.ui.spec && typeof q.ui.spec === "object") return q.ui.spec as Record<string, unknown>;
  return defaultQuestionSpec(q);
}
