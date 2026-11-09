# lean4 プラグイン: 主張の書き方

プラグインの版: lean4 0.3.0。この版の規則を書く。`ppdev version --json` の `plugins.lean4` がこれと違えば、この写しは実行ファイルより古いので、`ppdev init` で skills を更新する。

対象は Lake プロジェクト（`.prepare-dev/config.json` の `plugins.lean4.project_dir`）。証拠の種類は `proof`。

## spec の native 参照

```yaml
claims:
  - id: C1
    statement: ...
    native:
      - plugin: lean4
        ref: PrepareDev.Spec.applied_at_most_once   # 完全修飾の定理名
```

`verify_run` の `targets` には定理を含むモジュールのファイル（root からの相対パス、例 `model/PrepareDev/Spec/Theorems.lean`）を渡す。

## 実行内容

1. `lake env lean --src-deps` を再帰的に使って推移的な依存ソースを集め、`lean-toolchain`、`lakefile.*`、`lake-manifest.json` とともに `inputs.files` に記録する。
2. 入力ファイルを `sorry` / `admit` / `axiom` / `native_decide` / `implemented_by` について走査する。
3. `lake build <modules>` を実行する。
4. 各定理について `#check @<thm>` と `#print axioms <thm>` を `lake env lean --json` で実行し、仮定を含む完全な命題と公理一覧を記録する。

## 設定の項目

`.prepare-dev/config.json` の `plugins.lean4` と、verify run の `--config-json` を合わせたものを使う。ここに挙げた項目だけを受け付け、知らない項目、型の違う値、config.json の知らないプラグイン名があれば、RUN を作らずに誤りを返す。

| 項目 | 型 | 意味 |
| - | - | - |
| `project_dir` | 文字列 | Lake プロジェクトのディレクトリ（root からのパス）。必須 |
| `allowed_axioms` | 文字列の配列 | 許可する公理。既定は propext、Classical.choice、Quot.sound |
| `build_targets` | 文字列の配列 | `lake build` に渡すモジュール。既定は targets のファイルから求めたモジュール |
| `timeout_ms` | 整数 | `lake build` などの制限時間（ミリ秒）。既定は 600000 |

## 結果の対応

| 状況 | claim の結果 |
| - | - |
| ビルド成功、`sorry` なし、公理が許可一覧（既定: propext, Classical.choice, Quot.sound）内 | `verified` |
| `sorry` または許可外の公理、命題を取得できない | `unknown` |
| 型エラー、ビルド失敗 | `failed` |
| タイムアウト | `timeout` |
| lean / lake が見つからない | `unsupported` |

証明は「定理文が要求を正しく表しているか」までは保証しない。命題は記録に残るので、レビューで要求と照合する。
