---
name: prepare-review
description: Use to review a DIS/REQ/DES/SPEC record with fresh-context reviewers (no conversation history) using the EGM personas and standards, record findings as a REV record through prepare-dev, and handle them without hiding unaddressed items. Use before a newly written record is accepted, and when a change to an accepted record adds, removes or alters a rule (not for typo fixes, rewording or renames).
---

# Review（レビュー）

目的は、会話の文脈を知らない読み手に記録だけを読ませ、書かれていない前提、矛盾、過剰設計、実装に進めない曖昧さを見つけること。成果物は `REV-*`（kind `review`）。レビューは記録であって証拠ではない。`RUN-*` の結果は変わらない。

persona と評価基準は EGM（`references/egm/`、版は `references/egm/ATTRIBUTION.md`）のものを無改変で使う。進め方は EGM と異なり、ここに書いたとおりにする。

この skill の `references/` は、読み込んだ SKILL.md の置き場所から解決する。プロジェクト直下に `skills/` がある前提にしない。レビュー役には解決済みのファイルパスを渡す。

## レビューを回す場面

対象が確定しているか（`status: accepted`）と、修正が決まりを変えるかで決める。後の工程から前の記録へ戻って直すときも同じ。

| 場面 | レビュー |
| - | - |
| 新しく書いた記録を、人が確定するまで | 「段階」と「手順」のとおりに回す |
| 確定した記録の、決まりを変えない修正（誤字を直す、言い換える、ファイルの場所や名前を置き換える） | 回さない。検証（`RUN-*`）と手の確認だけで合否を決め、変えた箇所を人への報告に示す |
| 確定した記録の、決まりを変える修正（決まりを足す、消す、中身を変える。例: 「パスワードは 2 桁」を「3 桁と記号」に変える） | 「確定した記録の決まりを変えたとき」のとおりに、変えた決まりとそれに関わる箇所だけを見せる |

## 役割

| 役割 | 持つ情報 | 仕事 |
| - | - | - |
| 進める側（自分） | 会話、記録、経緯 | 資料を束ね、レビュー役を起動し、指摘を記録し、処理する |
| レビュー役（Goldfish） | 束ねた資料と persona だけ | 読む、言い直す、指摘する。書き込みはしない |
| 人 | 目的と優先順位 | 反論の採否、リスクの受容、未解決の判断 |

## 段階

対象の種類で段階を選ぶ。順序はこのとおり。

| 段階 | persona | 評価基準 | 見ること |
| - | - | - | - |
| `zoom_out` | `agents/zoom_out.md` | `document_standards/zoom-out-standard.md` | そもそも必要か、既存で足りないか、過剰設計、脱線 |
| `comprehension` | `agents/comprehension.md` | なし（資料だけ） | 文書だけで意図が伝わるか。言い直しを作者が照合する |
| `design_critique` | `agents/design_critic.md`、必要なら `structural.md` | `document_standards/feature-spec-standard.md`（`DIS-*` には `heilmeier-standard.md`） | 論理、根拠のない仮定、未対処のリスク、代替案の評価 |
| 中間の `zoom_out` | `agents/zoom_out.md` | `zoom-out-standard.md` | 指摘への対応で全体が複雑になったり脱線したりしていないか |
| `implementation_readiness` | `agents/implementation_readiness.md`、`codebase_details.md` | `document_standards/implementation-readiness-standard.md` | 実装者が判断を迫られずに進められるか（`DES-*` の実装計画、`SPEC-*`） |

`DIS-*` には zoom_out と comprehension と design_critique（heilmeier）。`REQ-*` には comprehension と design_critique。`DES-*` には全段階。`SPEC-*` には comprehension、design_critique、implementation_readiness。

## 手順

