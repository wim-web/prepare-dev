# vitest プラグイン: 主張の書き方

対象は vitest を使う TypeScript / JavaScript プロジェクト。証拠の種類は `test`。結果は実行したテストケースの範囲でのみ成り立つ（`scope.bounded = true`）。

## spec の native 参照

```yaml
native:
  - plugin: vitest
    ref: packages/runtime/src/questions.test.ts::[C1]
```

`ref` は `<テストファイルの末尾一致>::<テスト名に含まれる文字列>`。テスト名に `[C1]` のような主張の印を付けておくと、一つの主張に複数のテストを対応づけられる。`verify_run` の `targets` にテストファイルを渡す。

## 実行内容

1. テストファイルから相対 import とワークスペース内パッケージ（`packages/*`、`plugins/*`、`apps/*`）の import を推移的にたどり、`inputs.files` に記録する。`plugins.vitest.impl_files` で追加できる。
2. `vitest run --reporter=json --outputFile=...` を実行し、JSON レポートを証拠として保存する。

## 結果の対応

| 状況 | claim の結果 |
| - | - |
| 対応するテストがすべて成功 | `verified` |
| 一つでも失敗 | `failed` |
| 対応するテストがない | `unknown` |
| レポートが得られない | `tool_error` |
