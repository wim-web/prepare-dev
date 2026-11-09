---
name: prepare-design
description: Use after requirements to compare design alternatives (UX, responsibilities, architecture, data, integrations), choose with the human, and record a DES design record plus DEC decisions through prepare-dev. Includes reconciling the design against earlier DIS/REQ/DEC decisions before finalizing.
---

# Design（設計）

目的は、要求を満たす設計案を比較し、採否と理由を残すこと。成果物は `DES-*`（kind `design`）と、案の選択や受け入れたトレードオフの `DEC-*`。実装の順序と検証の方法は `DES-*` の節として書き、仕様（`SPEC-*`）とは分ける。

## 入力

- `REQ-*` と `DEC-*`、`DIS-*`（`trace REQ-xxxx`）。
- 既存コードの構造。再利用できる部品、守るべき境界。

## 本文の構成

EGM の設計書テンプレート（この SKILL.md から相対パス `../prepare-review/references/egm/templates/design_doc.md`）を基にする。節は次の順。

1. **概要と問題**: 一段落。`DIS-*` と `REQ-*` の参照。
2. **目標と非目標**: `REQ-*` の id で書く。
3. **未決事項**: 人の判断待ちのもの。質問の id を付ける。
4. **利用シナリオ（CUJ）**: Given / When / Then。`REQ-*` の受入条件と対応づける。
5. **技術計画**: 構成、責務の分割、データの流れ、外部連携、状態の置き場所。図を使ってよい。
6. **検討した代替案**: 案ごとに仕組み、利点、欠点、どの条件で破綻するか、棄却理由。棄却理由は技術的・範囲的な根拠を書き、「〜が言ったから」にしない。
7. **非機能要求**: 安全性、性能、互換性、移行と切り戻し。該当しなければ「なし」とその理由。
8. **実装計画**: 変更するファイル、順序、各段階で動く状態を保つこと。
9. **検証計画**: どの要求をどの手段（証明、モデル検査、有界検査、テスト）で確かめるか。`prepare-specify` の入力になる。
10. **前段との照合**: 下記。

## 手順

1. 設計上の決定点を列挙する。複数の妥当な案がある決定（データモデル、責務の境界、保存方式、プロトコル、外部依存）と、一つに決まる機械的な選択（命名、整形）を分ける。機械的な選択は自分で決める。
2. 複数案がある決定ごとに、最大三つの案を仕組み・利点・欠点・破綻条件つきで比べ、推奨と理由を書く。既存コードの再利用を先に探す。
3. `record_put` で `DES-*` を書く。frontmatter: `alternatives`（`key`、`title`、`status`、`reason`）、`chosen`、`tradeoffs`、`new_decisions`、`coverage`、`links.derived_from`（`REQ-*`）。前段の質問が未回答なら、推奨案を前提にして進め、その前提を「未決事項」に書く。
4. **前段との照合**（`prepare-dev` の共通規則）: `DIS-*`、`REQ-*`、`DEC-*` を読み直し、矛盾、新しい決定、棄却案の引き継ぎを「前段との照合」の節に書く。矛盾は自分で直さず質問にする。
5. `prepare-review` で zoom_out → comprehension → design_critique の順にレビューし、指摘を処理する。確定した `DES-*` を変えるときは、`prepare-review` の「レビューを回す場面」に従う（決まりを変えない修正には回さない）。
6. 判断が要る決定を、決定ごとに `question_create` する（同時に複数作ってよい）。`subjects` は `DES-*` と関係する `REQ-*`。選択肢は案の `key`、推奨に `reason`。作ったあとは回答まで `DES-*` を変えないので、レビューの修正を済ませてから作る。
7. 回答を `question_apply` で `DEC-*` にし、`DES-*` の `alternatives[].status` と `chosen` を更新してから `SPEC-*` へ進む。回答を待たずに進むなら、推奨案を前提にした `SPEC-*` であることを `SPEC-*` の `assumptions` に書く。

## 終了条件

- 複数案のある決定がすべて、案の比較と人の判断（`DEC-*`）か、質問の登録のどちらかになっている。
- `new_decisions` が空か、各項目が報告済みである。
- 検証計画が、どの要求をどの手段で確かめるかを述べている。
- レビューの blocker が未対処で残っていない（`status` の `open_findings`。`prepare-review` の手順 5 で一覧に残した、今回変えていない箇所への指摘は除く）。

設計レビューで指摘に対応したあと、もう一度「そもそも必要か、やりすぎていないか」（zoom_out）を見る。細部を直すうちに全体が膨らむことが多い。
