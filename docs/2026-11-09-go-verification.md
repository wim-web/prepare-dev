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
| RUN-0026 | gotest 0.2.0 | SPEC-0001 C1〜C6、SPEC-0003 V1〜V4、SPEC-0004 U2・A1・B1・D1・P1・P2・P3 | verified（103 テストすべて成功、`go test` の終了コード 0）。入力 201 ファイル、ディレクトリの一覧 21。`scope.bounds`: C4 は `model/traces/depth-6.jsonl` の 2,000 本・12,000 手を不一致 0、e2e は checkout からビルドした実行ファイルを検査、照合したモデルの digest |
| RUN-0027 | lean4 0.2.0 | SPEC-0001 C1・C2・C3・C5 | verified（`lake build`、公理は propext・Classical.choice・Quot.sound だけ） |
| RUN-0022 | vitest 0.3.0 | SPEC-0004 U1 | verified（`ui/` を project_dir にして `ui/node_modules/.bin/vitest` で実行、終了コード 0） |

RUN-0026 の対象は、`internal/core`・`internal/store`・`internal/runtime`・`internal/server`・`internal/plugin/vitest`・`internal/plugin/gotest`・`internal/uibuild`・`internal/e2e` のテストファイル。e2e は、テストの中で `./cmd/prepare-dev` をビルドし、別の場所に置いて PATH を `/usr/bin:/bin` にして動かす（実際の Lean による検証を含む）。RUN-0011〜0025 は、記録を改訂する前の版や前の版のプラグインで取った同じ内容の RUN で、履歴として残す（RUN-0017〜0019 が 85477c7 の時点の証拠で、プラグインの版が違うので今の版には当てはまらない。RUN-0020〜0025 は、下の追加の修正と README の整理の前の証拠）。RUN-0022（vitest）は、その後に入力も仕様も変わっていないので、今も当てはまる。記録した後の `status` は、未処理の回答 0、回答待ちの質問 0、未対処のレビューの指摘 0、問題 0。

## 人が実行して確かめたこと

### Lean のモデル

`model/bin/test` が成功した。禁止した記法が無いこと、`lake build`、SPEC-0001 の 8 つの定理の公理の監査、保存している操作列（`traces/depth-4.jsonl`、`depth-6.jsonl`、`recovery.jsonl`）がモデルから作り直したものと一致すること。一致したモデルの digest を `traces/model.sha256` に書き、Go のテスト（RUN-0026 の C4・C5）がそれを今のモデルと照合した。

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

## 85477c7 の後に直した不具合（2026-11-09 夜）

85477c7 のレビューで、既存のテストが通るまま次の三つが再現された。どれも同じ種類の見落としを周りに持っていたので、あわせて直した。直した後に、会話履歴の無いレビュー役三人（理解、設計の批評、実装の準備）が三回（REV-0062〜0064、REV-0065〜0067、REV-0068〜0070）さらに穴を見つけたので、それも直した。三回目でレビューの回数の上限に達し、人が判断画面の Q-0029 で「残る四つを直して完了へ進む（再確認はしない）」を選んだ（DEC-0009）。三回目の四つの直しは、会話履歴の無いレビュー役は見ておらず、実際に動かすテストでだけ確かめている。

