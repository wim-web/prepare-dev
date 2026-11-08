import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";
import { QuestionView } from "./QuestionView";
import type { Question } from "./api";

it("[U1] renders a decision with Markdown comparison, consequences and recommendation without UI JSON", () => {
  const question: Question = {
    id: "Q-0001", rev: 1, state: "open", kind: "single_choice", created_at: "2026-11-09", subjects: [], answers: [], allow_free_text: true,
    prompt: "# 予約人数の上限\n\n## 背景\n待ち時間と受け入れ人数を比較します。\n\n| 案 | 影響 |\n| - | - |\n| 4人 | 短い待ち時間 |\n| 6人 | 多く受け入れる |\n\n- あなたの優先順位を確認したい\n\n<script>alert(1)</script>",
    options: [{ key: "four", label: "4人まで", summary: "混雑を抑えます", recommended: true, reason: "待ち時間を短くできます" }, { key: "six", label: "6人まで", summary: "一度に多く受け入れます" }],
  };
  const html = renderToStaticMarkup(createElement(QuestionView, { question }));
  for (const content of ["<table>", "<th>案</th>", "予約人数の上限", "あなたの優先順位", "混雑を抑えます", "推奨の理由：", "待ち時間を短くできます", 'type="radio"', "この内容で回答する"]) expect(html).toContain(content);
  expect(html).not.toContain("<script");
  expect(html).not.toContain("REQ-0001");
  expect(html).not.toContain("checked=");
});
