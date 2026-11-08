import { defineCatalog } from "@json-render/core";
import { schema } from "@json-render/react/schema";
import { z } from "zod";

// The catalog contract that packages/runtime/src/ui-spec.ts targets. Specs served by
// GET /api/questions/:id/ui must only use these component types and this action.

export const SubjectRefSchema = z.object({
  id: z.string(),
  rev: z.number().int(),
  sha256: z.string(),
});

export const OptionSchema = z.object({
  key: z.string(),
  label: z.string(),
  summary: z.string().optional(),
  recommended: z.boolean().optional(),
  reason: z.string().optional(),
});

/** Selected option keys. The runtime accepts `string[] | string | null`. */
export const ChoiceSchema = z.union([z.array(z.string()), z.string(), z.null()]);

export const catalog = defineCatalog(schema, {
  components: {
    Card: {
      props: z.object({ title: z.string(), subtitle: z.string().optional() }),
      slots: ["default"],
      description: "質問全体を囲むカード。title に質問文、children に本文要素を並べる。",
    },
    Text: {
      props: z.object({ text: z.string(), tone: z.enum(["default", "muted", "warning"]).optional() }),
      description: "補足説明のテキスト。",
    },
    SubjectList: {
      props: z.object({ subjects: z.array(SubjectRefSchema) }),
      description: "質問の対象レコード（id / rev / sha256）の一覧。",
    },
    OptionList: {
      props: z.object({ options: z.array(OptionSchema), multiple: z.boolean(), value: ChoiceSchema.optional() }),
      description: "選択肢。value は {\"$bindState\": \"/answer/choice\"} で双方向バインドする。",
    },
    TextInput: {
      props: z.object({ label: z.string(), multiline: z.boolean().optional(), value: z.string().nullable().optional() }),
      description: "自由記述の入力欄。value は {\"$bindState\": \"/answer/text\"} で双方向バインドする。",
    },
    Button: {
      props: z.object({ label: z.string(), variant: z.enum(["primary", "secondary"]).optional() }),
      description: "ボタン。on.press に submit_answer を割り当てる。",
    },
  },
  actions: {
    submit_answer: {
      params: z.object({ question_id: z.string(), choice: ChoiceSchema, text: z.string().nullable() }),
      description: "回答を POST /api/questions/:id/answers に送る。choice/text は $state で /answer から読む。",
    },
  },
});

export type QuestionCatalog = typeof catalog;
