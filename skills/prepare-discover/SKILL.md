---
name: prepare-discover
description: Use at the start of a product or feature, before requirements, to establish whose problem is being solved, the current situation, alternatives, hypotheses and non-goals, and to record them as a DIS record through prepare-dev. Also use when a later stage reveals that the problem itself was unclear.
---

# Discover（問題の整理）

目的は「誰の、どんな問題を、なぜ今解くのか」を、仕様の前に根拠付きで確定すること。成果物は `DIS-*`（kind `discovery`）一件。要求はまだ書かない。

## 入力

- 依頼の文面、既存の README・設計文書・issue・コード、利用者の声や計測があればそれも。
- 既存の `DIS-*`、`REQ-*`、`DEC-*`（`record_list`）。同じ問題を扱う記録があれば、新規作成ではなく更新する。

## 観点

次の観点を調べ、`coverage` に状態を付ける。空欄のままにしない。

| 観点 | 内容 |
| - | - |
| `problem` | 誰の、どの場面の、どんな困りごとか。症状ではなく根本の問題。専門用語なしで一文で言えるか |
| `current` | 今はどうしているか。既存の手段とその限界 |
| `stakeholders` | 利用者、運用者、影響を受ける人。それぞれの利害 |
| `value` | 解けたら何が変わるか。誰が気にするか |
| `alternatives` | 作らずに済む方法はないか。既存のツール、設定、運用の変更 |
| `constraints` | 技術、期限、環境、互換性、法務などの制約 |
| `hypotheses` | 「この機能が必要だ」「利用者はこう使う」などの仮説と、それを支える証拠（調査、試作、反応）。証拠がなければ `hypothesis` のまま |
| `non_goals` | 今回やらないことと、その理由 |
| `risks` | 失敗したときに何が起きるか。やらない場合に何が起きるか |
| `success` | どうなれば成功か。中間と最終の確認方法 |

これは Heilmeier の質問群（この SKILL.md から `../prepare-review/references/egm/document_standards/heilmeier-standard.md`）を prepare-dev の記録に合わせたもの。

## 手順

1. 上の観点について、まず自分で調べて埋める。コードと既存文書を読む。推測した箇所は `hypothesis` と明記する。
2. 候補が複数ある観点（問題の捉え方、非目標の線引き、優先する利用者）は、候補と推奨理由を作る。
3. 粗さを減らしたいときは、`prepare-review` の手順で BRAINSTORM モードのレビュー役（zoom_out、design_critic、implementation_readiness、comprehension）に `DIS-*` の草稿を読ませ、不要な複雑さ、既存の手段、移行の危険、使い勝手の問題を指摘させる。指摘は `REV-*` に残す。
4. `record_put` で `DIS-*` を書く。frontmatter: `problem`、`stakeholders`、`hypotheses`（`id`、`statement`、`evidence`、`state`）、`non_goals`、`coverage`。本文に観点ごとの節と、用語の定義を書く。終了条件を満たしたら `status: proposed`。
5. レビュー（手順 3）の指摘を処理し終えてから、人の判断が要る項目を、独立した判断ごとに `question_create` する（同時に複数作ってよい）。対象（`subjects`）は `DIS-*`。典型的には: 問題の捉え方の選択、非目標の確定、優先する利用者、受け入れる制約。作ったあとは回答まで `DIS-*` を変えない（`prepare-dev` の「質問の流れ」）。
6. 回答を `question_apply` で `DEC-*` にし、`DIS-*` の `coverage` を `decided` に更新する。

## 終了条件

- `problem` が専門用語なしの一文で書かれ、`stakeholders` と `value` が具体的である。
- `alternatives` に「作らない」選択肢の検討が含まれる。
- すべての観点に `coverage` の状態があり、`hypothesis` には証拠の有無が書かれている。
- 人に聞くべき項目は質問として登録済みで、残りは `decided` か `out_of_scope`。

人の回答を待つ間に Requirements へ進んでよいが、`DIS-*` の判断に依存する要求は `hypothesis` として扱う。
