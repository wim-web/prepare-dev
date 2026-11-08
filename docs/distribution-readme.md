# prepare-dev

要求・判断・仕様・検証結果をプロジェクトに残すためのCLIとskillsです。Node.js 26以上が必要です。

## 導入

```sh
npm install -g ./prepare-dev-0.1.0.tgz
cd your-project
prepare-dev init
prepare-dev ui
```

tarballを展開し、`node package/bin/prepare-dev.mjs` で直接実行することもできます。元のソースやtsxは不要です。

`init` は `.agents/skills` と `.claude/skills` にskillsをコピーします。既存の内容と異なるskillは上書きせず、出力の `conflicts` に示します。プロジェクト内の配置先を選ぶ場合は `--skills-dir`、自分で配置する場合は `--no-skills` を使ってください。MCP設定は作成しません。

エージェントに `prepare-dev` skillを使うよう依頼すると、CLIを通して記録・質問・回答・検証を扱います。質問作成が返すURLをブラウザで開いて回答します。Markdownによる説明・比較表・候補の帰結と推奨理由を表示できます。

## CLIの例

```sh
prepare-dev record create --kind requirement --title "予約人数の上限" --body-file requirement.md
prepare-dev q create --subject REQ-0001 --prompt-file question.md --options-file options.json
prepare-dev q wait Q-0001 --timeout 50000
prepare-dev q pending
prepare-dev q apply Q-0001 ANS-0001-1 --title "上限を決定" --body-file decision.md
prepare-dev trace REQ-0001
prepare-dev status --json
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

`--fm-file` はfrontmatterのJSONオブジェクトを指定します。`verify run` の `--config-json` は検証器の設定（例: `'{"project_dir":"model"}'`）、`--claim` と `--target` は複数回指定できます。

すべての操作で `--root /path/to/project` を使えます。指定しなければ `PREPARE_DEV_ROOT`、それもなければ現在のディレクトリが対象です。必要に応じてruntimeを自動起動します。

## 検証

VitestとLeanの接続処理を同梱しています。Vitestは対象プロジェクトに、Lean/Lakeは実行環境に別途導入してください。`prepare-dev guidance vitest` または `prepare-dev guidance lean4` が、仕様の書き方と必要な設定を表示します。

```sh
prepare-dev verify run --plugin vitest --claim SPEC-0001/C1 --target example.test.ts --wait
prepare-dev trace REQ-0001
```

証明・テスト・検査対象と前提を区別して記録します。UIで回答したことだけで、仕様の正しさや実装の検証済みを意味することはありません。

## 更新と停止

更新または配布物の移動前に `prepare-dev stop` を実行してください。別のbuildや配置場所のruntimeが残っている場合、CLIは接続を拒否して停止・再起動を案内します。検証中の停止は拒否します。

同じプロジェクトに複数の書き手を作らないため、rootから決めたloopback TCPポートを起動中に保持します。別のサービスとの競合時はポート番号をエラーに表示して起動を拒否します。UIとAPIはloopbackで配信します。

MCPは必要な場合だけ `prepare-dev mcp --root /path/to/project` を各クライアントへ手動登録してください。

同梱ライブラリのライセンスは `THIRD_PARTY_NOTICES.txt`、EGM由来資料のライセンスと出典は `skills/prepare-review/references/egm/` にあります。
