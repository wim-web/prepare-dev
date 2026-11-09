# prepare-dev

曖昧なプロダクトの意図から、要求、設計、検査可能な仕様、実装、検証結果までをつなぐための仕組み。既存のエージェントが skills から CLI を使い、プロジェクトごとに一つのローカルのデーモンが記録、人への質問、検証ジョブを管理する。本体は Go の実行ファイル一つで、React で作った判断画面のビルド、skills、検証プラグインの案内を埋め込む。支援するプロダクトの言語は固定せず、形式検証の言語とツールはプラグインで差し替える。互換性の確認先は Codex CLI、次に Claude Code。

現行方針は [CLI として配布する（DEC-0004）](prepare/decisions/DEC-0004.md)、[判断画面を React で作る（DEC-0005）](prepare/decisions/DEC-0005.md)、[本体を Go で作り直す（DEC-0006）](prepare/decisions/DEC-0006.md)、[MCP の入口をなくす（DEC-0007）](prepare/decisions/DEC-0007.md)。構成は [DES-0004](prepare/designs/DES-0004.md)、画面の契約は [DES-0002](prepare/designs/DES-0002.md)、受入仕様は [SPEC-0001](prepare/specs/SPEC-0001.md)（回答の適用と回復）・[SPEC-0003](prepare/specs/SPEC-0003.md)（検証入力の追跡）・[SPEC-0004](prepare/specs/SPEC-0004.md)（判断画面と配布物）。

- [引き継ぎ記録（2026-11-08）](docs/records/2026-11-08-handoff.md): 決定済みの方針と経緯
- [具体化案（2026-11-08）](docs/2026-11-08-concretization-proposal.md): 候補と推奨、データ形式と API、マイルストーン1の完了条件
- [マイルストーン1の検証記録](docs/m1-verification.md)、[マイルストーン2の検証記録](docs/m2-verification.md): TypeScript 版での完了確認
- [Go 版の検証記録](docs/2026-11-09-go-verification.md): Go への移行で何をどう確かめたか

## 構成

| 場所 | 内容 |
| - | - |
| `cmd/prepare-dev` | 実行ファイルの入口 |
| `internal/core` | 記録・質問・検証記録・ジャーナルの型、frontmatter と YAML、ID、ハッシュ |
| `internal/store` | 記録（Markdown）、質問と RUN（JSON）、ジャーナル、原子的な書き込み |
| `internal/runtime` | 質問と回答、適用の確定と回復、検証ジョブ、trace、status、レビュー資料 |
| `internal/server` | HTTP API、SSE、判断画面の配信 |
| `internal/daemon` | デーモンの起動・識別・停止、lease、API クライアント |
| `internal/cli` | コマンドと引数 |
| `internal/skills` | skills のコピーと更新 |
| `internal/plugin` | 検証プラグイン（`lean4`、`vitest`、`gotest`） |
| `internal/uibuild` | 埋め込む画面のビルドがソースと一致するかの検査 |
| `internal/e2e` | 実行ファイルを別の場所に置き、Node.js と Go を PATH から外して動かすテスト |
| `assets.go` | 判断画面・skills・guidance の埋め込み |
| `ui/` | 判断画面（React）。ビルド結果 `ui/build/` を Git に入れる（[ui/README.md](ui/README.md)） |
| `guidance/` | 検証プラグインの書き方（`prepare-dev guidance <name>` で表示） |
| `skills/` | エージェント向け skills の正本（対象プロジェクトの `.agents/skills/` と `.claude/skills/` へコピー）。レビューの persona と評価基準は EGM から版を固定して取り込む（`skills/prepare-review/references/egm/`） |
| `model/` | SPEC-0001 の状態機械の Lean 4 モデル、不変条件の証明、適合テスト用 trace の生成 |
| `scripts/release` | macOS・Linux 向けの実行ファイルと tar.gz、`dist/artifact.json` の作成 |
| `prepare/` | このプロジェクト自身の記録（要求、仕様、判断、質問、検証実行、ジャーナル） |

## 使い方

配布物の tar.gz から実行ファイルを PATH の通った場所に置く。Node.js や Go は要らない。Lean・Vitest・Go などの検証器は、検証する環境に別途用意する。

```bash
cd your-project
prepare-dev init
prepare-dev ui
prepare-dev status
prepare-dev stop
```

`init` は skills をコピーし、MCP の設定は作らない。エージェントに `prepare-dev` skill の利用を依頼すると、CLI で質問・判断・仕様・検証の結果を扱う。人は判断画面で回答する。記録は一つの作業コピーでだけ書く（ブランチや worktree で並行して書いた `prepare/` はマージで衝突するので、そのときに直す。DEC-0008）。詳しいコマンド、ブランチと worktree の扱い、更新方法は [配布版の使い方](docs/distribution-readme.md)。

## 開発

Go 1.27 以上。判断画面を直すときだけ Node.js 26 以上と pnpm を使う。

```bash
go test ./...                                   # 単体・適合・配布物のテスト
go run ./cmd/prepare-dev status --root <対象>    # checkout から動かす
(cd model && bin/test)                          # Lean の証明、公理の監査、trace がモデルと一致するか
(cd ui && pnpm install && pnpm test && pnpm build)
go run ./scripts/release -version 0.2.0         # dist/ に tar.gz と artifact.json
PREPARE_DEV_E2E_BINARY=<展開した prepare-dev> go test ./internal/e2e   # 配布物そのものを検査
```

このリポジトリの記録は `.prepare-dev/config.json` の設定で検証する。`gotest` は、テストが実行中にビルドする `./cmd/prepare-dev` と、読むファイル（`model/**`、`ui/**`）を入力に含め、C4 は depth-6 の操作列を流して、流した本数と検査した実行ファイルを RUN の `scope.bounds` に残す。Lean のモデルを変えたら `model/bin/test` で操作列を照合し直す（照合したモデルの digest を `model/traces/model.sha256` に残し、Go のテストが確かめる）。

## TypeScript 版からの切り替え

記録・質問・RUN・ジャーナルの形式は変えていない（TypeScript 版が書いたファイルの写し `internal/core/testdata/ts-342bc64` で確かめている）。

1. 動いている TypeScript 版のデーモンを止める: `prepare-dev stop --root <対象>`（Go 版の `stop` も同じ停止 API を使う）。
2. エージェントの MCP 設定（`.mcp.json`、`.codex/config.toml` など）から `prepare-dev mcp` の登録を消す。
3. `prepare-dev init --replace-skills` で、TypeScript 版が入れた skills を新しい版に置き換える（元のものは `.prepare-dev/skills-backup/` に残る）。

戻すときは Go 版のデーモンを `prepare-dev stop` で止め、commit 342bc64 の checkout で `pnpm install` と `pnpm ui:build` を実行してから TypeScript 版（`pnpm cli`）を動かす（画面のビルドが無いと TypeScript 版のデーモンは起動しない）。TypeScript 版は Go 版が書いた記録・質問・RUN・ジャーナルをそのまま読み、書き換えない。Go 版で増えたもの（`gotest` の RUN、`.prepare-dev/skills.json`）は TypeScript 版では使われない。
