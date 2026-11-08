# prepare-dev

曖昧なプロダクトの意図から、要求、設計、検査可能な仕様、実装、検証結果までをつなぐための仕組み。既存のエージェントに skills と共通の MCP／CLI を与え、一つのローカル runtime が記録、人間への質問、検証ジョブを管理する。本体は TypeScript／Node.js、判断用 UI は React＋json-render。支援するプロダクトの言語は固定せず、形式検証の言語とツールはプラグインで差し替える。互換性の確認先は Codex CLI、次に Claude Code。

- [引き継ぎ記録（2026-11-08）](docs/records/2026-11-08-handoff.md): 決定済みの方針と経緯
- [具体化案（2026-11-08）](docs/2026-11-08-concretization-proposal.md): 候補と推奨、データ形式と API、マイルストーン1の完了条件
- [マイルストーン1の検証記録](docs/m1-verification.md): 完了条件13項目をどう確かめたか

## 構成

| 場所 | 内容 |
| - | - |
| `packages/core` | zod スキーマ（記録、質問、検証記録、journal）、ID、ハッシュ、frontmatter、プラグイン境界 |
| `packages/runtime` | 単一書き込みの store、質問と回答、`question_apply` の確定取引と回復、検証ジョブ、trace、status、HTTP＋SSE |
| `packages/mcp` | stdio の MCP 入口（デーモンの HTTP クライアント。デーモンがなければ起動する） |
| `packages/cli` | `prepare-dev` CLI（`serve`、`init`、`mcp`、`status`、`q`、`record`、`verify`、`trace`、`ui`） |
| `packages/ui` | 判断用 UI（React＋json-render、単一 HTML にビルド） |
| `plugins/lean4`, `plugins/vitest` | 検証プラグイン（runner と parser、推移的な依存の記録） |
| `model/` | SPEC-0001 の状態機械の Lean 4 モデル、不変条件の証明、適合テスト用 trace 生成 |
| `skills/` | エージェント向け skills の正本（対象リポジトリの `.agents/skills/` へリンク） |
| `prepare/` | このプロジェクト自身の記録（要求、仕様、判断、質問、検証実行、journal） |

## 使い方

```bash
pnpm install
pnpm ui:build                   # 判断用 UI を packages/ui/dist/index.html にビルド（Git 管理外）
pnpm test                       # runtime / MCP / 適合テスト
pnpm cli init --root <対象リポジトリ>   # prepare/ と MCP 登録（.mcp.json, .codex/config.toml）を用意
pnpm cli serve --root <対象リポジトリ>  # デーモン。UI は表示される URL
pnpm cli status --root <対象リポジトリ>
```

Codex CLI と Claude Code は `init` が書いた設定で MCP サーバ `prepare-dev` に接続する。skills は `skills/prepare-dev` を対象リポジトリの `.agents/skills/prepare-dev` にリンクする（Claude Code 向けに `.claude/skills -> ../.agents/skills`）。

強制終了からの回復を実プロセスで確かめる: `scripts/e2e-crash.sh`。
