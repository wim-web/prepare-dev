# prepare-dev

曖昧なプロダクトの意図から、要求・設計・検査可能な仕様・実装・検証結果までを、プロジェクトの記録（`prepare/`）につなぐための CLI と skills。エージェント（Codex、Claude Code）が skills から CLI を使い、人は判断画面で回答する。本体は Go の実行ファイル一つで、判断画面と skills を埋め込む。

使い方は [配布版の README](scripts/release/distribution-readme.md)、構成と設計の理由は [DES-0004](prepare/designs/DES-0004.md)。このプロジェクト自身の要求・仕様・判断・検証結果は `prepare/` にある。

## 構成

| 場所 | 内容 |
| - | - |
| `cmd/prepare-dev`、`internal/` | 本体（デーモン、CLI、HTTP API、検証プラグイン） |
| `ui/` | 判断画面（React）。ビルド結果を Git に入れて埋め込む（[ui/README.md](ui/README.md)） |
| `skills/` | エージェント向け skills の正本。検証プラグインごとの主張の書き方は prepare-verify の `references/` にある |
| `model/` | 回答の適用と回復の Lean 4 モデルと証明、適合テストの操作列 |
| `scripts/release` | 配布物（macOS・Linux の tar.gz）の作成と、同梱する README の元 |
| `prepare/` | このプロジェクト自身の記録 |
| `docs/` | 構想の段階で参照した資料の一覧（記録ではない） |

## 開発

Go 1.27 以上。判断画面を直すときだけ Node.js 26 以上と pnpm を使う。

```bash
go test ./...                                   # 単体・適合・e2e のテスト
go run ./cmd/prepare-dev status --root <対象>    # checkout から動かす
(cd model && bin/test)                          # Lean の証明と、操作列がモデルと一致するか
(cd ui && pnpm install && pnpm test && pnpm build)
go run ./scripts/release -version <版>           # dist/ に tar.gz
PREPARE_DEV_E2E_BINARY=<展開した prepare-dev> go test ./internal/e2e   # 配布物そのものを検査
```

Lean のモデルを変えたら `model/bin/test` で操作列を照合し直す（照合したモデルの digest を Go のテストが確かめる）。このリポジトリの証拠は、`.prepare-dev/config.json` の設定を使って `prepare-dev verify run` で取る。