1. **追加したファイルを見逃して、古い成功が「現在も有効」のままになる（SPEC-0003 V3）。** `//go:embed assets/*` は一致したディレクトリの下をすべて取り込むが、一覧は `assets/` の段だけだったので、`assets/nested/` にファイルを足すとテストは失敗するのに、前の RUN が当てはまったままだった。同じ見落としが、テストファイルの `//go:embed`（`go list` の `TestEmbedPatterns`・`XTestEmbedPatterns` にだけ出る）、ワイルドカードに一致する `.` で始まる名前と新しいディレクトリ、`all:` の下の `.` で始まる名前、ディレクトリのパターンの下の `node_modules`、`testdata/` の `.` で始まるファイルにもあった。go:embed の規則に合わせて一覧を取るようにし、一覧に数え方（既定、すべての名前、go:embed の規則）と名前のパターンを持たせた。
2. **消した decision の復元が、次の起動で書き込みを止める（SPEC-0001）。** 確定済みの decision のファイルを消すと起動時に journal から書き直すが、版を 1 からにしていたので、次の起動のジャーナルの検査が「同じ記録の二度の作成」（二つの作業コピーのマージ）と読んで書き込みを止めた。書き直すときは journal にある最後の版の次にするようにした。前の版（TypeScript 版と 85477c7）が 1 から書き直した decision も、ちょうど一つの取引が採番したものなら二度の作成に数えない（レビューの指摘）。その代わり、片方の作業コピーが回答から、もう片方が record create で同じ番号の decision を作ったマージは、decision のファイルの txid をその番号を採番した取引と照らして見つける（手で作った側のファイルを残すと書き込みを止める）。質問と回答のファイルは journal から戻せない。そこで起動時に、何かを書く前にすべての取引（確定済み、取り消し、進行中）の質問のファイルと回答を確かめ、欠けた取引があれば回復を始めず、何も書かずに（外部編集の記録も含めて）起動して status に戻すファイルを示すようにした。前は、確定済みの取引では decision だけを書き直してから「question … not found」で起動が止まり、取り消す取引では abort を書いて起動した後に status と質問の操作が失敗し、回答の入る前の版を戻すと回答が黙って無くなった。このリポジトリのジャーナル（305 行）には rev 1 の重複が無いことを確かめた。この機械には、ほかに prepare-dev のジャーナルは無い。
3. **テストの外の失敗を verified にする（SPEC-0003 V4、新設）。** テストが成功した後に `TestMain` が 1 で終わっても、RUN が verified になった。Vitest にも同じ見落としがあった。テストの外の失敗があれば、それが起きたパッケージかファイルのテストの主張を failed にし、起きた場所が分からなければどの主張も verified にしないようにした。レビューの指摘で、次も直した。
   - Vitest: 読み込めないファイル（テストが一つも無い）を、参照のファイルの部分が一致する主張に結びつける。未処理の例外と describe の中の hook の例外は JSON のレポートに出ず、ほかのファイルやテストが失敗した実行では終了コードからも見えないので、実行のたびに小さな reporter を加え、未処理の例外、ファイルと suite のエラー、isolate の設定を Vitest から受け取る。isolate が無効の project があるときは、未処理の例外を起きた場所の分からないものとして扱う。Vitest 3 より前の版の reporter のフック（onFinished）でも、受け取るファイルのタスクの木から同じものを読む（実際の Vitest では確かめていないので、その経路で読んだときは要約にそう書く）。globalSetup の後始末の例外は reporter に届かないので、ほかの失敗がある実行では見分けられない（仕様の対象外として書いた）。
   - Go: テストの外の panic は、同じパッケージにテストの失敗があっても見分ける。テストのプログラムがすべてのテストを走らせる前に終わったパッケージ（結果の出なかったテスト: ほかのテストが残した goroutine の panic で、そのテストの途中で終わった。テストの中の panic: 後ろに並ぶテストは走らない）は、どのテストが走らなかったか分からないので、ビルドの失敗と同じく、参照のパッケージの部分が一致する主張を failed にする。ビルドの失敗では、コンパイラの最初のエラーを、テストを走らせる前のほかの失敗では、テストの外の panic の行か最後の行を要約に残す。失敗したテストのあるパッケージで、panic でないテストの外の失敗（`TestMain` が自分の検査で 0 以外を返すなど）は区別できないので、仕様の対象外として書いた。
   - 前の版のプラグイン（gotest 0.1.0、vitest 0.2.0）が記録した RUN は、今の規則を経ていないので当てはまらないものにした（プラグインの版は gotest 0.2.0、vitest 0.3.0）。検証器の終了コードを RUN の `tool.extra.exit_code` に残す。

lean4 は、ビルドが失敗すると主張を verified にしないことを確かめた（変更なし）。

