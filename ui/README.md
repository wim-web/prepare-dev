# 判断画面（ui/）

人が説明と候補を読み、質問に回答するための画面。React 19 で作り、Vite で JS・CSS をインライン化した HTML ファイル 1 枚にビルドする。ビルド結果 `build/index.html` を Git に入れ、Go の実行ファイルに埋め込む（`assets.go`）。デーモンがこれを `/` と `/q/:id` で配信する。

根拠は [DEC-0005](../prepare/decisions/DEC-0005.md)（React で作る）と [DEC-0006](../prepare/decisions/DEC-0006.md)（本体を Go にし、画面はビルドを埋め込む）、画面の契約は [DES-0002](../prepare/designs/DES-0002.md)、配布の構成は [DES-0004](../prepare/designs/DES-0004.md)、受入条件は [SPEC-0004](../prepare/specs/SPEC-0004.md)。

## 開発

Node.js 26 以上と pnpm が要る。画面を直す人だけが使い、prepare-dev のビルドと利用には要らない。

```sh
cd ui
pnpm install
pnpm test        # 部品のテスト（SPEC-0004 U1）
pnpm typecheck
pnpm build       # build/index.html、build/THIRD_PARTY_NOTICES.txt、build/build.json
PREPARE_DEV_URL=http://127.0.0.1:7777 pnpm dev   # /api と /events を動いているデーモンへ中継
```

ビルドは、元にしたソースの digest と出力ファイルの sha256 を `build/build.json` に書く。ソースを変えてビルドし直さないと、Go のテスト（`internal/uibuild` の B1）とリリースのスクリプトが失敗する。直したら `build/` の三つのファイルもコミットする。

## 振る舞い

- 質問の `prompt`（背景・判断点・人に尋ねる理由）と `options`（候補・帰結・推奨理由）を同じ画面に表示する。`prompt` の Markdown は段落・箇条書き・表を表示でき、HTML は実行しない。
- 単一選択・複数選択・自由記述に対応する。空回答と送信中の再送を防ぎ、失敗時は入力を保持する。回答の保存、判断への反映、終了、対象版の変更を表示する。
- 接続が切れたら SSE をつなぎ直し、復帰時に一覧と表示中の質問を取り直す。関連記録は補足として折りたたむ。
