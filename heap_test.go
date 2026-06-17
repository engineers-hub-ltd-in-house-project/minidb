package minidb

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSlottedPageInsertGet(t *testing.T) {
	p := NewPage()
	recs := [][]byte{[]byte("apple"), []byte("banana"), []byte("cherry")}
	var slots []int
	for _, r := range recs {
		s, err := p.Insert(r)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
		slots = append(slots, s)
	}
	for i, s := range slots {
		got, err := p.Get(s)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if !bytes.Equal(got, recs[i]) {
			t.Fatalf("got %q want %q", got, recs[i])
		}
	}
}

func TestSlottedPageDeleteAndReuse(t *testing.T) {
	p := NewPage()
	// ページの空きを使い切るまで、大きめのレコードを詰める。
	big := bytes.Repeat([]byte("x"), 2000)
	var slots []int
	for {
		s, err := p.Insert(big)
		if err == ErrPageFull {
			break
		}
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
		slots = append(slots, s)
	}
	if len(slots) == 0 {
		t.Fatal("nothing inserted")
	}

	// もう入らないことを確かめる。
	if _, err := p.Insert(big); err != ErrPageFull {
		t.Fatalf("expected ErrPageFull, got %v", err)
	}

	// 1 件削除すると、その空きを再利用して、また入れられる。
	if err := p.Delete(slots[0]); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	reused, err := p.Insert(big)
	if err != nil {
		t.Fatalf("insert after delete failed (free space not reused): %v", err)
	}
	got, err := p.Get(reused)
	if err != nil {
		t.Fatalf("Get reused failed: %v", err)
	}
	if !bytes.Equal(got, big) {
		t.Fatal("reused record mismatch")
	}
}

func TestHeapFileInsert1000AndScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.db")
	disk, err := OpenDisk(path)
	if err != nil {
		t.Fatalf("OpenDisk failed: %v", err)
	}
	defer disk.Close()
	heap := NewHeapFile(disk)

	const N = 1000
	for i := 0; i < N; i++ {
		rec := []byte(fmt.Sprintf("record-%04d", i))
		if _, err := heap.Insert(rec); err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}

	count := 0
	seen := make(map[string]bool)
	err = heap.Scan(func(rid RecordID, rec []byte) error {
		count++
		seen[string(rec)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if count != N {
		t.Fatalf("scanned %d records, want %d", count, N)
	}
	for i := 0; i < N; i++ {
		key := fmt.Sprintf("record-%04d", i)
		if !seen[key] {
			t.Fatalf("missing record %q", key)
		}
	}

	pages, _ := disk.NumPages()
	t.Logf("inserted %d records across %d pages, scan returned %d", N, pages, count)
}
