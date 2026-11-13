// json-render を使わない版: ページ全体のデータの形。
import { z } from "zod";
import * as P from "./schema.ts";

export const page = z.object({
  spec: P.pageHead.shape.spec,
  title: P.pageHead.shape.title,
  lead: P.pageHead.shape.lead,
  sections: z
    .array(
      z.discriminatedUnion("type", [
        z.object({ type: z.literal("Story"), props: P.story }),
        z.object({ type: z.literal("Promises"), props: P.promises }),
        z.object({ type: z.literal("Explorer"), props: P.explorer }),
        z.object({ type: z.literal("Quiz"), props: P.quiz }),
        z.object({ type: z.literal("Limits"), props: P.limits }),
      ]),
    )
    .min(1)
    .describe("ページに並べる部品。上から順に"),
});

export type Page = z.infer<typeof page>;

// AI に渡す説明（json-render を使わない版では、これを自分で書く）。
export const guide = (rules: string[]) =>
  [
    "あなたは、仕様を読んで、その仕様を人に説明するページの中身を、JSON で書く。",
    "",
    "出力は、下の JSON Schema に合う JSON 一つだけ。前後に説明の文を書かない。",
    "",
    "## 使える部品",
    "",
    ...Object.entries(P.descriptions)
      .filter(([k]) => k !== "Page")
      .map(([k, v]) => `- ${k}: ${v}`),
    "",
    "sections に、使う部品を上から順に並べる。各要素は { \"type\": 部品の名前, \"props\": その部品の中身 } の形。",
    "",
    "## 書き方の決まり",
    "",
    ...rules.map((r) => `- ${r}`),
  ].join("\n");
