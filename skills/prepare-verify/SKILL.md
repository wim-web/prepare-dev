---
name: prepare-verify
description: Use after a spec is approved to create or update the code, proofs and tests for its claims, run the verification plugins through prepare-dev (verify_run), read the evidence, and loop back to the right stage. Distinguishes fixing the implementation from revisiting requirements.
---

# Build と Verify（実装と検証）

目的は、承認した `SPEC-*` の版を入力に、対象コード・証明・テストを作り、実際のツールで検査して、要求に結びついた証拠（`RUN-*`）を残すこと。

## 入力

- `SPEC-*`（`record_get` で版を控える）、`DES-*` の実装計画、`DEC-*`。
- プラグインの書き方: `prepare-dev guidance lean4`、`prepare-dev guidance vitest`、`prepare-dev guidance gotest`。配布物に同梱した文書を読む。対象プロジェクトに prepare-dev のソースを置く必要はない。

## 手順

1. `SPEC-*` の各主張について、native 参照が指すもの（定理、テスト、harness）を作るか更新する。参照の名前は仕様と一致させる。
2. 実装は `DES-*` の実装計画の順に進め、各段階でビルドとテストが通る状態を保つ。
3. `verify_run` を呼ぶ。`plugin`、`claims`（`{spec, claim}`）、`targets`（対象ファイル）、必要なら `config`。`verify_wait` で結果を受け取る。
4. 結果を読む。`result` はツールの実出力から決まる。自分の文章で「検証済み」と書いても記録にはならない。
   - `verified`: 主張ごとの `result`、`assumptions`（公理、前提）、`scope`（境界、実行したケース）を確認し、主張の保証の範囲と一致しているか見る。
   - `failed`: 反例やログ（`artifacts`）を読む。
   - `unknown` / `timeout`: 未解決として扱う。すぐに実装のバグと断定しない。
   - `tool_error` / `unsupported`: 検査自体の失敗。環境を直す。
5. `trace REQ-xxxx` で、要求から主張、証拠までが現在の版に対して `applicable` であることを確認する。
6. 要求ごとに人に報告する項目をまとめる。証拠があるもの、未解決のもの、保証の範囲。

## 二つのループを分ける

| 見つかったこと | 戻る先 |
| - | - |
| 実装、証明、テストの誤り | ここで直して `verify_run` をやり直す |
| 仕様の主張が要求を正しく表していない | `prepare-specify`。主張を直し、`SPEC-*` の版を上げる |
| 設計の前提が成り立たない | `prepare-design`。`DES-*` と `DEC-*` を見直す |
| 要求そのものの誤り、矛盾、実現不能 | `prepare-requirements`。その要求を対象に質問を作り、人の判断を `DEC-*` に残してから直す |

検証に通すために要求や仕様を黙って弱めない。境界を狭める、前提を増やす、対象外を広げる場合は、それ自体を判断として記録する。

## 終了条件

- `SPEC-*` のすべての主張に、現在の版に対して `applicable` な `RUN-*` がある。
- `verified` でない主張は、未解決として報告され、戻る先が決まっている。
- 証拠の `assumptions` と `scope` が仕様の `assumptions` と矛盾していない。
