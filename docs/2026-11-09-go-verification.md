# Go 版の確認記録（2026-11-09）

prepare-dev の本体を Go に移した変更（DEC-0006、DEC-0007、設計 DES-0004）を、何で、どこまで確かめたかの記録。主張の証拠はツールの出力から作った RUN（`prepare/runs/`）で、この文書は人が実行して確かめたことを残す確認記録である。RUN の代わりにはならない。

## 環境

| 項目 | 版 |
| --- | --- |
| OS | macOS 26.2（arm64） |
| Go | go1.27.3 darwin/arm64 |
| Lean | 4.35.0、Lake 5.0.0（`model/lean-toolchain`） |
| Node.js・pnpm（判断画面の開発だけ） | v26.13.0、12.16.1 |
| Linux の確認 | Docker 29.9.2（linux/arm64）、手元にあった `redis:7-alpine`（linux/arm64）を入れ物としてだけ使用。amd64 はエミュレーション |

## 主張の証拠（RUN）

checkout からビルドしたもので、`.prepare-dev/config.json` の設定を使って `prepare-dev verify run` で記録した。記録した時点では、三つとも今の版に当てはまる（applicable、不一致 0）。

| RUN | プラグイン | 主張 | 結果 |
| --- | --- | --- | --- |
| RUN-0017 | gotest | SPEC-0001 C1〜C6、SPEC-0003 V1〜V3、SPEC-0004 U2・A1・B1・D1・P1・P2・P3 | verified（77 テストすべて成功）。入力 201 ファイル、ディレクトリの一覧 21。`scope.bounds`: C4 は `model/traces/depth-6.jsonl` の 2,000 本・12,000 手を不一致 0、e2e は checkout からビルドした実行ファイルを検査、照合したモデルの digest |
| RUN-0018 | lean4 | SPEC-0001 C1・C2・C3・C5 | verified（`lake build`、公理は propext・Classical.choice・Quot.sound だけ） |
| RUN-0019 | vitest | SPEC-0004 U1 | verified（`ui/` を project_dir にして `ui/node_modules/.bin/vitest` で実行） |

RUN-0017 の対象は、`internal/core`・`internal/store`・`internal/runtime`・`internal/server`・`internal/plugin/vitest`・`internal/plugin/gotest`・`internal/uibuild`・`internal/e2e` のテストファイル。e2e は、テストの中で `./cmd/prepare-dev` をビルドし、別の場所に置いて PATH を `/usr/bin:/bin` にして動かす（実際の Lean による検証を含む）。RUN-0011〜0016 は記録を改訂する前の版に対する同じ内容の RUN で、履歴として残す。記録した後の `status` は、未処理の回答 0、回答待ちの質問 0、未対処のレビューの指摘 0、問題 0。

## 人が実行して確かめたこと

### Lean のモデル

`model/bin/test` が成功した。禁止した記法が無いこと、`lake build`、SPEC-0001 の 8 つの定理の公理の監査、保存している操作列（`traces/depth-4.jsonl`、`depth-6.jsonl`、`recovery.jsonl`）がモデルから作り直したものと一致すること。一致したモデルの digest を `traces/model.sha256` に書き、Go のテスト（RUN-0017 の C4・C5）がそれを今のモデルと照合した。

### 配布物

`go run ./scripts/release -version 0.2.0` で作った 4 つの tar.gz（`dist/artifact.json`）。

| 対象 | 実行ファイル（バイト） | tar.gz（バイト） | 実行ファイルの sha256 |
| --- | --- | --- | --- |
| darwin/arm64 | 12,707,490 | 5,328,091 | 4a5dc4816dc4085547d92ef8ffe5287dc63f52ec74d46dcca705724e469e5256 |
| darwin/amd64 | 13,948,736 | 5,785,303 | 1ca2000c96ebeb65864a970da1c895f3dff0aed356ff361e964021037696d198 |
| linux/arm64 | 12,386,464 | 5,144,624 | 452444bf1500136f738410797977ed2debdb02ed78b185e8ec014f7a00d94c44 |
| linux/amd64 | 13,668,512 | 5,701,782 | 4f51ec24a4fc6e257ca22b3abd403b7c9d1f2d17868e7aaecb12e7168a72957f |

