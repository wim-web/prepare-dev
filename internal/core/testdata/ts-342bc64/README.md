# TypeScript 版が書いたファイルの写し

commit 342bc64（TypeScript 版の最後のコミット）の `prepare/` から、記録（Markdown）、質問と RUN の JSON、ジャーナルをそのまま写したもの。`internal/core` のテスト（SPEC-0004 D1）が使う。

- 記録と質問: 読み直して書き出しても TypeScript 版とバイト単位で同じになることを確かめる（runtime が書き直すときの差分を、変えた項目だけにするため）。`cancelled` の無い古い質問は `cancelled: null` が加わる（TypeScript 版も同じ）。
- RUN とジャーナル: 書き直さないので書き方は問わず、読んで欠ける項目がないことを確かめる。

中身を編集しない。
