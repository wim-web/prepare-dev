# 判断画面（ui/）

人が説明と候補を読み、質問に回答するための画面。React 19 で作り、Vite で JS・CSS をインライン化した HTML ファイル 1 枚にビルドする。ビルド結果 `build/index.html` を Git に入れ、Go の実行ファイルに埋め込む（`assets.go`）。画面の契約は [DES-0002](../prepare/designs/DES-0002.md)。

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

ビルドは、元にしたソースの digest と出力ファイルの sha256 を `build/build.json` に書く。ソースを変えてビルドし直さないと、Go のテスト（`internal/uibuild`）とリリースのスクリプトが失敗するので、直したら `build/` の三つのファイルもコミットする。
