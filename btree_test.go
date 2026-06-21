package minidb

import (
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
)

// newTestTree は、一時ファイルの上に空の B+tree を用意する。
func newTestTree(t *testing.T, poolSize int) (*BPlusTree, func()) {
	t.Helper()
	disk, err := OpenDisk(filepath.Join(t.TempDir(), "btree.db"))
	if err != nil {
		t.Fatalf("OpenDisk failed: %v", err)
	}
	bp := NewBufferPool(disk, poolSize)
	tree, err := NewBPlusTree(bp)
	if err != nil {
		disk.Close()
		t.Fatalf("NewBPlusTree failed: %v", err)
	}
	return tree, func() { disk.Close() }
}

// ridFor は、キー k に対応する検査用の住所を作る。
func ridFor(k int64) RecordID {
	return RecordID{PageID: int(k), Slot: int(k % 7)}
}

// height は、根から葉までの段数を返す（テスト用）。葉だけなら 1。
func (t *BPlusTree) height() (int, error) {
	id := t.rootID
	h := 1
	for {
		n, err := t.readNode(id)
		if err != nil {
			return 0, err
		}
		if n.isLeaf {
			return h, nil
		}
		h++
		id = n.children[0]
	}
}

// rootIsInternal は、根が内部ノードに育ったかを返す（テスト用）。
func (t *BPlusTree) rootIsInternal() (bool, error) {
	n, err := t.readNode(t.rootID)
	if err != nil {
		return false, err
	}
	return !n.isLeaf, nil
}

// 分割が何度も起きるほど入れて、全件を引けることと、無いキーが
// 見つからないことを確かめる。挿入はランダム順。
func TestBTreeInsertAndSearch(t *testing.T) {
	tree, cleanup := newTestTree(t, 256)
	defer cleanup()

	const N = 500
	for _, ki := range rand.New(rand.NewSource(42)).Perm(N) {
		k := int64(ki)
		if err := tree.Insert(k, ridFor(k)); err != nil {
			t.Fatalf("Insert %d failed: %v", k, err)
		}
	}
	if h, _ := tree.height(); h < 3 {
		t.Fatalf("expected height >= 3 after %d inserts, got %d", N, h)
	}
	for i := 0; i < N; i++ {
		k := int64(i)
		got, ok, err := tree.Search(k)
		if err != nil {
			t.Fatalf("Search %d failed: %v", k, err)
		}
		if !ok {
			t.Fatalf("key %d not found", k)
		}
		if got != ridFor(k) {
			t.Fatalf("key %d: got %+v want %+v", k, got, ridFor(k))
		}
	}
	if _, ok, _ := tree.Search(int64(N + 1)); ok {
		t.Fatalf("absent key %d reported as found", N+1)
	}
}

