# minidb

連載「DBRE への道」第 3 部で作っている、教材用の自作データベース minidb です。本リポジトリは **第 17 回** 時点のコードにあたります。

第 17 回時点では、次のところまでを実装しています。

- **ページ** — 固定長 8KB（PostgreSQL に合わせた `PageSize`）のバイト列。
- **スロット付きページ** — ページ内に可変長レコードを詰め、削除跡を `compact` で回収する。
- **ディスクマネージャ** — ファイルをページ単位で読み書きし、ページ番号で位置を決める。
- **ヒープファイルと全件走査** — ページを並べてレコードを溜め、全ページ・全スロットを順にたどる Seq Scan 相当の走査を行う。
- **バッファプール** — 限られたフレームにページを載せ、ヒットならディスクへ行かずに返す。clock で置換し、書き換えた（dirty な）ページは flush で書き戻す。
- **B+tree** — キーで RecordID（行の住所、ctid に当たる）を引く索引。ノードを 1 ページとしてバッファプール越しに読み書きする。葉に（キー, RecordID）と次の葉への横ポインタ、内部ノードに仕切りキーと子ページ番号を持つ。挿入で分割（葉は先頭キーを写し上げ、内部は中央キーを押し上げ）、削除で兄弟からの借用・併合・木の縮約を行い、葉の横つながりをたどる順序付き走査もできる。
- **MVCC** — トランザクション番号（XID）とスナップショットで「いつ何が見えるか」を決める多版同時実行制御。行はバージョンの列として持ち、更新は旧バージョンに削除印（xmax）を付けて新バージョンを足す。Begin 時点で進行中だったトランザクションや未確定の変更は見えず、スナップショット分離を満たす。どの現役トランザクションからも見えなくなった不要タプル（dead version）を数える `DeadVersions` も持ち、これが第 14 回の VACUUM が回収する対象になる。
- **クエリ処理** — Volcano モデルのイテレータ（開く・一件出す・閉じるの三つで揃えた処理段）を積み重ね、てっぺんから一件ずつ引く実行器。表を頭から読む `SeqScan` は第 12 回の可視性をくぐらせ、見える行だけを返す。条件で振り分ける `Filter`、要る列だけ残す `Project` を上に重ね、`Run` でてっぺんから尽きるまで引くと、`SELECT name FROM users WHERE age > 30` が表を一周しながら一件ずつ流れる。途中に全件を抱えないのが基本で、PostgreSQL の EXPLAIN に出るあのインデントされた木と同じ積み方になる。
- **VACUUM と番号の周回** — 書き換えや削除で残った古い版を実際に表から取り除く回収。回収してよい境界は、いちばん古い現役のトランザクション（`OldestActive`）が決め、それより前に消された版だけを `Vacuum` が捨てて行を詰め直す。古いトランザクションが一本でも開いていると境界がそこで止まり、回収できないまま版が溜まる。あわせて、トランザクション番号が 32 ビットで一周すると過去が未来に見える「周回」を、前後を大小ではなく距離（`int32(a-b)` の符号）で決める `xidPrecedes` の小さなモデルで再現し、古い行の作成番号を特別な `FrozenXID` に置き換える `freeze`（凍結）で、一周しても過去のまま見え続けるようにする。PostgreSQL の autovacuum が背後で回している、回収と凍結の二つの仕事に当たる。
- **EXPLAIN とプランナ** — 同じ等値条件に対する全件走査と索引走査に費用の数字をつけ、安いほうの実行の木を選ぶ費用ベースのプランナ。第 10 回の B+tree を列の値から行 ID への索引にした `IndexScan` を足し、全件走査と同じ三つの約束（開く・一件出す・閉じる）で差し替えられるようにする。当たる行数は列の異なり数から見積もり（`Stats` / `estimateRows`、一意な列なら一行、二種類しかない列なら半分）、順番読み 1 件を 1.0・索引経由 1 件を 4.0（PostgreSQL の `seq_page_cost` と `random_page_cost` の既定比）として費用を比べる。`PlanEquals` は一意な `id` には Index Scan を、二種類しかない `city` には索引があっても Seq Scan を選ぶ。索引が使われないのは壊れているからではなく、当たる行が多すぎて拾い読みより全件順読みが安いと見積もりが言うから。`Plan` の `Cost` と `EstRows`、`Explain` の一行は、PostgreSQL の `EXPLAIN` に出る cost と rows そのもの。
- **チューニングの原理** — 限られたメモリをバッファプールと接続でどう分けるかを、手元の計測で裏づける回。第 9 回のバッファプールに参照列を流してヒット率を測る `MeasureHitRate` と、よく触る一部に参照を集めた偏りのある参照列を作る `LocalReferences` で、容量を増やすほどヒット率は上がるが、よく触る一部が収まったあとは伸びが鈍ることを数で見せる（容量 2 で 0.16、10 で 0.65、40 で 0.91、100 で 0.98）。容量を決める基準は、全データ量ではなく、よく触る一部の大きさ。あわせて、接続ごとにプロセスとメモリを持つ PostgreSQL を `BackendMemory`（メモリは接続数に比例）で、接続プール（PgBouncer 相当）が奥の接続を上限に束ねて総メモリを頭打ちにするさまを `PoolingCapsBackends`（千接続を五十に、10000 MiB を 500 MiB に）でモデル化する。`shared_buffers` の効きは `pg_stat_database` のヒット率で測り、`work_mem` は接続数との積で見て、接続は増やす前にプールで絞る、という運用判断につながる。
- **観測（USE と RED）** — 測れる値が多すぎる `pg_stat_*` を、二つの型に落として読む回。処理は RED（流量・失敗・所要時間）で、実行のたびに所要時間と成否を積む `REDStat` が `Count`・`ErrorRate`・`AvgDuration` を返す。資源は USE（使用率・飽和・エラー）で、バッファプールを `BufferPoolUSE`（使用率＝載っているページ数／フレーム数、飽和＝第 16 回のミス率）、表を `StorageUSE`（飽和＝生きた版に対する不要タプルの割合、第 14 回の回収が止まると上がり `Vacuum` で下がる）で表す。使用率はバッファプールが動けば満杯で 1 に張りつき詰まりを表さないので、見るのは飽和（取りこぼし・待ち・溜まり）。PostgreSQL では RED を `pg_stat_statements` の `calls`・`mean_exec_time` と `xact_rollback` に、USE を `blks_read` の割合・`n_dead_tup` の比・待ちの数に割り当て、RED で処理の異常に気づいて USE で詰まった資源にたどり着く、という読む順に結びつく。

