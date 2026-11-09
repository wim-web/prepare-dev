# vitest プラグイン: 主張の書き方

対象は vitest を使う TypeScript / JavaScript プロジェクト。証拠の種類は `test`。結果は実行したテストケースの範囲でのみ成り立つ（`scope.bounded = true`）。

## spec の native 参照

```yaml
native:
  - plugin: vitest
    ref: src/QuestionView.test.tsx::[U1]
```

`ref` は `<テストファイルの末尾一致>::<テスト名に含まれる文字列>`。テスト名に `[C1]` のような主張の印を付けておくと、一つの主張に複数のテストを対応づけられる。`verify_run` の `targets` にテストファイルを渡す。テストのプロジェクトがルート直下でなければ `config.project_dir` に書く。

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

## 結果の対応

| 状況 | claim の結果 |
| - | - |
| 対応するテストがすべて成功 | `verified` |
| 一つでも失敗 | `failed` |
| 対応するテストがない | `unknown` |
| レポートが得られない | `tool_error` |
