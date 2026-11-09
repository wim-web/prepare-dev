# vitest プラグイン: 主張の書き方

プラグインの版: vitest 0.4.0。この版の規則を書く。`ppdev version --json` の `plugins.vitest` がこれと違えば、この写しは実行ファイルより古いので、`ppdev init` で skills を更新する。

対象は vitest を使う TypeScript / JavaScript プロジェクト。証拠の種類は `test`。結果は実行したテストケースの範囲でのみ成り立つ（`scope.bounded = true`）。

## spec の native 参照

```yaml
native:
  - plugin: vitest
    ref: src/QuestionView.test.tsx::[U1]
```

`ref` は `<テストファイルの末尾一致>::<テスト名に含まれる文字列>`。テスト名に `[C1]` のような主張の印を付けておくと、一つの主張に複数のテストを対応づけられる。`verify_run` の `targets` にテストファイルを渡す。一つの主張のテストが複数のファイルにあるときは、そのファイルをすべて targets に入れる。一部だけを走らせた RUN も、vitest の最も新しい RUN として主張の今の結果を決め、走らなかったテストは結果に入らない。テストのプロジェクトがルート直下でなければ `config.project_dir` に書く。

## 入力の記録

`inputs.files` には、結果を左右するファイルを内容の sha256 とともに記録する。記録したファイルが変わるか消えると、その証拠は現在の版に当てはまらなくなる。

1. テストファイル、`config.impl_files` の追加実装、Vitest の設定ファイルを起点に、esbuild でバンドルするのと同じ規則で import を推移的にたどる。相対パス、`package.json` の `main`・`exports`、`tsconfig.json` の `paths`、`node_modules` にリンクしたワークスペースのパッケージを解決する。拡張子の無い import は Vite の既定の順（`.mjs`、`.js`、`.mts`、`.ts`、`.jsx`、`.tsx`、`.json`）で探す。
2. 解決先が `node_modules` の中にあるパッケージは記録しない。インストール結果は manifest と lockfile に一致することを前提にする。
3. JavaScript・TypeScript・JSON・CSS 以外のファイル（画像、`?raw` の読み込み、`.vue` など）は、import されたファイルとして記録するが、その中の import はたどらない。
4. 型だけの import（`import type`、型としてしか使わない import）は、実行時に読み込まれないので記録しない。
5. 対象と各入力の上位ディレクトリ（ルートまで）にある `package.json`、依存の lockfile（pnpm・npm・yarn・bun）、`pnpm-workspace.yaml`、`.npmrc`、`tsconfig.json` を含める。
6. import が読むファイルのディレクトリ（その段だけ）のファイル名の一覧を `inputs.listings` に記録する。隣にファイルが増えて解決先が変わりうる（`api.ts` の隣に `api.js` ができるなど）ので、一覧が変われば証拠を現在の版に当てはまらないものにする。

Vite の設定で決まる解決（`resolve.alias`、`import.meta.glob`、プラグイン）は評価しない。実行時に組み立てたパス、テストがファイル操作で読む fixture、生成に使った元のファイルとあわせて、`impl_files` にルートからのパスかパターン（`*`・`?`・`[...]`、任意の段数の `**`）で列挙する。パターンが探すディレクトリのファイル名の一覧も `inputs.listings` に記録し、一致するファイルが増えたら証拠を現在の版に当てはまらないものにする。`node_modules` の手動改変はこの版照合では検出しない。

## 実行

`vitest run --reporter=json --outputFile=...` を実行し、JSON レポートを証拠として保存する。vitest は project_dir の `node_modules/.bin/vitest`、無ければルートのものを使い、その場所を `tool.extra.path` に残す。

## 設定の項目

`.prepare-dev/config.json` の `plugins.vitest` と、verify run の `--config-json` を合わせたものを使う。ここに挙げた項目だけを受け付け、知らない項目、型の違う値、config.json の知らないプラグイン名があれば、RUN を作らずに誤りを返す。

| 項目 | 型 | 意味 |
| - | - | - |
| `project_dir` | 文字列 | Vitest のプロジェクトのディレクトリ（root からのパス）。既定は `.` |
| `impl_files` | 文字列の配列 | 依存をたどる起点に足す実装のファイル（入力の記録の 1） |
| `bounds_from` | 文字列 | テストが書く範囲の JSON（root からのパス）。中の数と文字列の値を `scope.bounds` に取り込む |
| `timeout_ms` | 整数 | Vitest の制限時間（ミリ秒）。既定は 600000 |
| `env` | 値が文字列のオブジェクト | Vitest に渡す環境変数（`CI=1` はいつも渡す） |

## 結果の対応

| 状況 | claim の結果 |
| - | - |
| 対応するテストがすべて成功 | `verified` |
| 一つでも失敗 | `failed` |
| テストは成功したが、そのファイル自体が失敗（`afterAll` などの hook の例外。describe の中の hook も含め、ほかのテストが失敗していても見分ける） | `failed` |
| ファイルが読み込めず、テストが一つも無い | `ref` のファイルの部分が一致する主張（空ならすべての主張）が `failed` |
| そのファイルで未処理の例外が起きた（テストの後に投げられた例外、拒否された Promise） | `failed` |
| 未処理の例外が起きたファイルが分からない（isolate が無効の project があるときを含む）、または vitest が 0 以外で終わったのに、失敗したテスト・ファイル・未処理の例外が無い | `failed`（すべての主張） |
| 対応するテストがない、またはスキップが混ざる | `unknown` |
| レポートが得られない | `tool_error` |

JSON のレポートには、未処理の例外も、describe の中の hook の例外も出ない（未処理の例外があると、レポートは成功と報告し、vitest は 1 で終わる）。そこで実行のたびに小さな reporter を加え、未処理の例外と、それが起きたテストファイルとテスト、ファイルとその中の suite のエラー、それぞれの project がテストファイルを分けて動かすか（isolate）を `vitest-unhandled.json` に書かせる（Vitest 3 以降の `onInit`・`onTestRunEnd`、それより前の `onFinished`。実際の Vitest で確かめたのは 5.1.0 の `onTestRunEnd` だけで、`onFinished` の経路で読んだときは要約にそう書く）。isolate が無効の project があると、未処理の例外はほかのファイルのテストが残したものでありうるので、起きた場所が分からないものとして扱う。reporter が何も書かなかったときは、未処理の例外も describe の中の hook の例外も読めないので、0 以外の終了コードの規則だけで扱い、終了コードが 0 以外ならそのことを要約に書く（`dangerouslyIgnoreUnhandledErrors` を有効にすると、未処理の例外があっても 0 で終わるので見逃す）。`globalSetup` の後始末の例外は reporter に届かず、同じ実行にほかの失敗があると見分けられない。

ほかのファイルの失敗は、そこに対応しない主張の結果を変えない。主張に及んだテストの外の失敗は、RUN の要約にファイルと理由の一行（レポートの `message` の一行目か、未処理の例外の message）で示す。vitest の終了コードは `tool.extra.exit_code` に残す。