## 必要なもの

- Go 1.26 以降

## 試し方

```sh
git clone https://github.com/engineers-hub-ltd-in-house-project/minidb.git
cd minidb

# テスト
go test ./...

# デモ（一時ファイルに 1000 件入れて全件走査する）
go run ./cmd/minidb
```

コマンドとして手元に入れて試すこともできます。

```sh
go install github.com/engineers-hub-ltd-in-house-project/minidb/cmd/minidb@latest
minidb
```

### 実際の出力

`go test ./... -v` の出力:

```
=== RUN   TestBTreeInsertAndSearch
--- PASS: TestBTreeInsertAndSearch (0.74s)
=== RUN   TestBTreeScanIsSorted
--- PASS: TestBTreeScanIsSorted (0.44s)
=== RUN   TestBTreeDeleteWithMerge
--- PASS: TestBTreeDeleteWithMerge (0.48s)
=== RUN   TestBTreeRandomizedAgainstMap
--- PASS: TestBTreeRandomizedAgainstMap (2.30s)
=== RUN   TestBufferPoolWriteReadBack
--- PASS: TestBufferPoolWriteReadBack (0.00s)
=== RUN   TestBufferPoolEvictionFlushesDirty
--- PASS: TestBufferPoolEvictionFlushesDirty (0.03s)
=== RUN   TestBufferPoolAllPinnedReturnsError
--- PASS: TestBufferPoolAllPinnedReturnsError (0.01s)
=== RUN   TestSlottedPageInsertGet
--- PASS: TestSlottedPageInsertGet (0.00s)
=== RUN   TestSlottedPageDeleteAndReuse
--- PASS: TestSlottedPageDeleteAndReuse (0.00s)
=== RUN   TestHeapFileInsert1000AndScan
    heap_test.go:111: inserted 1000 records across 2 pages, scan returned 1000
--- PASS: TestHeapFileInsert1000AndScan (1.78s)
=== RUN   TestSeqScanVisibility
--- PASS: TestSeqScanVisibility (0.00s)
=== RUN   TestFilter
--- PASS: TestFilter (0.00s)
=== RUN   TestProjectAndPipeline
--- PASS: TestProjectAndPipeline (0.00s)
=== RUN   TestPlannerPicksIndexForSelective
--- PASS: TestPlannerPicksIndexForSelective (0.35s)
=== RUN   TestPlannerPicksSeqForNonSelective
--- PASS: TestPlannerPicksSeqForNonSelective (0.36s)
=== RUN   TestPlannerFallsBackWithoutIndex
--- PASS: TestPlannerFallsBackWithoutIndex (0.34s)
=== RUN   TestPlanExplainShowsChoice
--- PASS: TestPlanExplainShowsChoice (0.34s)
=== RUN   TestHitRateRisesWithDiminishingReturns
    tuning_test.go:47: hit rate: pool=2 0.159, 10 0.652, 40 0.908, 100 0.980
--- PASS: TestHitRateRisesWithDiminishingReturns (0.33s)
=== RUN   TestPoolingCapsBackendMemory
--- PASS: TestPoolingCapsBackendMemory (0.00s)
=== RUN   TestREDRecordsRateErrorsDuration
--- PASS: TestREDRecordsRateErrorsDuration (0.00s)
=== RUN   TestBufferPoolUSEReflectsFillAndMisses
--- PASS: TestBufferPoolUSEReflectsFillAndMisses (0.33s)
=== RUN   TestStorageUSEReflectsDeadTuples
--- PASS: TestStorageUSEReflectsDeadTuples (0.00s)
=== RUN   TestVacuumReclaimsDeadVersions
--- PASS: TestVacuumReclaimsDeadVersions (0.00s)
=== RUN   TestVacuumBlockedByOldTx
--- PASS: TestVacuumBlockedByOldTx (0.00s)
=== RUN   TestXIDWraparoundHidesUnfrozenRow
--- PASS: TestXIDWraparoundHidesUnfrozenRow (0.00s)
=== RUN   TestFreezeSurvivesWraparound
--- PASS: TestFreezeSurvivesWraparound (0.00s)
PASS
ok  	github.com/engineers-hub-ltd-in-house-project/minidb	8.981s
?   	github.com/engineers-hub-ltd-in-house-project/minidb/cmd/minidb	[no test files]
```

