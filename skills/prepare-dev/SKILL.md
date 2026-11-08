---
name: prepare-dev
description: Use when a prepare-dev MCP server is available and the work involves requirements, decisions, specs, asking the human a question through the local UI, or recording verification evidence. Covers the question → answer → confirm loop and verify_run.
---

# prepare-dev

prepare-dev の runtime（MCP server `prepare-dev`）を使って、要求・判断・仕様・証拠を `prepare/` の記録として残す。記録の正本は runtime 経由で書き、人への質問は UI を通す。

## 最初にすること

1. `status` を呼ぶ。未処理の回答（`pending`）があれば、新しい質問を作る前にそれを処理する。前のセッションで受け取り損ねた回答がここに残る。
2. 対象の記録を `record_get` で読み、`rev` と `sha256` を控える。更新は必ず `expected_rev` と `expected_sha256` を付ける。

## 人に聞く前に

調査、候補の作成、比較、推奨理由の整理を先に済ませる。質問は「候補と推奨を示して選んでもらう」形にし、思考を丸投げしない。聞くのは目的、優先順位、価値判断、許容するトレードオフ、調査で得られない情報だけ。

## 質問の流れ

1. `question_create`: `subjects` に対象の記録 id、`options` に候補（`recommended` と `reason` を付ける）。返る `url` を人に伝える。
2. `question_wait`: `pending` が返ったら再度呼ぶ。待機時間は `timeout_ms` で調整できる。
3. 回答を受け取ったら `question_ack`。これは受領の記録で、処理済みではない。
4. 回答を判断にするなら `question_apply` に decision の本文（採用、代替案、理由）を渡す。これが唯一の確定操作で、`DEC-*` が作られる。`record_put` では回答由来の decision を作れない。
5. 反映しないなら `question_close` に理由を付ける。
6. `stale`（対象が質問後に変わった）と返った回答は自動では反映されない。対象の現在版を読み直し、必要なら質問し直す。

同じ `answer_id` で `question_apply` を再試行しても判断は増えない。

## 検証

1. spec（`SPEC-*`）の `claims[].native` に、プラグインごとの参照（Lean の定理名、vitest のテスト名など）を書く。
2. `verify_run` で `plugin`、`claims`（`{spec, claim}`）、`targets` を渡し、`verify_wait` で結果を受け取る。
3. 結果の `result` はツールの実出力から決まる。自分の文章で「検証済み」と書いても記録にはならない。
4. `trace` で要求から判断・仕様・証拠までを確認する。`applicable: false` の証拠は過去の版に対するもので、現在版の成功ではない。

## 工程を戻すとき

検証に通すために要求や仕様を黙って弱めない。要求の誤りを見つけたら、その要求を対象にした質問を作り、人の判断を `DEC-*` に残してから直す。
