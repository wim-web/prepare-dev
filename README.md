# prepare-dev

曖昧なプロダクトの意図から、要求、設計、検査可能な仕様、実装、検証結果までをつなぐための仕組み。既存のエージェントが skills から CLI を使い、一つのローカル runtime が記録、人間への質問、検証ジョブを管理する。本体は TypeScript／Node.js、判断用 UI は React。支援するプロダクトの言語は固定せず、形式検証の言語とツールはプラグインで差し替える。互換性の確認先は Codex CLI、次に Claude Code。

現行方針は [CLI 配布・MCP 登録を必須にしない（DEC-0004）](prepare/decisions/DEC-0004.md) と [json-render を採用しない（DEC-0005）](prepare/decisions/DEC-0005.md)。UI は質問の背景・候補の比較・推奨理由を React で表示する。[変更後の契約と受入条件（DES-0002）](prepare/designs/DES-0002.md) を実装の基準にする。UIとCLI配布物を実装した。配布構成は [DES-0003](prepare/designs/DES-0003.md)、受入仕様は [SPEC-0004](prepare/specs/SPEC-0004.md)、検証結果は [配布検証記録](docs/2026-11-09-release-verification.md) を参照。

- [引き継ぎ記録（2026-11-08）](docs/records/2026-11-08-handoff.md): 決定済みの方針と経緯
- [具体化案（2026-11-08）](docs/2026-11-08-concretization-proposal.md): 候補と推奨、データ形式と API、マイルストーン1の完了条件
- [マイルストーン1の検証記録](docs/m1-verification.md): 完了条件13項目をどう確かめたか
- [マイルストーン2の検証記録](docs/m2-verification.md): 上流の skills とレビューを具体例一件で通した結果と、そこで直したこと

## 完了範囲

M2までの完了条件8項目と、React UI・CLI配布物の実用検証を確認した。[M2の完了確認](docs/m2-verification.md)に条件ごとの証拠、旧案との置換関係、確認範囲をまとめている。M3は今回の完了判定に含めない。

## 構成

2026-11-09 に仕様齟齬6件を修正した。[修正内容と検証結果](docs/2026-11-09-consistency-verification.md) に、形式検証・回帰テスト・ブラウザでの再接続確認と、保証の範囲を記録している。

| 場所 | 内容 |
| - | - |
| `packages/core` | zod スキーマ（記録、質問、検証記録、journal）、ID、ハッシュ、frontmatter、プラグイン境界 |
| `packages/runtime` | 単一書き込みの store、質問と回答、`question_apply` の確定取引と回復、検証ジョブ、trace、status、HTTP＋SSE |
| `packages/mcp` | stdio の MCP 入口（デーモンの HTTP クライアント。デーモンがなければ起動する） |
| `packages/cli` | `prepare-dev` CLI（`serve`、`init`、`mcp`、`status`、`q`、`record`、`verify`、`trace`、`ui`） |
| `packages/ui` | 判断用 UI（React＋Markdown、単一 HTML にビルド） |
| `plugins/lean4`, `plugins/vitest` | 検証プラグイン（runner と parser、推移的な依存の記録） |
| `model/` | SPEC-0001 の状態機械の Lean 4 モデル、不変条件の証明、適合テスト用 trace 生成 |
| `skills/` | エージェント向け skills の正本（対象プロジェクトの `.agents/skills/` と `.claude/skills/` へコピー）。入口 `prepare-dev` と工程ごとの `prepare-discover`、`prepare-requirements`、`prepare-design`、`prepare-specify`、`prepare-verify`、`prepare-review`。レビューの persona と評価基準は EGM から版を固定して取り込む（`skills/prepare-review/references/egm/`） |
| `prepare/` | このプロジェクト自身の記録（要求、仕様、判断、質問、検証実行、journal） |

## 使い方

Node.js 26以上が必要。配布tarballにはCLI、UI、skills、検証器の接続処理と案内を含む。Vitest・Lean自体は対象プロジェクトに用意する。

```bash
npm install -g ./prepare-dev-0.1.0.tgz
cd your-project
prepare-dev init
prepare-dev ui
prepare-dev status --json
prepare-dev stop
```

`init` はskillsをコピーし、MCP設定は変更しない。既存のskillsに変更があれば上書きせず報告する。エージェントに `prepare-dev` skillの利用を依頼すると、CLIで質問・回答・判断・仕様・検証記録を扱う。MCPは任意。詳しいコマンドと更新方法は [配布版の使い方](docs/distribution-readme.md)。

## 開発・梱包

```bash
pnpm install
pnpm typecheck
pnpm test
pnpm test:package              # UI＋CLIを梱包し、別フォルダで配布物を検証
pnpm cli init --root <対象プロジェクト>
pnpm cli status --root <対象プロジェクト>
```

`pnpm run package` は `dist/prepare-dev-0.1.0.tgz` とSHA-256を含む `dist/artifact.json` を生成する。ソースcheckout・tsx・workspace依存を必要としない。`prepare-dev stop` 後に配布物を更新する。

強制終了からの回復を実プロセスで確かめる: `scripts/e2e-crash.sh`。
