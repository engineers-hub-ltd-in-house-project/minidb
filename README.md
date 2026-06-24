# minidb

連載「DBRE への道」第 3 部で作っている、教材用の自作データベース minidb です。本リポジトリは **第 12 回** 時点のコードにあたります。

第 12 回時点では、次のところまでを実装しています。

- **ページ** — 固定長 8KB（PostgreSQL に合わせた `PageSize`）のバイト列。
- **スロット付きページ** — ページ内に可変長レコードを詰め、削除跡を `compact` で回収する。
- **ディスクマネージャ** — ファイルをページ単位で読み書きし、ページ番号で位置を決める。
- **ヒープファイルと全件走査** — ページを並べてレコードを溜め、全ページ・全スロットを順にたどる Seq Scan 相当の走査を行う。
- **バッファプール** — 限られたフレームにページを載せ、ヒットならディスクへ行かずに返す。clock で置換し、書き換えた（dirty な）ページは flush で書き戻す。
- **B+tree** — キーで RecordID（行の住所、ctid に当たる）を引く索引。ノードを 1 ページとしてバッファプール越しに読み書きする。葉に（キー, RecordID）と次の葉への横ポインタ、内部ノードに仕切りキーと子ページ番号を持つ。挿入で分割（葉は先頭キーを写し上げ、内部は中央キーを押し上げ）、削除で兄弟からの借用・併合・木の縮約を行い、葉の横つながりをたどる順序付き走査もできる。
- **MVCC** — トランザクション番号（XID）とスナップショットで「いつ何が見えるか」を決める多版同時実行制御。行はバージョンの列として持ち、更新は旧バージョンに削除印（xmax）を付けて新バージョンを足す。Begin 時点で進行中だったトランザクションや未確定の変更は見えず、スナップショット分離を満たす。どの現役トランザクションからも見えなくなった不要タプル（dead version）を数える `DeadVersions` も持ち、これが第 14 回の VACUUM が回収する対象になる。

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
PASS
ok  	github.com/engineers-hub-ltd-in-house-project/minidb	5.794s
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
| `cmd/minidb/main.go` | 1000 件入れて全件走査するデモ |

## バージョニング

連載の回ごとにタグ（`v0.10` のような形）を打って、各回の状態をあとからたどれるようにする方針です。第 12 回時点ではまだタグを打っていません。

## 注意

これは連載の教材用に、仕組みを追えることを優先した最小実装です。本番用のデータベースではありません。

## ライセンス

MIT License. 詳細は [LICENSE](LICENSE) を参照してください。