`go run ./cmd/minidb` の出力:

```
inserted 1000 records across 2 pages, scan returned 1000
```

## ファイル構成

| ファイル | 役割 |
| --- | --- |
| `page.go` | スロット付きページ（ページ内のレコード配置と詰め直し） |
| `disk.go` | ページの入出力（ファイルをページ単位で読み書き） |
| `heap.go` | ヒープファイルと全件走査（レコードの置き場所と Seq Scan） |
| `heap_test.go` | ページ／ヒープファイルのテスト |
| `buffer.go` | バッファプール（フレーム管理・clock 置換・dirty/flush） |
| `buffer_test.go` | バッファプールのテスト |
| `btree.go` | B+tree 索引（探索・挿入・分割・削除・借用・併合・縮約・順序走査） |
| `btree_test.go` | B+tree のテスト（挿入探索・順序走査・削除併合・ランダム照合） |
| `mvcc.go` | MVCC（XID・スナップショット・バージョン可視性・dead version の計数） |
| `mvcc_test.go` | MVCC のテスト（スナップショット分離・自己更新・版分岐・削除可視性・abort・DeadVersions） |
| `executor.go` | クエリ処理（Volcano モデルのイテレータ：SeqScan・Filter・Project と Run） |
| `executor_test.go` | クエリ処理のテスト（可視性走査・条件振り分け・三段パイプライン） |
| `vacuum.go` | VACUUM（不要タプルの回収・OldestActive 境界・32 ビットの周回モデルと凍結） |
| `vacuum_test.go` | VACUUM のテスト（回収・古い tx による回収停止・周回での消失・凍結での生存） |
| `planner.go` | EXPLAIN とプランナ（索引走査・異なり数からの行数見積もり・費用比較で安い木を選ぶ） |
| `planner_test.go` | プランナのテスト（選択性が高い時の索引走査・低い時の全件走査・索引なしの退避・Explain） |
| `tuning.go` | チューニングの原理（ヒット率の計測・偏りのある参照で容量とヒット率の鈍り・接続に比例するメモリとプールによる頭打ち） |
| `tuning_test.go` | チューニングのテスト（容量を上げるとヒット率の伸びが鈍る・プールで backend と総メモリが頭打ち） |
| `metrics.go` | 観測（処理を RED／資源を USE で表す・バッファプールの飽和＝ミス率・表の飽和＝不要タプルの割合） |
| `metrics_test.go` | 観測のテスト（RED の積み上げ・プール満杯で使用率 1 と飽和・不要タプルで表の飽和が上下） |
| `cmd/minidb/main.go` | 1000 件入れて全件走査するデモ |

## バージョニング

連載の回ごとにタグ（`v0.10` のような形）を打って、各回の状態をあとからたどれるようにする方針です。第 17 回時点ではまだタグを打っていません。

## 注意

これは連載の教材用に、仕組みを追えることを優先した最小実装です。本番用のデータベースではありません。

## ライセンス

MIT License. 詳細は [LICENSE](LICENSE) を参照してください。