その後の外部レビューの再確認で、前の三つが直っていることが確かめられ、新しく一つ見つかった。質問の JSON が壊れていると、status 自体が失敗する。起動時には回復できない取引として見つけるのに、status が質問の一覧の読み込みで止まり、戻すファイルの案内を出せなかった（質問の一覧は、読めないファイルが一つあると全体が失敗していた）。読めない質問のファイルは、質問の一覧・未処理回答・trace から外し、status の problems に示すようにした（記録の読めないファイルと同じ扱い）。どの取引も使っていなければ書き込みは止めない。実際のデーモンで、壊れた質問のファイルを status が problems に示し、ほかの質問を回答待ちとして表示することと、質問の一覧がほかの質問だけを返すことを確かめた。

その次の再確認で、さらに二つ見つかった。一つ目: 書き込みを止めている間（回復できない取引があるとき）に要求を編集すると、記録を古い sha256 で読んでいたので、古い RUN が今も当てはまると示した（`applicable=true`）。止まっている間も外部編集はジャーナルに記録しないが、記録はファイルの今の内容の sha256 で読むようにした。二つ目: 読み取りの権限が無い質問のファイルでは、まだ status が失敗した（質問の一覧と回復待ちの判定が、壊れた JSON などの決まった誤りしか飛ばしていなかった）。ストアが使えなくなったとき以外の誤りは、読めないファイルとして扱うようにした。どちらもテストで確かめた。実際のデーモンでの確認は、そのためのスクリプト（判断画面の API への回答の POST、ファイルの削除と権限の変更を含む）が自動の許可判定で止められたので行っていない。

直した後の証拠は RUN-0022・0026・0027（上の表）。e2e は checkout からビルドした実行ファイルで動かした。上の「配布物」と Linux の確認、TypeScript 版との共存は 85477c7 の時点のもので、この修正の後に配布物は作り直していない。

### 実際の Vitest 5.1.0 で確かめたこと

`ui/node_modules` の Vitest 5.1.0 を、リポジトリの外の一時ディレクトリで動かした。

- `afterAll` が例外を投げたファイル: レポートのファイルは `status` が `failed`、`message` にその例外、テストはすべて `passed`。同じファイルのテストが失敗していても `message` は残る。
- 存在しないモジュールを import するファイル: `status` が `failed`、`message` に「Cannot find module …」、`assertionResults` は空。
- テストの後に `setTimeout` から例外を投げるファイル（未処理の例外）: そのファイルだけなら、終了コードは 1、レポートは `success: true` で、失敗したテストもファイルも無い。ほかのファイルのテストが失敗する実行では、終了コードは 1 で、未処理の例外はレポートにも標準出力にも出ない。
- reporter の `onTestRunEnd(testModules, unhandledErrors)` は未処理の例外を受け取り、例外は起きたテストファイル（`VITEST_TEST_PATH`）とテスト（`VITEST_TEST_NAME`）を持つ。`--reporter=json --outputFile=… --reporter=<reporter のファイル>` で、JSON のレポートと reporter を並べて使える。
- プラグインを通した結果（上の五つのファイルと、参照の前半が空の主張を一度に実行）: 未処理の例外のファイル、失敗したテスト、読み込めないファイル、`afterAll` とテストの失敗が同居するファイルの主張はどれも failed、ほかのファイルの主張は verified、参照の前半が空の主張は読み込めないファイルのために failed。要約にそれぞれの理由（「unhandled error: late failure」「Cannot find module …」「teardown broke」）、`tool.extra.exit_code` に 1。
- describe の中の `afterAll` の例外は、同じファイルのほかのテストが失敗すると、レポートのファイルの `message` が空になる。reporter の `onTestRunEnd` が受け取る `testModules` の `children.allSuites()` の `errors()` には入っていて、プラグインを通すと、その describe の主張は failed、理由は「nested teardown broke」。ファイル直下の `afterAll` の例外も同じ経路で一つだけ届く。
- `vitest.config.mjs` に `isolate: false` を書いた project では、reporter の `onInit` が受け取る project の `config.isolate` が false で、プラグインはそれを読み取る。
- `globalSetup` が返した後始末が例外を投げると、終了コードは 1 だが、reporter の未処理の例外にもファイルのエラーにも入らない。ほかのファイルのテストが失敗する実行では、成功したテストの主張は verified のまま。
- 5.1.0 は reporter の `onTestRunEnd` を呼び（出力の `hook` が `onTestRunEnd`）、`onFinished` は呼ばない。`onFinished` の経路は、Vitest 2 までのファイルのタスクの形（`filepath`、`tasks`、`result.errors`）を模した入力を node で reporter に与え、ファイルのエラー、二段の describe のエラー、未処理の例外とそのファイルが書き出されることを確かめた。

