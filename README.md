# minidb

連載「DBRE への道」第 3 部で作っている、教材用の自作データベース minidb です。本リポジトリは **第 9 回** 時点のコードにあたります。

第 9 回時点では、次のところまでを実装しています。

- **ページ** — 固定長 8KB（PostgreSQL に合わせた `PageSize`）のバイト列。
- **スロット付きページ** — ページ内に可変長レコードを詰め、削除跡を `compact` で回収する。
- **ディスクマネージャ** — ファイルをページ単位で読み書きし、ページ番号で位置を決める。
- **ヒープファイルと全件走査** — ページを並べてレコードを溜め、全ページ・全スロットを順にたどる Seq Scan 相当の走査を行う。
- **バッファプール** — 限られたフレームにページを載せ、ヒットならディスクへ行かずに返す。clock で置換し、書き換えた（dirty な）ページは flush で書き戻す。

B+tree、ログとリカバリは、第 10 回以降でこのリポジトリに積み増していきます。

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
=== RUN   TestBufferPoolWriteReadBack
--- PASS: TestBufferPoolWriteReadBack (0.02s)
=== RUN   TestBufferPoolEvictionFlushesDirty
--- PASS: TestBufferPoolEvictionFlushesDirty (0.09s)
=== RUN   TestBufferPoolAllPinnedReturnsError
--- PASS: TestBufferPoolAllPinnedReturnsError (0.03s)
=== RUN   TestSlottedPageInsertGet
--- PASS: TestSlottedPageInsertGet (0.00s)
=== RUN   TestSlottedPageDeleteAndReuse
--- PASS: TestSlottedPageDeleteAndReuse (0.00s)
=== RUN   TestHeapFileInsert1000AndScan
    heap_test.go:111: inserted 1000 records across 2 pages, scan returned 1000
--- PASS: TestHeapFileInsert1000AndScan (3.53s)
PASS
ok  	github.com/engineers-hub-ltd-in-house-project/minidb	3.679s
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
| `cmd/minidb/main.go` | 1000 件入れて全件走査するデモ |

## バージョニング

連載の回ごとにタグ（`v0.9` のような形）を打って、各回の状態をあとからたどれるようにする方針です。第 9 回時点ではまだタグを打っていません。

## 注意

これは連載の教材用に、仕組みを追えることを優先した最小実装です。本番用のデータベースではありません。

## ライセンス

MIT License. 詳細は [LICENSE](LICENSE) を参照してください。
