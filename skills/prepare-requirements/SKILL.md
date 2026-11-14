---
name: prepare-requirements
description: Use with prepare-dev when the user states a new wish, when a behavior is ambiguous, or when a finding or a remark on a figure would change the meaning of what is built. Turns the wish into requirements, concrete examples (normal, exceptional, boundary) with expected results, acceptance conditions, open questions and guarantee requirements, and records which parts the user said, which the AI proposed, and which the user has decided.
---

# prepare-requirements（要件整理）

希望を、通常例・例外・境界例と期待結果、制約、未決事項、受入条件、保証要求にする。誰が言ったことか、採用されたかを、記録として残す。

先に `prepare-dev` の「共通の決まり」を読む。

## 起動する状況

- 新しい希望が出た。
- 振る舞いが曖昧。
- レビューの指摘や図への指摘が、意味を変える。

## 手順

先に見せて、決めてもらってから、記録する。`prepare-dev` の「いちばん大事な決まり」に従う。

1. **調べる。** `ppdev list` と `ppdev status` で、既にある要求・決定・未決事項を見る。関係するコードや資料も読む。
2. **希望を要求として残す。** 利用者の言葉を `--source` に入れる。
   `ppdev new REQ --title "<要求>" --origin user --source "発言「…」"`
3. **具体例を考える。** 通常、例外、境界のそれぞれについて、「こうすると、こうなる」を考える。成功してほしい例と、拒否すべき例の両方を入れる。まだ記録しない。
4. **HTML のページ一枚で見せる。** 作るものの一文、具体例を三つから五つ、画面の絵や流れの図を、`prepare-show` の「見せるのは、HTML のページ」に従ってページにし、ブラウザで開く。利用者に決めてほしい点があれば、ページに案を並べ、チャットでまとめて一回で聞く。差が出る具体例、選べる案、推奨と理由を添える。
5. **返事を待つ。** 待つ間に進めてよいのは、調べることだけ。作ることは進めない。
   聞いたことは、見せたページ（`.ppdev/pages/`）に残っている。返事が来る前に、未決事項として記録に積まない。
6. **決まったことを記録する。** 利用者がよいと言った具体例と、決めたことを残す。
   `ppdev new EX --title "<例>" --body "<こうすると、こうなる>" --origin user --source "発言「…」" --link example-of:<REQ>`
   `ppdev decide --title "<何を決めたか>" --source "発言「…」" --resolve <Q の番号>`
   利用者が述べた例は `--origin user`。AI が出した例を利用者が認めたときは、`--origin ai` で作り、`ppdev decide ... --adopt <番号@版>` で採用にする。
   具体的に示した複数の提案は、一度にまとめて決めてもらえる。示していない将来の提案まで承認されたことにしない。
7. **受入条件を残す。** 「依頼した変更ができた」と言うために要る動き・制約・確認事項。決まった具体例から作る。
   `ppdev new ACC --title "<条件>" --link accepts:<REQ> --link uses:<EX>`
8. **保証要求は、利用者が形式証明や決まった確かめ方を求めたときだけ作る。**（下の「保証要求の項目」）

## 保証要求の項目

`ppdev new GRT --title "<主張>" --fields-file <JSON> --link guarantees:<REQ>` で作る。JSON の項目：

| 項目 | 内容 |
|---|---|
| `necessity` | `required`（必須）／`auxiliary`（補助）。決まっていなければ入れない |
| `requires` | 必要な確認方法の一覧。`{"method": "theorem-proof", "target": "model"}`、`{"method": "conformance-test", "target": "implementation", "min_depth": 4}` など |
| `allowed_axioms` | 証明が頼ってよい公理。ここにない公理に頼る証明は、証拠にならない |
| `require_review` | 形式条件のレビュー完了を求めるか |
| `require_mutation` | わざと壊した変種で検証が落ちることの確認を求めるか |
| `premises` | 成り立つとして扱う前提（確かめない） |
| `out_of_scope` | 対象外 |

`method` は `theorem-proof`（形式証明）、`model-check`（モデル検査）、`conformance-test`（モデルとコードの突き合わせ）、`test`（実行テスト）。`target` は `model`（動作ルール）か `implementation`（コード）。

保証要求は AI の提案として作り、利用者が採用する。採用するまで `ppdev guarantee check` は「未決」を返し、その主張を満たしたとは記録されない。

**利用者に毎回同じ判断を求めない。** 保証の方針（例：「ルールを証明し、コードはテストで確かめる」「認める公理は Lean の標準のものだけ」）は、差が出る具体例を添えて一度決めてもらう。その後の主張は、その決定から導出して採用する。

`ppdev derive --from <方針の決定> --title "<主張> の保証" --body "<方針のどこから導いたか>" --adopt <GRT@版>`

方針と違う点がある主張だけ、利用者に確認する。主張そのものと具体例は、主張ごとに確かめる。

## 守ること

- 得られた証拠に合わせて、保証要求を後から弱めない。弱める必要があれば、保証範囲の変更として提案し、利用者の決定に結び付ける。
- 別の会話や別のプロジェクトの技術選択を、明示されないまま持ち込まない。
- 版を重ねるときは `ppdev revise <番号> --base <元にした版>`。衝突（終了コード 3）したら、最新の版を読み直してからやり直す。
- 採用済みの要求を AI が書き換えた版は、提案として残る。利用者が採用するまで、採用済みとして扱わない。
