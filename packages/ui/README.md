# @prepare-dev/ui

人が説明と候補を読み、質問に回答するための Web UI。仕様は React 19 での実装とし、json-render は採用しない。単一 HTML ファイルとして配布する。

根拠は [DEC-0005](../../prepare/decisions/DEC-0005.md)、変更後の契約と受入条件は [DES-0002](../../prepare/designs/DES-0002.md)。Reactへの移行は実装済み。配布物の構成と受入条件は [DES-0003](../../prepare/designs/DES-0003.md) と [SPEC-0004](../../prepare/specs/SPEC-0004.md) に記録する。

- ビルド: `pnpm --filter @prepare-dev/ui build` → `packages/ui/dist/index.html`（JS/CSS を全てインライン化）。デーモン（`prepare-dev serve`）がこれを `/` と `/q/:id` で配信する。
- 開発: `PREPARE_DEV_URL=http://127.0.0.1:7777 pnpm --filter @prepare-dev/ui dev`（`/api`・`/events` をデーモンへプロキシ）。型検査: `pnpm --filter @prepare-dev/ui typecheck`。

## 変更後の仕様

- React の部品が質問データを描画する。`prompt` の背景・判断点・人に尋ねる理由と、`options` の候補・帰結・推奨理由を同じ画面に表示する。
- `prompt` の Markdown は段落・箇条書き・表を表示でき、比較が必要な質問には表を使える。画面の配置は UI 側で管理する。
- 一つの質問で一つの独立した判断を扱う。回答の保存、対象版の照合、再取得は既存の runtime を使う。
- CLI から質問の内容を登録できる。画面 JSON の入力、`ui.spec`、catalog、json-render の spec 生成・検証は要求しない。
- `GET /api/questions/:id/ui` とその呼び出しは廃止し、質問データの取得にそろえる。既存質問の `ui.spec` は表示に使わず、過去の質問・回答・判断を保持する。

## 操作と通信

単一選択・複数選択・自由記述に対応する。空回答と送信中の再送を防ぎ、失敗時は入力を保持する。回答の保存、判断への反映、終了、対象版の変更を表示する。接続エラー時はSSE接続を作り直し、復帰時に一覧と表示中の質問を再取得する。関連記録は補足として折りたたむ。

実操作の結果は [配布検証記録](../../docs/2026-11-09-release-verification.md) を参照。
