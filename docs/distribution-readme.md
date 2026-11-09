# prepare-dev

要求・判断・仕様・検証結果をプロジェクトに残すための CLI と skills です。macOS と Linux（arm64・amd64）向けの実行ファイル一つで動きます。Node.js や Go などの言語処理系は要りません。

## 導入

```sh
tar -xzf prepare-dev-<version>-<os>-<arch>.tar.gz
install prepare-dev-<version>-<os>-<arch>/prepare-dev ~/.local/bin/   # PATH の通った場所へ
cd your-project
prepare-dev init
prepare-dev ui
```

判断画面、skills、検証プラグインの案内は実行ファイルに入っています。置き場所を移しても同じように動きます。

`init` は `.agents/skills` と `.claude/skills` に skills をコピーし、入れたものの digest を `.prepare-dev/skills.json` に残します。次の版で `init` を実行すると、編集されていない skill は新しい版に置き換わります。利用者が編集した skill は残し、出力の `conflicts` に示します。`--replace-skills` を付けると、編集された skill を `.prepare-dev/skills-backup/` に退避してから置き換えます。配置先を選ぶ場合は `--skills-dir`、自分で配置する場合は `--no-skills` を使ってください。MCP の設定は作りません。

エージェントに `prepare-dev` skill を使うよう依頼すると、CLI を通して記録・質問・検証を扱います。質問を作ると URL が返るので、ブラウザで開いて回答します。回答は判断画面からだけ受け付け、CLI には回答の操作がありません。画面は Markdown の説明・比較表・候補の帰結と推奨理由を表示します。

## CLI の例

```sh
prepare-dev record create --kind requirement --title "予約人数の上限" --body-file requirement.md
prepare-dev q create --subject REQ-0001 --prompt-file question.md --options-file options.json
prepare-dev q wait Q-0001 --timeout 50000
prepare-dev q pending
prepare-dev q apply Q-0001 ANS-0001-1 --title "上限を決定" --body-file decision.md
prepare-dev trace REQ-0001
prepare-dev status
prepare-dev stop
```

質問の種類は `q create --kind single_choice|multi_choice|free_text|review` で指定します。既定は単一選択です。`--no-free-text` で選択肢への補足欄を省けます。`options.json` は次の形式です。

```json
[{"key":"four","label":"4人まで","summary":"混雑を抑えます","recommended":true,"reason":"待ち時間を短くできます"}]
```

記録を更新するときは、最初に `record get ID` で読んだ `rev` と `sha256` を渡します。他者がその後に変更していたら更新は拒否されます。

```sh
prepare-dev record get REQ-0001
prepare-dev record put REQ-0001 --expected-rev 1 --expected-sha256 "読み取ったsha256" --body-file requirement.md
```

`--fm-file` は frontmatter の JSON オブジェクトを指定します。`verify run` の `--config-json` は検証器の設定（例: `'{"project_dir":"model"}'`）、`--claim` と `--target` は複数回指定できます。`verify` と `trace` の出力は、入力ファイルの一覧を件数に、不一致と実行したテストの一覧を先頭の数件に縮めます。すべてを見るには `--json` を付けます。

すべての操作で `--root /path/to/project` を使えます。指定しなければ `PREPARE_DEV_ROOT`、それもなければ現在のディレクトリが対象です。必要なときに記録を管理するデーモンを自動で起動します。

## 検証

Lean 4（`lean4`）、Vitest（`vitest`）、Go のテスト（`gotest`）の接続処理を同梱しています。Lean・Vitest・Go そのものは、検証する環境に別途導入してください。`prepare-dev guidance lean4`（`vitest`、`gotest`）が、仕様の書き方と必要な設定を表示します。

```sh
prepare-dev verify run --plugin gotest --claim SPEC-0001/C1 --target internal/app/app_test.go --wait
prepare-dev trace REQ-0001
```

証明・テスト・検査対象と前提を区別して記録します。画面で回答したことだけで、仕様の正しさや実装の検証済みを意味することはありません。デーモンは、それを起動した CLI の PATH で検証器を探します。検証器を入れ替えたら `prepare-dev stop` で止めてから使ってください。

## ブランチと worktree（Branches and worktrees）

記録（`prepare/` の記録・質問・判断・検証の結果とジャーナル）は、一つの作業コピーでだけ書いてください。ID は作業コピーごとに同じ番号から振られるので、二つのブランチや worktree で書いた `prepare/` をマージすると、同じ番号の記録やジャーナル（`prepare/events.jsonl`）が衝突します。衝突は、ほかのコンフリクトと同じくマージのときに直してください（同じ番号で中身の違う記録を両方残すなら、片方は新しい番号で作り直します）。

直した結果のジャーナルに矛盾がある（同じ記録や質問が二度作られている、別の作業コピーで判断に反映した回答が残っているなど）と、デーモンは何も書かずに `status` の problems に示します。衝突の印が残っていると、デーモンは起動せず、CLI がその理由を示します。ブランチの切り替えなどで Git が `prepare/` を書き換えると、動いているデーモンはそれに気付いて何も書かずに止まり、次のコマンドが今の状態で起動し直します。

## 更新と停止

デーモンはプロジェクトごとに一つで、`prepare-dev stop` まで動き続けます。別の版の実行ファイルで動いているデーモンには接続せず、その版・build・場所・pid と停止の方法を表示します。同じ build の写しを別の場所に置いた場合は、そのまま接続します。検証の実行中は停止を拒否します。

同じプロジェクトに書き手を二つ作らないため、デーモンは `.prepare-dev/lease` を排他的にロックしてから記録を開きます。ロックはプロセスの終了とともに外れるので、強制終了したデーモンの `.prepare-dev/daemon.json` が残っていても、次の CLI が古いものと判断して新しいデーモンを起動します。ロックを持ったまま応答しないデーモンは、表示される pid のプロセスを止めてください。判断画面と API は loopback（127.0.0.1）でだけ配信します。

同梱ライブラリのライセンスは `THIRD_PARTY_NOTICES.txt`、EGM 由来資料のライセンスと出典は `skills/prepare-review/references/egm/` にあります。