- macOS: tar.gz から取り出した実行ファイルを `PREPARE_DEV_E2E_BINARY` に渡し、`go test ./internal/e2e` が全部通った（arm64 はそのまま、amd64 は Rosetta）。中身は、質問から判断と trace・bundle・ui までの流れ、skills のコピーと更新、書き手を一つにすること（同時起動、二つ目の serve、強制終了、シンボリックリンク経由、lease ファイルの削除、ジャーナルの置き換わり、マージしたジャーナル、TypeScript 版の残した daemon.json）、回答の経路と MCP の入口が無いこと、実際の Lean による検証、デーモンの強制終了からの回復。
- Linux: 取り出した実行ファイルを Alpine のコンテナ（ネットワークなし、PATH は `/usr/bin:/bin` で go・node・npm・pnpm が無いことを確認）で動かし、次が通った。arm64 はそのまま、amd64 はエミュレーション。
  - `version --json`、`init`（skills 7 つ、MCP の設定を作らない）
  - 記録と質問の作成、判断画面の配信、Origin の無い回答の拒否、Origin のある回答、`q answer` が無いこと
  - `q wait`・`ack`・`apply`（DEC-0001）、`trace`、`ui`、`bundle`
  - 二つ目の `serve` が lease で拒まれること、`kill -9` したデーモンの daemon.json が残っても次の `status` で起動し直すこと
  - lease と daemon.json を消したデーモンが 3 秒以内に終わり、次の `status` で新しいデーモンが起動すること

### TypeScript 版との共存と戻し方

commit 342bc64 を作業用の場所に展開し、`pnpm install --offline` と `pnpm ui:build` を実行して動かした（画面のビルドが無いと TypeScript 版のデーモンは起動しない）。Go 版は上の darwin/arm64 の配布物を使った。

- Go 版のデーモンが動いている root で TypeScript 版の CLI の `status`: 「daemon belongs to another build or installation」で拒み、デーモンを起動しない。
- TypeScript 版のデーモンが動いている root で Go 版の CLI の `status`: 相手の version・build・場所・pid を示して拒む。Go 版の `serve`: TypeScript 版の daemon.json と lease のポート（使用中）を示して拒む。どちらの場合も `serve` のプロセスは一つのまま。
- 戻し方: 今の `prepare/` の写しを TypeScript 版で開き、記録 88 件、質問 28 件、RUN-0017（listings は無視される）を読めた。TypeScript 版はファイルを一つも書き換えなかった。

この検出は相手の daemon.json があるときしか働かない移行期の対策で、0.3.0 で外す（DES-0004）。

### 独立したプロジェクトでの一連の利用（REQ-0010 P4）

配布物の実行ファイル（darwin/arm64）で空のディレクトリに `init` し、次を通した。

1. `record create` で REQ-0001 を作った。
2. `q create` で Markdown の比較表と推奨つきの質問 Q-0001 を作った。
3. 返った URL をブラウザ（Claude のアプリ内のブラウザ）で開き、比較表・推奨と理由・人に尋ねる理由が表示されることを見て、「4人まで」を選んで回答した。画面は「回答を保存しました」と「エージェント処理待ち」を示した。
4. `q wait`（answered）、`q ack`、`q apply` で DEC-0001 を作った。画面は読み込み直さずに「判断に反映済み」に変わった（SSE）。
5. DEC-0001 を根拠にした SPEC-0001 と Lean のモデルを書き、`verify run --plugin lean4` で RUN-0001 が verified（proof）になった。
6. `trace REQ-0001` が DEC-0001 と、SPEC-0001 の C1 の現在の証拠 RUN-0001 をつないだ。`status` は未処理 0、適用できない証拠 0。

### 二つの作業コピーで書いた記録のマージ

人の判断（DEC-0008）で、マージの衝突は普通のコンフリクトとして人が直すことにした。prepare-dev は、壊れた状態を書き進めないための検査だけを持つ。e2e で次を確かめた。

- 衝突の印が残ったジャーナルでは、デーモンが起動せず、CLI が 10 秒以内に理由を示す。
- 両側の行を残したジャーナルでは、`status` が矛盾を problems に示し、書き込みを拒む。
- 動作中にジャーナルが置き換わる（ブランチの切り替えなどで Git がファイルを作り直すのと同じ）と、デーモンは次の操作を書かずに拒んで止まり、次のコマンドが今のジャーナルで起動し直す。

実際の `git merge` での確認はしていない。

## 確かめていないこと

- Linux の実機での実行（コンテナだけ。amd64 はエミュレーション）と、Intel の Mac での実行（Rosetta だけ）。
- Windows（対象外）。
- Codex と Claude Code がそれぞれ Go 版の CLI で全工程を進めること。skills は CLI のコマンドを使うように直したが、新しいセッションで通しては試していない。
- 古い MCP の登録で起動した TypeScript 版の `prepare-dev mcp` のプロセスが、このリポジトリを開いていた既存の Codex と Claude Code のセッションに残っている。TypeScript のソースを消したので、それらは新しいデーモンを起動できず、Go 版のデーモンには別の build として接続を拒まれる。セッションを閉じると消える。
- ブラウザでの確認は Chromium 系の一つだけ。
