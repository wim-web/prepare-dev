# 参考にした手順と、その出どころ

ここの skill は、既にある skill の進め方を参考にして書いている。文章や設定をそのまま写したものはない。手順は書き直し、起動・記録・完了の決まりは prepare-dev のものに合わせている。

元の skill が更新されたときは、下の「読んだ版」から何が変わったかを見て、取り入れるものを選ぶ。起動の設定、受け渡す情報、記録・検証・図とのつながりに影響しないかも確かめる。

| この skill | 参考にしたもの | 読んだ版 | 変えたところ |
|---|---|---|---|
| `prepare-dev` | pstack の poteto-mode | pstack-claude 0.9.137（2026-11-12 に全文を読んだ）。思考量の決まりは 0.9.138（2026-11-14 に該当の段落を読んだ）。元の cursor/plugins の main は要約で読んだ | 思考量を仕事に合わせて上げ下げする決まりは、同じにした。違いは、変えた理由を記録することと、上げ幅を照合すること。確かめる担当だけは上げ下げしない、という例外は取り入れていない。別モデルへの切り替えと、利用者を待たずに進める決まりも取り入れていない |
| `prepare-setup` | pstack の setup-pstack と、その設定の表 | pstack-claude 0.9.137 と 0.9.138。表の書き方は、実際に使われている設定の表（2026-11-14 に読んだ）に合わせた | 行のない役割は起動しない。表にないモデルへ切り替えない。上げ下げをやめる行、使わない値、起動数の上限、例外を書ける |
| `prepare-design` | pstack の architect、arena | 同上 | 設計がまとまっても確認なしに実装へ進まない。担当の追加は進行役を通す |
| `prepare-review-spec` | EGM の egm-review-loop | google/elephant-goldfish-model の main（2026-11-12 に要約で読んだ） | 対象を記録から作る文書にした。指摘を記録の番号と版に結び付ける。解決役は対象を直接書き換えない。一括の自動承認は置いていない |
| `prepare-review-code` | pstack の interrogate | pstack-claude 0.9.137 | 起動を進行役の設定に通す |
| `prepare-verify` | pstack の create-formal-verification-skill、maintain-formal-verification-skill、statechart | pstack-claude 0.9.137 | 検証の対象と方法を、保証要求から決める。受理の確認（穴、公理、変種）を ppdev が行う。TLA+ と statechart は、今は使っていない |
| `prepare-show` | visual-explainer、yomiyasu | visual-explainer 0.15.0、yomiyasu 3.1.0（どちらも 2026-11-12 に要約で読んだ）。visual-explainer は 2026-11-14 に、main の手順書を項目ごとに問い合わせて読み直した（原文の全文は読めていない） | 取り入れたもの：見せるのは一つのファイルで開ける HTML のページ、図の描き方の決まり、どの内容にどの図を使うか、避ける見た目、手描きの SVG を基本にして Mermaid は頼まれたときだけ。取り入れていないもの：元の見た目の一式（kit.css）と描画の検査（render.mjs）、ページ上で図を押して返事を集める仕組み、一歩ずつ進める表示、スライド。代わりに、見た目は自前の雛形（`prepare-show/page.html`）を写して使う。検証結果から作る図の番号と版は ppdev が発行する。日本語を整える skill は、導入時の設定で指定されたときだけ使う |

## 記録できていないこと

- 読んだ時点のコミット ID は記録していない。pstack-claude は配布版の番号で特定できる。ほかは「main を 2026-11-12 に読んだ」までしか分からない。
- 「要約で読んだ」ものは、原文を一字ずつ確かめていない。
- pstack と pstack-claude のライセンスは確かめていない。EGM は Apache-2.0、visual-explainer と yomiyasu は MIT と読んだ。

元の文書やコードを写して取り込む、または再配布することになったら、そのときにライセンスを確かめ、必要な著作権表示を保つ。
