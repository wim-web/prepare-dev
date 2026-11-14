---
name: prepare-review-code
description: Use with prepare-dev when a change touches the implementation or existing behavior. Reviewers try to break the change against the adopted design, decisions and examples, keep only the findings they can reproduce by running something, and report them with reproduction steps, impact and the place to fix. Reports only - it does not fix.
---

# prepare-review-code（実装レビュー）

実装の不具合と、既存の動作の退行を探す。残すのは、実行して再現できた指摘だけ。

先に `prepare-dev` の「共通の決まり」を読む。

## 起動する状況

利用者が、別の担当によるレビューを使うと決めているときに使う。頼まれていないのに始めない。そのうえで、次のときに起動する。

- 実装を変えた。
- 既存の動作に影響する変更がある。

設計が要求に合っているかは、ここでは見ない。それは `prepare-review-spec` が見る。

## 手順

1. **対象を固定する。** 採用済みの設計、関係する決定、具体例、コードの差分、テストと実行結果を集める。
   `ppdev run start` ではなく、進行役の手順で起動する。
   `ppdev spawn check --role 実装レビュー --target <DES の番号> --file <差分や対象ファイル>`
2. **壊しにいく。** 確認役は、変更を作業用の写しで動かし、採用済みの具体例と受入条件に反する動きを探す。見る場所：
   - 採用済みの具体例のうち、テストになっていないもの
   - 例外と境界（空、上限、順番の入れ替わり、途中の失敗、二重の実行）
   - 変更していないはずの既存の動作
   - 保存や送信に失敗したときの表示
3. **再現できたものだけ残す。** 読んだだけの推測は指摘にしない。確かめられなかった疑いは、「未確認」として分けて返す。
4. **指摘を記録する。**
   ```
   ppdev new FND --title "<指摘>" --body "<再現の手順と、期待との違い>" \
     --link about:<DES@版> --link raised-by:<実行> \
     --fields-json '{"run":"<実行>","affects_acceptance":true,"file":"<場所>","impact":"<影響>","fix_target":"<直す対象>"}'
   ```
   受入条件に関わらないと確認役が判断した指摘は `"affects_acceptance": false` にする。
5. **回答を記録する。** `ppdev run answer <実行> --file <報告>`。止まったら `ppdev run fail`。
6. **直すのは別の作業。** 修正は `prepare-design` の「本番の実装」で行う。直したあと、もう一度ここへ戻す。

指摘の閉じ方は `prepare-review-spec` の「指摘の閉じ方」と同じ。不採用や軽微にするには、反証になる保存内容、新しい文脈での再確認、利用者の決定のどれかが要る。

## 確認役への依頼文

```
この変更を壊しにいく。直さない。報告だけを返す。
設計と決定：<場所>
具体例と受入条件：<場所>
差分：<場所>

作業用の写しで実際に動かす。元の作業場所は変えない。
残すのは、実行して再現できた不具合だけ。指摘ごとに次を返す。
- 再現の手順（入力、順番、実行した命令）
- 期待した結果と、実際の結果
- 影響（受入条件の成否に関わるか）
- 直す対象（ファイルと場所）

再現できなかった疑いは、「未確認」として分けて返す。別の担当を起動しない。
<ticket_line>
```

## 参考にした手順

変更を壊しにいき、実行で示せた指摘だけを残し、直さずに報告する進め方は、pstack の interrogate を参考にしている。担当の起動は `prepare-dev` の設定を通す形に変えている。