1. **資料を束ねる**: `review_bundle` に対象と関連する `REQ-*`、`DEC-*`、`DES-*`、`SPEC-*` の id を渡す。返る `path`（`.prepare-dev/reviews/bundle-*.md`）と `inputs`（版）を控える。評価基準は問題・目標・構成・代替案・リスクをまとめて見るものなので、一枚の記録だけに当てない。`inputs` は bundle に入れた記録の版だけである。レビュー役がリポジトリのコードや文書を読むのは自由で、それは `inputs` に入れない（読んだファイルは報告の本文に書かせる）。
2. **レビュー役を起動する**: 会話履歴を持たない新しいサブエージェントに、`references/wrappers/goldfish-prompt.md` の指示を persona ファイル、評価基準ファイル、bundle のパス、段階名、mode、段階ごとの注記で埋めて渡す。会話の内容、自分の意図、前の指摘は渡さない。
   - mode: 各段階の最初の round は `BRAINSTORM`（広く探す）。直したあとの再確認は `REVIEW`（変更箇所と未対処の指摘に絞る）。comprehension は常に `REVIEW`。
   - 段階ごとの注記（`{focus_note}`）: `REQ-*` の design_critique には「要求が何を、なぜ求めるかを見る。どう作るか（ファイル構成、アルゴリズム、ライブラリ、ハッシュ方式など）への指摘は Nit にする」と書く。評価基準 `feature-spec-standard.md` は設計寄りなので、この注記で高さを揃える。再確認の round には「前回から変わった箇所: ...」と `REV-*` の id を書く。
   - Codex: サブエージェント起動の tool に新規コンテキスト（親の会話を引き継がない設定）で渡す。
   - Claude Code: `Agent` tool（general-purpose）に同じ内容を渡す。
   - どちらも、レビュー役には読み取りだけをさせる。
3. **指摘を記録する**: レビュー役の報告を `REV-*` として `record_put` する。frontmatter: `stage`、`subjects`（レビュー対象の版）、`inputs`（bundle の `inputs` 全部）、`reviewer`（`persona`、`persona_version` は `egm@<commit>`、`host`、`context: fresh`）、`paraphrase`（comprehension のとき）、`findings`。各 finding は `id`（`REV-*` の中で一意。`f1`、`f2`、…。他の記録から参照するときは `REV-xxxx/f1`）、`severity`、`scope`、`summary`、`rationale`、`proposed`。`actual` は `unaddressed` のまま。本文にレビュー役の報告を貼る。CLI では `findings` を JSON ファイルに書いて `--fm-file` で渡し、`actual` の更新も同じファイルを直して `record put` する（`findings` は配列ごと置き換わる）。
4. **言い直しを照合する**（comprehension）: `paraphrase` を自分の意図と比べる。違っていれば、それ自体を finding にする（資料の書き方の問題）。
5. **指摘を処理する**: 指摘ごとに提案（`proposed`）を決め、実際に行ったことを `actual` と `actual_ref` に記録する。
   - `fix`: 対象の記録を直す。新しい決まりや仕組みを足さずに直せるときだけ選ぶ。直したら `actual: fixed`、`actual_ref` に直した記録の版。
   - `contest`: 指摘が誤りだと主張する。レビュー役が読める根拠（記録の該当箇所、コード）を示す。反論は人が認めてはじめて `contested_accepted` になる。それまでは未対処。
   - `escalate`: 根拠が資料の外（口頭の決定、慣習）にある。人への質問（`question_create`）にし、`actual: escalated`、`actual_ref` に `Q-*`。回答で `DEC-*` ができたら `actual_ref` を更新する。
   - `decline`: Nit で直さない。`actual: declined`。
   - リスクを受容するなら、人の判断として `DEC-*` に残し、`actual: accepted_risk`、`actual_ref` に `DEC-*`。

   直すのに新しい決まりや仕組み（記録に無かった条件、主張、手順、検査など）を足す必要がある指摘は、その場で足さない。`proposed: escalate` にし、その段階のほかの指摘を処理し終えてから、足すものごとに質問を作って、まとめて人に聞く。選択肢は「今やる」「別の作業にする」「直さない」。回答が `DEC-*` になったら、「今やる」は足して直す（`fixed`）。「別の作業にする」は `escalated` のまま `actual_ref` を `DEC-*` にし、人への報告に書く。「直さない」は `accepted_risk` にする。

   今回の作業で書いても変えてもいない箇所への指摘は、直さず一覧に残す。`proposed: escalate`、`proposed_note` に「今回変えていない箇所。人がやると決めるまで直さない」と書き、`actual` は `unaddressed` のままにする。`status` の `open_findings` が、その一覧になる。頼まれた作業を先に終え、残した指摘を人への報告に書く。

   提案は完了ではない。`REV-*` の `status` を `resolved` にしても、`actual: unaddressed` の finding は `status` の `open_findings` に残る。
