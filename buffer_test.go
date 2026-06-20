package minidb

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

// ヒットした後、書き換えた内容がちゃんと残っているかを確かめる。
func TestBufferPoolWriteReadBack(t *testing.T) {
	disk, _ := OpenDisk(filepath.Join(t.TempDir(), "bp.db"))
	defer disk.Close()
	bp := NewBufferPool(disk, 3)

	pageID, page, err := bp.NewPage()
	if err != nil {
		t.Fatalf("NewPage failed: %v", err)
	}
	if _, err := page.Insert([]byte("hello")); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	bp.Unpin(pageID, true) // 書き換えたので dirty で返す

	// もう一度取り出して、内容が残っているか。
	p2, err := bp.FetchPage(pageID)
	if err != nil {
		t.Fatalf("FetchPage failed: %v", err)
	}
	got, _ := p2.Get(0)
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("got %q want %q", got, "hello")
	}
	bp.Unpin(pageID, false)
}

// フレーム数より多いページを作り、追い出しが起きても、
// 書き換えた内容がディスクへ書き戻されて失われないことを確かめる。
func TestBufferPoolEvictionFlushesDirty(t *testing.T) {
	disk, _ := OpenDisk(filepath.Join(t.TempDir(), "bp.db"))
	defer disk.Close()
	bp := NewBufferPool(disk, 2) // フレームは 2 枚だけ

	const N = 6
	ids := make([]int, N)
	for i := 0; i < N; i++ {
		id, page, err := bp.NewPage()
		if err != nil {
			t.Fatalf("NewPage %d failed: %v", i, err)
		}
		if _, err := page.Insert([]byte(fmt.Sprintf("page-%d", i))); err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
		bp.Unpin(id, true) // 書き換えたので dirty
		ids[i] = id
	}

	// 2 枚しかないので、6 ページぶんを作る間に追い出しが何度も起きている。
	// それでも、全ページの内容がディスクに残っているはず。
	if err := bp.FlushAll(); err != nil {
		t.Fatalf("FlushAll failed: %v", err)
	}
	for i, id := range ids {
		p, err := bp.FetchPage(id)
		if err != nil {
			t.Fatalf("FetchPage %d failed: %v", id, err)
		}
		got, _ := p.Get(0)
		want := []byte(fmt.Sprintf("page-%d", i))
		if !bytes.Equal(got, want) {
			t.Fatalf("page %d: got %q want %q (dirty page lost on eviction)", id, got, want)
		}
		bp.Unpin(id, false)
	}
}

// すべて pin したまま新しいページを要求すると、追い出せず ErrNoFreeFrame になる。
func TestBufferPoolAllPinnedReturnsError(t *testing.T) {
	disk, _ := OpenDisk(filepath.Join(t.TempDir(), "bp.db"))
	defer disk.Close()
	bp := NewBufferPool(disk, 2)

	// 2 枚とも pin したまま保持する。
	if _, _, err := bp.NewPage(); err != nil {
		t.Fatalf("NewPage 1 failed: %v", err)
	}
	if _, _, err := bp.NewPage(); err != nil {
		t.Fatalf("NewPage 2 failed: %v", err)
	}

	// 3 枚目を要求すると、追い出せる相手がいないので失敗する。
	_, _, err := bp.NewPage()
	if err != ErrNoFreeFrame {
		t.Fatalf("expected ErrNoFreeFrame, got %v", err)
	}
}