### 再現したテスト

直す前の実装で失敗し、直した後に通る。

- `internal/plugin/gotest` の `TestEmbedListings`: レビューの再現と同じ形（`assets/*` の下の段に足すと実際のテストが失敗し、前の RUN が当てはまらなくなる）と、パターンごとにファイルを一つ足して、一覧が変わるのは `go list` が取り込むファイルが変わるときであることを `go list` の結果と比べる 14 通り。
- `internal/plugin/gotest` の `TestPackageFailures`: `TestMain` が 1 で終わるパッケージ、テストを走らせる前に終わる `TestMain`、失敗したテストのあるパッケージでのテストの外の panic、テストの中の panic（後ろに並ぶテストの主張も failed）、ほかのテストが残した goroutine の panic、ビルドの失敗を実際に実行する。説明できない 0 以外の終了コードは、作った出力を与える。
- `internal/plugin/vitest` の `TestFailuresOutsideTests`: 上の Vitest の形（describe の中の `afterAll`、isolate が無効のときの未処理の例外を含む）。
- `internal/runtime` の `[C3]`「a deleted decision file is written again」: 確定済みの適用と段階 b の後で中断した適用のそれぞれで decision のファイルを消し、起動し直すと次の版で書き直され、続く起動が書き込めること。
- `internal/store` の `[C3]`: 前の版が rev 1 で書き直した decision を二度の作成に数えず、二つの取引の decision と、手で作った記録の二度の作成は見つけること。
- `internal/runtime` の `[C3]`「a transaction whose question file is gone or lacks its answer」: 確定済みの取引、取り消す取引、回答の入る前の版を戻した場合、質問のファイルが壊れているか読み取りの権限が無い場合のそれぞれで、起動して何も書かずに status に戻すファイルを示し、ファイルを戻せば回復すること。「the question file of an aborted, cleaned-up transaction」: 取り消しと清掃が済んだ取引の質問のファイルが読めなくても、status が失敗せず回復待ちと答えること。
- `internal/runtime` の `[V1]`「while nothing is written」: 回復できない取引があって書き込みを止めている間に要求を編集すると、前の RUN が当てはまらなくなること。
- `internal/runtime` の「a question file that cannot be read is a problem」と「…for its permissions…」: どの取引も使っていない質問のファイルが壊れているか読み取りの権限が無くても、status、質問の一覧、trace はほかの質問で動き、そのファイルを problems に示し、書き込みは止めないこと。「a decision of the same number made with record create」: 回答から作った decision と record create で作った同じ番号の decision のマージを起動時に見つけて、書かないこと。
- `internal/runtime` の `[V1]`: 前の版のプラグインが記録した RUN が当てはまらないこと。

レビューの再現ログのテスト（`/tmp/prepare-dev-review-repros`）も、`go test -overlay` で三つとも通る。

記録: SPEC-0001 rev 15、SPEC-0003 rev 14（V4 を追加）、レビュー REV-0062〜0070、判断 DEC-0009（Q-0029）。

## 確かめていないこと

- Linux の実機での実行（コンテナだけ。amd64 はエミュレーション）と、Intel の Mac での実行（Rosetta だけ）。
- Windows（対象外）。
- Codex と Claude Code がそれぞれ Go 版の CLI で全工程を進めること。skills は CLI のコマンドを使うように直したが、新しいセッションで通しては試していない。
- 古い MCP の登録で起動した TypeScript 版の `prepare-dev mcp` のプロセスが、このリポジトリを開いていた既存の Codex と Claude Code のセッションに残っている。TypeScript のソースを消したので、それらは新しいデーモンを起動できず、Go 版のデーモンには別の build として接続を拒まれる。セッションを閉じると消える。
- ブラウザでの確認は Chromium 系の一つだけ。