// バラバラの順で入れても、走査は昇順で、件数も集合も合うことを確かめる。
func TestBTreeScanIsSorted(t *testing.T) {
	tree, cleanup := newTestTree(t, 256)
	defer cleanup()

	const N = 300
	for _, ki := range rand.New(rand.NewSource(7)).Perm(N) {
		k := int64(ki)
		if err := tree.Insert(k, ridFor(k)); err != nil {
			t.Fatalf("Insert %d failed: %v", k, err)
		}
	}
	var got []int64
	err := tree.Scan(func(key int64, rid RecordID) error {
		if rid != ridFor(key) {
			t.Fatalf("scan key %d: rid %+v want %+v", key, rid, ridFor(key))
		}
		got = append(got, key)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if len(got) != N {
		t.Fatalf("scan returned %d keys, want %d", len(got), N)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("scan not ascending at %d: %d then %d", i, got[i-1], got[i])
		}
	}
}

// 多段の木を作ってから大半を消し、借用・マージ・根の縮約が起きても、
// 残ったキーは引けて、消したキーは引けないことを確かめる。
func TestBTreeDeleteWithMerge(t *testing.T) {
	tree, cleanup := newTestTree(t, 256)
	defer cleanup()

	const N = 200
	for i := 0; i < N; i++ {
		k := int64(i)
		if err := tree.Insert(k, ridFor(k)); err != nil {
			t.Fatalf("Insert %d failed: %v", k, err)
		}
	}
	ri0, _ := tree.rootIsInternal()
	if !ri0 {
		t.Fatalf("expected internal root after %d inserts", N)
	}
	h0, _ := tree.height()

	for i := 0; i < N-10; i++ { // 0..189 を消し、190..199 を残す
		k := int64(i)
		if err := tree.Delete(k); err != nil {
			t.Fatalf("Delete %d failed: %v", k, err)
		}
	}

	h1, _ := tree.height()
	ri1, _ := tree.rootIsInternal()
	if h1 >= h0 && ri1 {
		t.Fatalf("tree did not shrink: height %d->%d, rootInternal %v->%v", h0, h1, ri0, ri1)
	}
	for i := 0; i < N; i++ {
		k := int64(i)
		got, ok, err := tree.Search(k)
		if err != nil {
			t.Fatalf("Search %d failed: %v", k, err)
		}
		if i < N-10 {
			if ok {
				t.Fatalf("deleted key %d still found", k)
			}
		} else {
			if !ok {
				t.Fatalf("surviving key %d not found", k)
			}
			if got != ridFor(k) {
				t.Fatalf("key %d: got %+v want %+v", k, got, ridFor(k))
			}
		}
	}
}

// 無作為な挿入・削除を繰り返し、答え合わせ用の素朴なマップと突き合わせる。
// 分割・借用・マージの細かいほころびは、これで炙り出される。
// フレーム数を木より少なめにしてあるので、途中で追い出しも起きる。
// dirty な葉が追い出し時にちゃんと書き戻され、追い出しをまたいでも木が
// 壊れないことも、ここで一緒に確かめている。
func TestBTreeRandomizedAgainstMap(t *testing.T) {
	tree, cleanup := newTestTree(t, 128)
	defer cleanup()

	r := rand.New(rand.NewSource(1))
	oracle := map[int64]RecordID{}
	const ops = 3000
	const keyspace = 500

	check := func() {
		for k := int64(0); k < keyspace; k++ {
			got, ok, err := tree.Search(k)
			if err != nil {
				t.Fatalf("Search %d failed: %v", k, err)
			}
			want, wok := oracle[k]
			if ok != wok {
				t.Fatalf("key %d: tree ok=%v oracle ok=%v", k, ok, wok)
			}
			if ok && got != want {
				t.Fatalf("key %d: tree %+v oracle %+v", k, got, want)
			}
		}
		var keys []int64
		for k := range oracle {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		var got []int64
		err := tree.Scan(func(key int64, rid RecordID) error {
			if rid != oracle[key] {
				t.Fatalf("scan key %d: rid %+v oracle %+v", key, rid, oracle[key])
			}
			got = append(got, key)
			return nil
		})
		if err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		if len(got) != len(keys) {
			t.Fatalf("scan %d keys, oracle %d", len(got), len(keys))
		}
		for i := range keys {
			if got[i] != keys[i] {
				t.Fatalf("scan/oracle mismatch at %d: %d vs %d", i, got[i], keys[i])
			}
		}
	}

	for i := 0; i < ops; i++ {
		k := int64(r.Intn(keyspace))
		if r.Intn(100) < 60 {
			rid := RecordID{PageID: int(k), Slot: r.Intn(8)}
			if err := tree.Insert(k, rid); err != nil {
				t.Fatalf("Insert %d failed: %v", k, err)
			}
			oracle[k] = rid
		} else {
			if err := tree.Delete(k); err != nil {
				t.Fatalf("Delete %d failed: %v", k, err)
			}
			delete(oracle, k)
		}
		if i%500 == 0 {
			check()
		}
	}
	check()
}
