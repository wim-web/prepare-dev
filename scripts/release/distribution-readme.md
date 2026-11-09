# prepare-dev

要求・判断・仕様・検証結果をプロジェクトに残すための CLI と skills です。macOS と Linux（arm64・amd64）向けの実行ファイル一つで動き、Node.js や Go などの言語処理系は要りません。

## 導入

```sh
tar -xzf prepare-dev-<version>-<os>-<arch>.tar.gz
install prepare-dev-<version>-<os>-<arch>/prepare-dev ~/.local/bin/   # PATH の通った場所へ
cd your-project
prepare-dev init    # skills を .agents/skills と .claude/skills にコピー
prepare-dev ui      # 判断画面を開く
```

エージェントに `prepare-dev` skill を使うよう依頼すると、CLI で記録・質問・検証を扱います。質問を作ると URL が返るので、ブラウザで開いて回答します。新しい版で `init` を実行すると、編集していない skill は新しい版に置き換わり、編集した skill は残ります。

## よく使うコマンド

```sh
prepare-dev status
prepare-dev record create --kind requirement --title "予約人数の上限" --body-file requirement.md
prepare-dev q create --subject REQ-0001 --prompt-file question.md --options-file options.json
prepare-dev q wait Q-0001
prepare-dev q apply Q-0001 ANS-0001-1 --title "上限を決定" --body-file decision.md
prepare-dev verify run --plugin gotest --claim SPEC-0001/C1 --target internal/app/app_test.go --wait
prepare-dev trace REQ-0001
prepare-dev stop
```

すべてのコマンドと引数は `prepare-dev help` にあります。記録を管理するデーモンは必要なときに起動し、`prepare-dev stop` まで動きます。

## 検証

Lean 4（`lean4`）、Vitest（`vitest`）、Go のテスト（`gotest`）の接続処理を同梱しています。検証器そのものは別に導入してください。仕様の書き方と設定は、init がコピーする prepare-verify の skill の `references/`（`lean4.md`、`vitest.md`、`gotest.md`）にあります。デーモンは起動した CLI の PATH で検証器を探すので、検証器を入れ替えたら `prepare-dev stop` で止めてから使ってください。

## ブランチと worktree（Branches and worktrees）

記録（`prepare/`）は一つの作業コピーでだけ書いてください。ID は作業コピーごとに同じ番号から振られるので、二つのブランチや worktree で書いた `prepare/` をマージすると、同じ番号の記録やジャーナル（`prepare/events.jsonl`）が衝突します。衝突は、ほかのコンフリクトと同じくマージのときに直してください（中身の違う同じ番号の記録を両方残すなら、片方を新しい番号で作り直します）。直した結果に矛盾が残っていると、デーモンは何も書かずに `status` の problems に示します。

## 更新と停止

別の版の実行ファイルで動いているデーモンには接続せず、その版と pid を表示します。版を上げたら `prepare-dev stop` で止めてから使ってください。応答しないデーモンは、表示される pid のプロセスを止めてください。判断画面と API は 127.0.0.1 でだけ配信します。

同梱ライブラリのライセンスは `THIRD_PARTY_NOTICES.txt`、EGM 由来資料のライセンスと出典は `skills/prepare-review/references/egm/` にあります。