6. **再確認する**: Blocker か Major を `fix` したら、同じ段階を `REVIEW` mode でもう一度行う。再確認には、その指摘を出した persona だけを使ってよい。settled（`contested_accepted`、`accepted_risk`、`declined`）の指摘は、対象と根拠が変わっていない範囲では再開しない。関連する記録の版が変わったら再確認する。`fixed` は settled ではなく、次のレビューで同じ箇所に指摘が出れば再開する。
7. **収束と上限**: 段階は次の両方を満たしたら収束。(1) その段階のすべての `REV-*` で Blocker と Major が未対処でない（`fixed`、`escalated`、`contested_accepted`、`accepted_risk` のいずれか）。手順 5 で一覧に残した指摘（今回変えていない箇所への指摘）は、この条件に数えない。(2) 直近の `REVIEW` round が、直した箇所に新しい Blocker と Major を出していない。`BRAINSTORM` round が毎回新しい Major を出すのは persona の性質なので、BRAINSTORM は段階ごとに一回にし、その結果で収束を判定しない。round の上限は段階ごとに四回（BRAINSTORM 一回と REVIEW 三回）。上限に達したら、未対処を一覧にして人に聞く（受容して進む、続ける、前の工程へ戻る）。上限到達を受容と読み替えない。費用の目安: persona 一つの round が十五万〜二十五万 token（具体例の実測）なので、再確認は persona を絞る。費用を理由に上限の手前で止めるときも、それは人が決めることなので、上限に達したときと同じく人に聞く（自分で置いた予算を規則のように扱わない）。

## 確定した記録の決まりを変えたとき

「手順」を次のように変えて行う。ここに書いていないことは「手順」と同じ。

1. **見せるもの**: `review_bundle` で記録の全文を束ねない。次の三つを一つのファイルにまとめ（置き場所は `review_bundle` の出力と同じ `.prepare-dev/reviews/`）、bundle のパスの代わりに渡す。`REV-*` の `inputs` には、抜粋を取った記録の id と版（`record_get` の `rev` と `sha256`）を書く。
   - 頼まれたこと。人の言葉のまま書く（照らす先として渡す。手順 2 の「会話の内容は渡さない」の例外はこれだけ）。
   - 変えた決まりの、前と後。
   - それに関わる箇所の抜粋（変えた決まりを使っている決まりと、同じことを決めている決まり）。記録の id と版を付ける。
2. **レビュー役**: persona は `agents/design_critic.md` 一つ。評価基準は渡さない。mode は `REVIEW`、`stage` は `design_critique`。`{focus_note}` には「見る範囲は、このファイルにある変えた決まりと、それに関わる箇所。見ることは、変えた決まりが頼まれたことに合うかと、ほかの決まりと食い違わないか」と書く。
3. **回数**: Blocker か Major を直したら、同じ役で一度だけ確かめ直す。確かめ直した後も Blocker か Major が未対処で残るときは、それ以上回さず、一覧にして人に聞く（手順 7 の、上限に達したときと同じ）。
4. **確定し直す**: 指摘を処理し終えたら、変えた決まりの前と後を示して `question_create` する（`subjects` は変えた記録）。人が確定し、回答が `DEC-*` になる。

## 規則

- レビュー役が失敗、無応答、空の報告だったときは「未実施」として記録し、「指摘なし」に数えない。
- Nit は進行を止めない。
- bundle に入れた記録はすべて `inputs` に残す。レビュー役が自分で読んだリポジトリのファイルは `inputs` ではなく本文に書く。Codex ではホストの指示ファイルがレビュー役に届くことがあるため、それも前提として扱う。
- 質問（`question_create`）は、その段階のレビューの修正を済ませてから作る。質問の後に対象を直すと回答が `stale` になる。
- 修正で対象の版が進み、回答の無い質問が古い版を指すようになったら、新しい版で質問を作り直し、古い質問を `question_cancel`（`replaced_by` に新しい質問）で取り消す。
- レビューの経緯（round、予算、止めた理由）は対象の記録の本文に書かない。`REV-*` の指摘と処理が持つ。本文に書くと round ごとに対象の版が進み、対象を指す質問が古くなる。
- 同じ対象に複数の persona を使うときは、persona ごとに `REV-*` を分けてよい。
