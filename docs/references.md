# 参照元の一覧

prepare-dev を構想した会話（ChatGPT、2026-11-08 まで）で参照した資料。その会話の AI が、会話・添付・引き継ぎ文書から抜き出した一覧を、2026-11-10 に残した。文は原文のままだが、個人に紐づくリンク（ChatGPT の space・ライブラリ・プラグインのページ、Neo の保存記事、claude.ai のセッション）と、追跡用のパラメータが付いた重複のリンクは外し、名前だけを残した。原文の「この会話」は、その ChatGPT の会話を指す。工程の記録ではないので、prepare/ ではなくここに置く（DES-0004）。最後の節（11）は、残すときに足したもの。

---

**Chopin の話から、要件整理・PDD・構成検討・rev 2／3 のレビュー・EGM 評価まで、この会話と添付・引き継ぎ文書から参照元を抜き出しました。**

同じ資料への重複リンクはまとめ、`utm_source`・`linkId` などの追跡用パラメータは除いています。**元の個別 URL を回収できなかったものも、末尾に名前を残しています。**

## 1. 出発点・比較したプロダクト

| 参照元 | この話で参照した内容 |
|---|---|
| [GitHub Next — Chopin](https://githubnext.com/projects/chopin/) | 人と AI が計画・仕様を共有し、議論と決定理由を残す体験 |
| [githubnext/chopin](https://github.com/githubnext/chopin) | 関連リポジトリ・公開状況の確認先として登場 |
| [Ref Plans](https://plan.ref.tools/) | Chopin に似た、計画の共有・レビューの候補 |
| [Ref Plans — Codex への導入](https://docs.ref.tools/plans/install/codex) | Codex から利用する経路 |
| [Ref — Pricing](https://docs.ref.tools/usage/pricing) | 利用条件・料金の比較 |
| [Pad](https://www.getpad.dev/) | 計画・仕様を扱う代替候補 |
| [Intent](https://intentapp.dev/) ／ [Documentation](https://intentapp.dev/docs) | エージェントと仕様・計画を扱う代替候補 |
| [Plannotator](https://plannotator.ai/) | エージェントが作った計画を人がレビューする候補 |
| [Plannotator — Codex](https://docs.plannotator.ai/open-source/agents/codex) | Codex との連携方法 |

## 2. 対話・要求整理・仕様化の skills／フレームワーク

| 参照元 | この話で参照した内容 |
|---|---|
| [QingYunA/answer-me-with-html](https://github.com/QingYunA/answer-me-with-html) | HTML 上で選択し、エージェントへの返答をまとめる UI |
| [mattpocock/skills — grilling/SKILL.md](https://raw.githubusercontent.com/mattpocock/skills/main/skills/productivity/grilling/SKILL.md) | `grill-me` 系の問いによる掘り下げ。引き継ぎ文書に残した参照先 |
| [obra/superpowers](https://github.com/obra/superpowers) | skills を組み合わせる開発フロー |
| [Superpowers — brainstorming/SKILL.md](https://github.com/obra/superpowers/blob/main/skills/brainstorming/SKILL.md) | 構想の整理、選択肢、設計の対話、試作からのフィードバック |
| [BMAD — Install BMAD](https://docs.bmad-method.org/start/install-bmad/) | 導入・利用形態の確認 |
| [BMAD — Define Requirements and a Specification](https://docs.bmad-method.org/plan/define-requirements-and-a-specification/) | 構想から利用者・利用シナリオ・要求を整理する手順 |
| [GitHub Spec Kit](https://github.com/github/spec-kit) | 仕様を中心に進める開発フローの比較 |
| [Spec Kit — Integrations](https://github.github.io/spec-kit/integrations/index.html) | 対応エージェント・接続先 |
| [Spec Kit — specify.md](https://github.com/github/spec-kit/blob/main/templates/commands/specify.md) | 機能説明から要求、利用シナリオ、受入条件を作る手順 |
| [Spec Kit — clarify.md](https://github.com/github/spec-kit/blob/main/templates/commands/clarify.md) | 仕様の曖昧さを絞って確認する手順 |
| [Kiro — Analyze Requirements](https://kiro.dev/docs/specs/analyze-requirements/) | 要求間の矛盾・不足の分析と、修正案の提示 |

## 3. 要求工学・プロダクト探索の根拠

| 参照元 | この話で参照した内容 |
|---|---|
| [SWEBOK — Software Engineering Topics](https://www.computer.org/education/bodies-of-knowledge/software-engineering/topics) | 要求の獲得・分析・仕様化・妥当性確認・管理という整理 |
| [SWEBOK Guide V4 — PDF](https://ieeecs-media.computer.org/media/education/swebok/swebok-v4.pdf) | 上記の本文資料 |
| [ISO/IEC/IEEE 29148](https://www.iso.org/standard/72089.html) | 要求工学・要求仕様の参照軸 |
| [Design Council — Framework for Innovation](https://www.designcouncil.org.uk/resources/framework-for-innovation/) | Double Diamond、問題の探索と解決案の探索 |
| [Goal-Oriented Requirements Engineering — RE01.pdf](https://webperso.info.ucl.ac.be/~avl/files/RE01.pdf) | 目標から要求を導く考え方 |
| [SEBoK — System Requirements](https://sebokwiki.org/wiki/System_Requirements) | システム要求の整理 |
| [Supporting Stakeholder Requirements Expression with LLM Revisions](https://arxiv.org/html/2601.16699v1) | LLM による要求の書き直しに関する研究。前半の添付文書で参照 |

IREB、SEI の QAW、GOV.UK の Alpha も登場しましたが、個別資料の URL を完全には回収できていません。末尾に分けています。

## 4. PDD Kit・Proof-Driven Development

| 参照元 | この話で参照した内容 |
|---|---|
| [GitHub Next — PDD Kit](https://githubnext.com/projects/pdd-kit/) | 意図の明確化、形式仕様、実装・証明、実際の検証をつなぐ流れ |
| [PDD Kit — Reading the Spec](https://githubnext.com/projects/pdd-kit/#reading-the-spec) | 人が確認する仕様と、Verus の契約の対応 |
| [PDD Kit — Where Proof Stops](https://githubnext.com/projects/pdd-kit/#where-proof-stops) | 証明の対象外、信頼する部分、テストとの区別 |
| [PDD Kit — An Overview of login.rs](https://githubnext.com/projects/pdd-kit/#an-overview-of-loginrs) | ログイン処理の仕様を具体化した例 |
| [PDD Kit — What to Expect from PDD Kit](https://githubnext.com/projects/pdd-kit/#what-to-expect-from-pdd-kit) | 適用範囲と、段階的に検証する進め方 |
| [Microsoft Research — Formally verifying agent-written full-stack web apps with PDD Kit](https://www.microsoft.com/en-us/research/blog/formally-verifying-agent-written-full-stack-web-apps-with-pdd-kit/) | PDD の工程、意図の文書、証明・テスト・信頼境界の説明 |

ここで参照している PDD Kit は、**GitHub Next の Proof-Driven Development** です。

## 5. json-render・判断用 UI

| 参照元 | この話で参照した内容 |
|---|---|
| [vercel-labs/json-render](https://github.com/vercel-labs/json-render) | 判断用 UI の基盤候補 |
| [json-render — Catalog](https://json-render.dev/docs/catalog) | 使用できるコンポーネント・アクションを定義する仕組み |
| [json-render — Registry](https://json-render.dev/docs/registry) | コンポーネント、`emit`、アクションの handlers の接続 |
| [json-render — Data Binding](https://json-render.dev/docs/data-binding) | UI の入力と状態の対応 |
| [json-render — Core API](https://json-render.dev/docs/api/core) | `UIElement.on`、`ActionBinding`、`params` などの型・API |
| [json-render — MCP パッケージ README](https://github.com/vercel-labs/json-render/blob/main/packages/mcp/README.md) | `@json-render/mcp` と MCP Apps の関係 |

## 6. MCP・エージェントとの接続

| 参照元 | この話で参照した内容 |
|---|---|
| [MCP — Architecture](https://modelcontextprotocol.io/docs/learn/architecture) | クライアント・サーバー、stdio／HTTP、共通実装とプロセスの関係 |
| [MCP Apps — Overview](https://modelcontextprotocol.io/extensions/apps/overview) | 会話内 UI の埋め込みと、ホスト側の対応 |
| [Codex — MCP](https://developers.openai.com/codex/mcp) | MCP 登録、`tool_timeout_sec` など。確認時の転送先は [ChatGPT Learn の MCP ページ](https://learn.chatgpt.com/docs/extend/mcp?surface=cli) |

## 7. 形式検証・テスト・結果の意味

### Lean

| 参照元 | この話で参照した内容 |
|---|---|
| [Lean 公式](https://lean-lang.org/) | Lean の位置づけ |
| [Lean Reference — Validating Proofs](https://lean-lang.org/doc/reference/latest/ValidatingProofs/) | 証明の検査、定理の意味、公理・依存関係、保証の範囲 |
| [Lean 4.35.0 — `Lean/Elab/Import.lean`](https://github.com/leanprover/lean4/blob/v4.35.0/src/Lean/Elab/Import.lean) | `--src-deps` の依存列挙処理を確認したソース |
| [Lean 4.35.0 — `Lean/Shell.lean`](https://github.com/leanprover/lean4/blob/v4.35.0/src/Lean/Shell.lean) | `--src-deps` の CLI 分岐を確認したソース |

### Kani・Verus・モデル検査・テスト

| 参照元 | この話で参照した内容 |
|---|---|
| [Kani — Verification Results](https://model-checking.github.io/kani/verification-results.html) | JSON 出力、検査結果の状態、中断時の出力ファイルの扱い |
| [Kani — Loop Unwinding](https://model-checking.github.io/kani/tutorial-loop-unwinding.html) | 探索境界、unwinding と対象の性質の区別 |
| [verus-lang/verus](https://github.com/verus-lang/verus) | Rust の実装・仕様・証明を扱う検証器 |
| [Verus Guide — Trusted Computing Base](https://verus-lang.github.io/verus/guide/tcb.html) | 証明が信頼する構成要素と、その境界 |
| [Lamport — TLA+ とモデルの説明](https://lamport.azurewebsites.net/tla/model-popup.html) | モデルについて確かめたことと、実装について確かめたことの区別 |
| [Lamport — TLA+ Tutorial, Session 5](https://lamport.azurewebsites.net/tla/tutorial/session5.html) | 有限状態の探索、任意長の実行、進行性・fairness |
| [CBMC — Tutorial](https://raw.githubusercontent.com/diffblue/cbmc/develop/doc/cprover-manual/cbmc-tutorial.md) | 有界検査と、その結果が意味する範囲 |
| [Vitest — Writing Tests](https://vitest.dev/guide/learn/writing-tests) | prepare-dev 本体の TypeScript 実装をテストする用途 |

## 8. EGM — Elephant-Goldfish Model

### 全体構成・導入・対応環境

| 参照元 | この話で参照した内容 |
|---|---|
| [google/elephant-goldfish-model](https://github.com/google/elephant-goldfish-model) | EGM 全体 |
| [README.md](https://github.com/google/elephant-goldfish-model/blob/main/README.md) | 目的、skills、導入方法 |
| [docs/designs/architecture.md](https://github.com/google/elephant-goldfish-model/blob/main/docs/designs/architecture.md) | Elephant／Goldfish の役割、工程、レビュー段階、実装への引き継ぎ |
| [docs/harnesses.md](https://github.com/google/elephant-goldfish-model/blob/main/docs/harnesses.md) | 共通 skills と各ホストの接続、fresh context の条件 |
| [TESTING.md](https://github.com/google/elephant-goldfish-model/blob/main/TESTING.md) | 導入・実行テストの記録と、確認できている範囲 |
| [Issue #237](https://github.com/google/elephant-goldfish-model/issues/237) | Goldfish にホスト側の指示ファイルが届く問題。主に harnesses 文書経由で参照 |
| 公開 EGM プラグイン（ChatGPT のページ） | 導入先として紹介されたリンク |

### 読んだ skill 本文・テンプレート

| 参照元 | この話で参照した内容 |
|---|---|
| [egm-brainstorm/SKILL.md](https://github.com/google/elephant-goldfish-model/blob/main/skills/egm-brainstorm/SKILL.md) | 構想の探索、レビュー観点、brief の構成、対話規則 |
| [egm-draft/SKILL.md](https://github.com/google/elephant-goldfish-model/blob/main/skills/egm-draft/SKILL.md) | 設計書作成、節ごとの承認、選択肢の提示、brief との照合 |
| [設計書テンプレート — design_doc.md](https://github.com/google/elephant-goldfish-model/blob/main/skills/egm-draft/references/design_doc.md) | 目標・非目標、利用シナリオ、設計、代替案、実装・検証計画 |
| [egm-review/SKILL.md](https://github.com/google/elephant-goldfish-model/blob/main/skills/egm-review/SKILL.md) | 段階的レビュー、再監査、残件を受容して進める分岐 |
| [egm-review-loop/SKILL.md](https://github.com/google/elephant-goldfish-model/blob/main/skills/egm-review-loop/SKILL.md) | 指摘 ID、settled／fixed、未実施 reviewer、収束・上限・対話ゲート |
| [egm-resolve/SKILL.md](https://github.com/google/elephant-goldfish-model/blob/main/skills/egm-resolve/SKILL.md) | CONTEST／ESCALATE／PROPOSE FIX／DECLINE、修正と処理終了の意味 |
| [egm-drift-report/SKILL.md](https://github.com/google/elephant-goldfish-model/blob/main/skills/egm-drift-report/SKILL.md) | 設計文書と実装の差異を調べる手順 |

### Reviewer・評価基準

| 参照元 | この話で参照した内容 |
|---|---|
| [agents/comprehension.md](https://github.com/google/elephant-goldfish-model/blob/main/agents/comprehension.md) | 文書を先に言い換え、読み手の理解と意図のずれを見つける |
| [feature-spec-standard.md](https://github.com/google/elephant-goldfish-model/blob/main/resources/document_standards/feature-spec-standard.md) | 問題、目標、設計、代替案、リスクの確認 |
| [heilmeier-standard.md](https://github.com/google/elephant-goldfish-model/blob/main/resources/document_standards/heilmeier-standard.md) | 目的、現状との差、価値、リスク、費用、期間、成功条件 |
| [implementation-readiness-standard.md](https://github.com/google/elephant-goldfish-model/blob/main/resources/document_standards/implementation-readiness-standard.md) | 実装計画の具体性、残った設計判断、変更対象と検証計画 |
| [zoom-out-standard.md](https://github.com/google/elephant-goldfish-model/blob/main/resources/document_standards/zoom-out-standard.md) | 本当に必要か、単純化できるか、既存のものを使えるか |
| [re-implementation-standard.md](https://github.com/google/elephant-goldfish-model/blob/main/resources/document_standards/re-implementation-standard.md) | 振る舞いやインターフェースを再実装できるだけの記述があるか。調査担当のサブエージェントが参照 |

次の二つは、読んだ skill 本文から案内されていた接続資料です。上の評価で本文を精査した資料とは区別して挙げます。

- [docs/codex.md](https://github.com/google/elephant-goldfish-model/blob/main/docs/codex.md)
- [docs/claude.md](https://github.com/google/elephant-goldfish-model/blob/main/docs/claude.md)


## 9. ほかの場所で参照したもの（2026-11-10 に追記）

| 参照元 | 参照した内容 |
|---|---|
| [lauren（@poteto）の投稿（2026-11-03）](https://x.com/poteto/status/2117437607940681849) | 対抗レビューにトークンを使うより、実証的な方法と形式検証を組み合わせるほうがよい、という主張。Codex の別のプロジェクト（ai-ultra-neo-research）のセッションで、2026-11-05 に参照した |
| [pstack の README](https://github.com/cursor/plugins/blob/main/pstack/README.md) ／ [解説記事](https://barnabyrobson.org/on-pstack/) | poteto の skill 集。プロジェクトごとに、アプリを利用者と同じように動かして確かめる skill（`create-verification-skill`）と、形式検証の skill（`create-formal-verification-skill`）を作る。同じセッションで 2026-11-07 に参照した |
| [PDD Kit](https://githubnext.com/projects/pdd-kit/) と [Microsoft Research の記事](https://www.microsoft.com/en-us/research/blog/formally-verifying-agent-written-full-stack-web-apps-with-pdd-kit/) の empirical verification | 形式検証と並べる確かめ方（経験的検証）。コードを実際に動かして確かめ、証明が届かない所（証明器から見えないライブラリ、外の世界、終わるかどうか、人が判断すること）を補う。結果は証明ではなく証拠として扱う。prepare-dev では、テストの RUN と人の確認記録（REC）がこれにあたる |
