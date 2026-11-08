# 取り込み元

- リポジトリ: https://github.com/google/elephant-goldfish-model
- 版: tag `v0.1.1`、commit `dea7761de06ce5b51c177ebb9bba0469f24af5a4`（2026-11-06）
- ライセンス: Apache-2.0（同梱の `LICENSE`）
- 取り込んだもの: `agents/`（reviewer persona 9件）、`resources/document_standards/`（評価基準 5件）、`skills/egm-draft/references/design_doc.md`（設計書テンプレート）
- 取り込んでいないもの: brainstorm / draft / review / review-loop / resolve の SKILL 本文（対話規則が prepare-dev の方針と合わないため、手順は `skills/prepare-*/SKILL.md` に書き直した）

ファイルは無改変で置く。prepare-dev 側の差分は各 skill の本文と `wrappers/` に書く。上流の更新は commit を比較して取り込む。
