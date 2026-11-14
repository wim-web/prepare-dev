---
name: prepare-design
description: Use with prepare-dev when how to build something is undecided, when facts gathered by a prototype would let alternatives be compared, when a design has a problem, or when it is time to write the production code. Compares design alternatives on the adopted examples, records designs, prototypes and observed facts, hands the design to spec review before it is chosen, and implements only after the start conditions in prepare-dev are met.
---

# prepare-design（設計・試作・実装）

設計案を比べ、事実を試作で確かめ、採用された設計を実装する。

先に `prepare-dev` の「共通の決まり」を読む。

## 起動する状況

- 実現方法が決まっていない。
- 事実を調べれば、案を比べられる。
- 設計に問題が見つかった。
- 本番の実装に入る。

## 設計案を比べる

実現方法に、利用者の判断が要る選択肢があるときに行う。小さな依頼で、やり方が一つに決まるなら、比べずに進める。

1. **事実から始める。** 採用済みの要求・具体例・制約（`ppdev list --status adopted`）と、関係するコードを読む。推測ではなく、読んだものと実行結果を根拠にする。
2. **違いのある案を出す。** 選択の余地があるなら、方向の違う案を二つ以上出す。別の担当に案を作らせるときの役割は「設計」。同時に作れる案の数は、設定の表の「設計」の行の人数まで。担当の中からもう一人要ると分かったときは、何を調べる作業か、役割は何かを進行役へ返す。自分では起動しない。
3. **同じ具体例で比べる。** 採用済みの具体例を各案に当て、結果がどう変わるかを並べる。案ごとに、置いた前提と境界を書く。
4. **案を書いた文脈とは別の目で評価する。** 評価役の役割は「設計の評価」。進行役を通して起動する。
5. **記録する。**
   `ppdev new DES --title "<設計>" --body-file <ファイル> --link designs:<REQ>`
   採らなかった案と理由も本文に残す。
6. **利用者がレビューを使うと決めているなら、選ぶ前に仕様レビューへ渡す。** 設計案、代替案、前提を `prepare-review-spec` に渡す。設計が新しい前提や制約を加えるなら、要求との差が指摘として出る。
7. **採否を決める。** 期待結果・外から見える振る舞い・前提・保証を変える選択は、利用者に決めてもらう。変えない選択は、既にある決定から導出して採用する（`ppdev derive`）。

## 試作

事実を調べるための小さな実験。例：「取消を送ったあとでも、回答は届くか」を確かめる数行のプログラム。

**頼まれたものの見た目や動きを、先に作ってみることは、試作ではない。** それは実装で、何を作るかを利用者が決めたあとに行う。画面の案を見せたいときは、図で見せる。動くものを見せたほうが早いと思うときは、作る前に、何を見せるために何を作るかを利用者に言い、了承を得る。

試作を行うとき：

- 作る前に、何を調べるかという問いを、利用者に一言伝える。
- 問いを残す。`ppdev new PRT --title "<問い>" --body "<やり方>"`
- 本番とは別の場所に出力する。
- 観測した条件と結果を残す。`ppdev new OBS --title "<分かったこと>" --body "<条件と結果>" --origin observed --source "<試作と実行>" --link observed-by:<PRT>`
- 試作が動いたことだけで、採用設計・本番コード・配布物に入れない。

本番へ取り込むときは、使う部分を特定し、利用者が決めた内容と、必要な検証を満たした変更として扱う。取り込んだことを設計の記録に書く。

## 本番の実装

`prepare-dev` の「本番の実装を始める条件」を確かめてから始める。

1. 採用済みの設計と受入条件に合わせて実装する。担当に書かせるときの役割は「実装」。決定済みの期待結果へ合わせる修正は、聞き直さずに進める。
2. 新しい意味の選択が出てきたら、実装で決めない。`prepare-requirements` へ戻す。
3. テストを書き、実行する。採用済みの具体例は、テストとして実行できる形にする。
4. コードと設計の対応を残す。
   `ppdev new OBS --title "<設計> を実装した場所" --body "<ファイルと関数>" --origin observed --source "<実装の差分>" --link implements:<DES@版>`
5. `prepare-review-code` へ渡す。必須の保証があれば `prepare-verify` へ渡す。

検証で違反が見つかり、実装の不具合と判定されたら、ここで直す。直したあとは実装レビューへ戻す。

## 引き継いでいないもの

設計と比較の手順は、pstack の architect と arena を参考にしている。次のものは引き継いでいない。

- 設計がまとまったら確認なしで実装へ進むこと。
- 設計の方向が増えたら担当を自動で増やすこと。
- 指定したモデルが使えないとき、別のモデルや親のモデルへ切り替えること。

仕事に合わせて思考量を上げ下げする決まりは、pstack と同じものを使う。担当の起動は、すべて `prepare-dev` の「担当を起動するとき」を通す。
