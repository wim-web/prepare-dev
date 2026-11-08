---
name: prepare-requirements
description: Use after discovery to turn the problem into requirements with rationale, priorities and acceptance criteria, recorded as REQ records through prepare-dev. Covers users and stakeholders, normal and exceptional scenarios, terms, data, states, rules, quality requirements and constraints.
---

# Requirements（要求）

目的は、`DIS-*` の問題から、検査できる受入条件を持つ要求を根拠付きで作ること。成果物は `REQ-*`（kind `requirement`）を要求ごとに一件と、優先順位やトレードオフの判断 `DEC-*`。設計の決定はまだしない。

## 入力

- `DIS-*` と、それに対する `DEC-*`（`trace DIS-xxxx`）。
- 既存の `REQ-*`。重複や矛盾がないか先に確認する。
- 既存コードの振る舞い（現状の制約になる）。

## 観点

次の観点は要求の集合として調べる。記録は要求ごとなので、`coverage` は各 `REQ-*` にその要求について付ける（`users` ならその要求が誰のものか決まっているか、`quality` ならその要求の品質目標が決まっているか、`priority` なら優先度が人の判断で確定しているか）。その要求に関係しない観点は `out_of_scope` と理由を書く。集合としての漏れ（利用者ごとの要求があるか、例外が通常と同数か、用語が一致するか、矛盾がないか）は手順 3 で確かめ、漏れは新しい `REQ-*` にするか質問にする。観点の key を独自に足さない。

| 観点 | 内容 |
| - | - |
| `users` | 利用者と関係者ごとの要求。誰にとっての要求かを明記 |
| `scenarios_normal` | 通常の利用シナリオ。Given / When / Then で書く |
| `scenarios_exception` | 例外、失敗、中断、競合、再試行のシナリオ |
| `terms` | 用語と定義。`DIS-*` の用語と一致させる |
| `data` | 扱うデータ、識別子、版、保持期間 |
| `states` | 状態と遷移。禁止される遷移 |
| `rules` | 業務規則、不変条件 |
| `quality` | 性能、信頼性、保守性、安全性などの品質要求と、その測り方 |
| `constraints` | 技術、互換性、環境、法務の制約 |
| `priority` | must / should / could / wont とその根拠 |
| `acceptance` | 各要求の受入条件（`AC-n`）。何をどう確かめれば満たしたといえるか |
| `rationale` | 各要求が必要な理由。`DIS-*` の仮説や証拠への対応 |

## 手順

1. `DIS-*` の問題と仮説から要求の候補を列挙する。各候補に「なぜ必要か」を一行で書けないものは要求にしない。
2. シナリオを Given / When / Then で書き、例外シナリオを通常と同じ数だけ検討する。中断と再試行のシナリオは必ず含める。
3. 用語、データ、状態、規則を整理し、矛盾する要求を見つけたら候補と推奨を作る。
4. `record_put` で `REQ-*` を要求ごとに作る。frontmatter: `priority`、`acceptance`（`AC-n` の一覧）、`coverage`、`links.derived_from`（`DIS-*`）。本文: 要求、根拠、受入条件、対象外。終了条件を満たしたら `status: proposed`。
5. `prepare-review` で comprehension と design_critique のレビューを受け、指摘を処理する。
6. 人の判断が要る項目を、独立した判断ごとに `question_create` する（同時に複数作ってよい）。`subjects` は関係する `REQ-*`（複数可）。典型的には: 優先順位、相反する要求のトレードオフ、品質要求の目標値、対象外にする範囲。作ったあとは回答まで対象の `REQ-*` を変えない。
7. 回答を `question_apply` で `DEC-*` にし、`REQ-*` の `priority` や `coverage` を更新する。

## 終了条件

- すべての `REQ-*` に根拠と受入条件があり、受入条件は検査方法が想像できる具体さである。
- 例外シナリオが通常シナリオと同程度に書かれている。
- 用語が `DIS-*` と一致し、要求同士の矛盾が解消されているか、判断として質問に登録されている。
- 観点ごとの `coverage` が埋まっている。

人間と AI が合意しただけでは、現実に必要とされる要求だと確かめたことにはならない。重要な要求には `DIS-*` の仮説と証拠を対応づけ、証拠がない要求は `hypothesis` のまま次へ渡す。
