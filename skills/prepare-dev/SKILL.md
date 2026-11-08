---
name: prepare-dev
description: Use when working with prepare-dev on a product's intent, requirements, decisions, design, specs, review, or verification evidence. Use its CLI from the skill; MCP is an optional transport and is not required. Entry point that picks the stage and defines the shared rules for asking the human and recording through the runtime.
---

# prepare-dev（入口）

skill から `prepare-dev` CLI を実行し、共通の runtime を通して問題の整理から要求、設計、検査可能な仕様、実装、検証結果までを `prepare/` の記録につなぐ。MCP 登録は不要。MCP が利用できる場合は同じ操作を呼ぶ追加経路として使える。この skill は入口で、工程の選択と共通規則を決める。工程ごとの手順は `prepare-discover`、`prepare-requirements`、`prepare-design`、`prepare-specify`、`prepare-verify`、`prepare-review` にある。

本文と工程の skills にある `record_get`、`question_create`、`verify_run` などは runtime の操作名。CLI では下の対応を使う。

## 最初にすること

1. `status` を呼ぶ。未処理の回答（`pending`）、未対処のレビュー指摘（`open_findings`）、現在版と不一致の判断・証拠があれば、新しい作業より先にそれを扱う。前のセッションで受け取り損ねた回答はここに残る。回答待ちの質問（`waiting_questions`）は人の判断を待っているもので、その URL を人に伝え直す。`subjects_current` が false の質問は、回答しても `stale` になるので、対象の現在版を見て質問し直すか `question_close` する。
2. `record_list` で既存の記録を見て、どの工程まで進んでいるかを把握する。工程は次の順だが、後の工程で問題が見つかれば前へ戻る。

| 工程 | 記録 | skill |
| - | - | - |
| 問題の整理 | `DIS-*` | `prepare-discover` |
| 要求 | `REQ-*`（判断は `DEC-*`） | `prepare-requirements` |
| 設計 | `DES-*`、`DEC-*` | `prepare-design` |
| 仕様 | `SPEC-*` | `prepare-specify` |
| 実装と検証 | コード、証明、テスト、`RUN-*` | `prepare-verify` |
| レビュー | `REV-*` | `prepare-review`（各工程の終わりと、戻るときに使う） |

3. 一つの依頼ですべての工程を通す必要はない。依頼の性質に合わせて適用範囲を選び、選ばなかった工程と理由を記録に書く。

## 共通規則

- **人に聞く前に調べる。** 既存資料とコードを読み、候補、比較、推奨とその理由を先に作る。人に確認するのは、目的、優先順位、価値判断、許容するトレードオフ、調査では得られない事実だけ。質問だけを並べて思考を人に移さない。
- **判断は工程の終わりに、独立した判断ごとに聞く。** 工程の途中で節ごとに承認を求めない。工程の終わりに、決める必要のある項目を候補付きで `question_create` する。一つの質問は一つの判断（一つの `kind` と一組の `options`）で、回答一つが `DEC-*` 一件になる。独立した判断が三つあれば質問を三つ同時に作る。選択肢の key に接頭辞を付けて複数の判断を一つの質問に詰めない。推奨には `recommended: true` と `reason` を付ける。
- **観点を空欄のまま完了にしない。** 各工程の観点は `coverage` に `decided` / `hypothesis` / `unverified` / `out_of_scope` のいずれかで記入する。該当しないなら `out_of_scope` と理由を書く。
- **記録は runtime 経由で書く。** `record_put`（作成は id なし、更新は `record_get` で得た `expected_rev` と `expected_sha256` 付き）。回答に由来する判断は `question_apply` だけが作る。`status` は、書いている途中は `draft`、工程の終了条件を満たして人の判断待ちになったら `proposed`、判断が反映されたら `accepted`、置き換えられたら `superseded`。
- **記録同士は `links` でつなぐ。** `derived_from`（`REQ-*` → `DIS-*`、`DES-*` → `REQ-*`）、`specifies`（`SPEC-*` → `REQ-*`）、`rationale_for`（`DEC-*` → 対象）。`trace` は `links` を両方向にたどる（`links` に返す）ので、質問の id を記録に書き戻す必要はない。質問は `subjects` から引ける。
- **理由を残す。** 採用した案だけでなく、棄却した案とその理由、判断の根拠を `DEC-*` に書く。前の工程で決まったことを後の工程で黙って変えない（下の「前段の決定との照合」）。
- **検証を通すために要求を黙って弱めない。** 要求の誤りを見つけたら、その要求を対象にした質問を作り、人の判断を `DEC-*` に残してから直す。
- **レビューは証拠ではない。** レビューの指摘が解決しても `RUN-*` の `result` は変わらない。証拠はツールの実出力からだけ作る。

