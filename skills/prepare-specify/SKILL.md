---
name: prepare-specify
description: Use after design to turn requirements into checkable claims (contracts, state models, invariants, formal specs, test conditions) with explicit assumptions and out-of-scope, recorded as SPEC records whose claims carry native references for the verification plugins of prepare-dev.
---

# Specify（仕様）

目的は、要求を「何を、どの対象に、どの条件で、どの方法で確かめるか」が決まった主張に変えること。成果物は `SPEC-*`（kind `spec`）。実装方式の細部は書かない。それは `DES-*` の実装計画にある。

## 入力

- `REQ-*`（受入条件）、`DES-*`（検証計画、状態の置き場所）、`DEC-*`。
- 使うプラグインの書き方: `prepare-dev guidance lean4` または `prepare-dev guidance vitest`。配布物に同梱した文書から `verify_run` に渡す参照の形式を確認する。

## 主張の種類

要求ごとに、どの表現が適切かを選ぶ。一つの要求に複数の主張があってよい。

| 種類 | 向いている要求 | 得られる保証 |
| - | - | - |
| 証明（proof） | 任意長の操作列に対する不変条件、状態機械の安全性 | 明示した前提と公理のもとでの証明。モデルと実装の対応は別の主張 |
| モデル検査・有界検査 | 有限状態の相互作用、並行性、境界内の探索 | 検査したモデルと境界内での反例なし |
| テスト（test） | 実装の振る舞い、適合、回帰 | 実行したケースの範囲 |
| 静的検査 | 型、契約、禁止パターン | 検査器が扱う範囲 |

モデルを検証する主張と、実装がモデルに従うことの主張（適合）は分けて書く。証明とテストを並べても、実装全体を証明したことにはならない。

## 手順

1. `REQ-*` の受入条件ごとに、主張の文（statement）を書く。曖昧な語（適切に、正しく、速やかに）を使わない。何が観測できれば満たしたといえるかを書く。
2. 各主張に、使うプラグインと native 参照を付ける（Lean の定理名、テストファイルとテスト名など）。参照先がまだ存在しなければ、名前だけ決めて `prepare-verify` で作る。
3. 前提（`assumptions`）と対象外（`out_of_scope`）を列挙する。モデルの初期状態、許可する遷移、扱わない故障を含める。
4. 状態機械や不変条件は本文に明示する。表や箇条書きでよい。
5. `record_put` で `SPEC-*` を書く。frontmatter: `claims`（`id`、`statement`、`requirement`、`native: [{plugin, ref}]`）、`assumptions`、`out_of_scope`、`links.specifies`（`REQ-*`）。
6. **前段との照合**: `DES-*` の検証計画と `REQ-*` の受入条件をすべて主張が覆っているか、`DEC-*` に反する主張がないかを確認し、本文に書く。主張を書くうちに設計の欠陥（設計どおりに作ると主張が偽になる、設計の前提が誤っている）を見つけたら、`DES-*` は直さない（未回答の質問の対象なら回答が `stale` になる）。推奨する直し方を前提にして `SPEC-*` を書き、その前提を `assumptions` に、`DES-*` に書き戻す内容を本文の「`DES-*` に反映すべき事項」に残し、直し方を候補つきの質問にする。設計の場合分けをそのまま主張に写すと、設計の誤った前提も主張に写る。並行性や中断が関わる手順は、場合分けのテストに加えて状態の探索で確かめられないかを検討する。
7. `prepare-review` で comprehension、design_critique、implementation_readiness のレビューを受け、指摘を処理する。
8. 判断が要る項目（証明とテストのどちらにするか、検査の境界、対象外にする条件）を、独立した判断ごとに `question_create` する。作ったあとは回答まで `SPEC-*` を変えない。

## 終了条件

- すべての受入条件に少なくとも一つの主張が対応し、各主張にプラグインと native 参照がある。
- `assumptions` と `out_of_scope` が書かれ、主張の保証の範囲が読める。
- 主張の文が、検証記録の `result` だけで真偽を判定できる形になっている。

主張は定理名やテスト名だけでは不十分で、仮定を含む完全な命題が証拠に残る。定理に結論を仮定として置けば公理なしで証明できるので、レビューでは命題と要求の対応を見る。
