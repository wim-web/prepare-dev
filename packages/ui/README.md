# @prepare-dev/ui

人が質問に回答するための Web UI（React 19 + [json-render](https://json-render.dev)）。単一 HTML ファイルとして配布する。

- ビルド: `pnpm --filter @prepare-dev/ui build` → `packages/ui/dist/index.html`（JS/CSS を全てインライン化）。デーモン（`prepare-dev serve`）がこれを `/` と `/q/:id` で配信する。
- 開発: `PREPARE_DEV_URL=http://127.0.0.1:7777 pnpm --filter @prepare-dev/ui dev`（`/api`・`/events` をデーモンへプロキシ）。型検査: `pnpm --filter @prepare-dev/ui typecheck`。

## カタログ契約（`src/catalog.ts`、`defineCatalog(schema, …)` + zod）

- spec はフラット形式 `{ root, elements: { key: { type, props, children?, on? } }, state }`。初期 state は `{ answer: { choice: null | [], text: "" } }`。
- コンポーネント: `Card{title, subtitle?}`（children スロット）、`Text{text, tone?}`、`SubjectList{subjects}`、`OptionList{options, multiple, value}`、`TextInput{label, multiline?, value}`、`Button{label, variant?}`
- 双方向バインド: `OptionList.value` / `TextInput.value` に `{"$bindState": "/answer/choice"}` / `{"$bindState": "/answer/text"}`（実装は `useBoundProp`）。
- アクション: `submit_answer{question_id, choice, text}`。`Button.on.press` から `choice: {"$state": "/answer/choice"}`, `text: {"$state": "/answer/text"}` で呼ぶと `POST /api/questions/:id/answers` を実行し、結果を state `/submit` に書いて画面に表示する。

**spec の生成**: `GET /api/questions/:id/ui` が返す。`packages/runtime/src/ui-spec.ts` の `questionSpec()` が、質問の `ui.spec` があればそれを、なければ `defaultQuestionSpec()`（Card → SubjectList / OptionList / TextInput / Button）を生成する。UI 側（`src/App.tsx`）は質問一覧・対象レコード・処理待ち回答を素の React で描き、質問本体だけを `<Renderer>` で描画する。