## 質問の流れ

1. `question_create`: `subjects` に対象の記録 id、`options` に候補（`recommended`、`reason`）。返る `url` を人に伝える。
2. `question_wait`: `pending` が返ったら再度呼ぶ。待機時間は `timeout_ms` で調整する。
3. 回答を受け取ったら `question_ack`。受領の記録であって、処理済みではない。
4. 反映するなら `question_apply` に decision の本文（採用、代替案、理由）を渡す。`DEC-*` が作られる。反映しないなら `question_close` に理由を付ける。
5. `stale`（対象が質問後に変わった）と返った回答は自動では反映されない。対象の現在版を読み直し、必要なら質問し直す。

同じ `answer_id` で `question_apply` を再試行しても判断は増えない。

対象を直す必要が出て、まだ回答のない質問を作り直すときは、直した後に新しい版で `question_create` し、古い質問を `question_cancel`（`reason` と `replaced_by` に新しい質問の id）で取り消す。回答のある質問は取り消せないので、回答ごとに `question_apply` か `question_close` で処理する。

質問を作ったあとは、回答が反映されるまで `subjects` の記録を変えない。変えると版が進み、回答は `stale` になる。質問の id を記録の `links` に書き戻すのもこれに当たるので、しない（`trace` が `subjects` から質問を引く）。質問の前に直すことがあれば、直してから作る。

## 前段の決定との照合

設計や仕様を書き終えたら、`trace` と `record_get` で `DIS-*`、`REQ-*`、`DEC-*` を読み直し、次を確認して記録の本文に「前段との照合」として書く。記憶に頼らない（別セッションで実行されることがある）。

- **矛盾**: 前段で決めたことを、今の文書が別の判断に変えていないか。見つけたら全件報告する。自分では直さない。今の文書が正しいこともあるので、人に聞く。
- **新しい決定**: 前段になかった決定を今の文書が加えていないか。アーキテクチャ、外部インターフェース、依存関係に関わるものは報告する（`DES-*` の `new_decisions`）。それ以外の機械的な選択は自分で決めてよい。
- **棄却した案**: 前段で棄却した案とその理由が引き継がれているか。

## CLI の操作

質問は `q create --prompt-file question.md --options-file options.json --subject REQ-ID` で登録できる。本文には、平易な題、背景、何を決めるか、人の判断が必要な理由を書く。比較が役立つ場合は Markdown の表を使う。各選択肢は `key`・人が読む `label`・選ぶとどうなるかの `summary`・`recommended`・推奨理由の `reason` を持つ。記録の ID や内部用語を読まないと判断できない質問にしない。画面レイアウトの JSON は作らない。

`prepare-dev record create|get|put|list`、`q create|wait|pending|ack|apply|close|cancel`、`verify run|wait|get`、`trace`、`bundle`、`status` を使う。`record_list` は `record list`、`review_bundle` は `bundle` に対応する。`--root` に対象リポジトリを渡す。CLI が未導入の開発中は prepare-dev の checkout で `pnpm cli` を使える。MCP を有効にする必要はない。

- frontmatter は `--fm key=value`（値が `[` か `{` で始まれば JSON）か、`--fm-file <json>`（オブジェクトをまとめて渡す。`findings` や `claims` のような長い配列はこちら）。`status` も `--fm status=proposed` で設定する。
- 本文は `--body-file <path>`。`put` は本文を省けば現在の本文を保つ。
- `record put ID` には、編集前に `record get ID` で読んだ `--expected-rev N --expected-sha256 HASH` が必須。他者による変更を検出できるよう、更新直前に読み直して期待値を差し替えない。衝突したら変更内容を読み直して反映案を作り直す。
- 質問種別は `q create --kind single_choice|multi_choice|free_text|review`。既定は `single_choice`。複数選択には `multi_choice`、記述だけなら `free_text` を使う。
- zsh では `=` を含む引数が展開されることがあるので、`--fm` の値は引用符で囲む。
- 質問の対象は `--subject <id>` を対象ごとに繰り返す。複数の id を一つのシェル変数に入れて展開すると zsh では分割されないので、配列か `bash -c` を使う。
