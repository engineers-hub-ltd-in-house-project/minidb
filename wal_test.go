package minidb

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 落ちる前にデータファイルへ書かなくても、WAL から全ページを復旧できる。
func TestWALRecoveryRebuildsDataFile(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")
	dataPath := filepath.Join(dir, "data.db")

	// 落ちる前。WAL へは書くが、データファイルには一切書かない。
	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	const N = 50
	for i := 0; i < N; i++ {
		p := NewPage()
		if _, err := p.Insert([]byte(fmt.Sprintf("row-%03d", i))); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
		if err := wal.AppendPageImage(i, p.Bytes()); err != nil {
			t.Fatalf("AppendPageImage failed: %v", err)
		}
	}
	wal.Close()

	// 落ちた。data.db は空のまま。ディスクに残っているのは wal.log だけ。
	// 復旧する。WAL をデータファイルへ流し込む。
	disk, err := OpenDisk(dataPath)
	if err != nil {
		t.Fatalf("OpenDisk failed: %v", err)
	}
	defer disk.Close()

	applied, err := RecoverInto(walPath, disk)
	if err != nil {
		t.Fatalf("RecoverInto failed: %v", err)
	}
	if applied != N {
		t.Fatalf("applied %d records, want %d", applied, N)
	}

	// データファイルの各ページに、書いた行が復旧されているか。
	for i := 0; i < N; i++ {
		p, err := disk.ReadPage(i)
		if err != nil {
			t.Fatalf("ReadPage %d failed: %v", i, err)
		}
		got, err := p.Get(0)
		if err != nil {
			t.Fatalf("Get on page %d failed: %v", i, err)
		}
		want := fmt.Sprintf("row-%03d", i)
		if string(got) != want {
			t.Fatalf("page %d: got %q want %q", i, got, want)
		}
	}
}

// 末尾が途中で切れていても、そこまでの正しいレコードは復旧し、壊れた末尾は無視する。
func TestWALIgnoresTornTail(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")
	dataPath := filepath.Join(dir, "data.db")

	wal, _ := OpenWAL(walPath)
	const good = 3
	for i := 0; i < good; i++ {
		p := NewPage()
		p.Insert([]byte(fmt.Sprintf("ok-%d", i)))
		wal.AppendPageImage(i, p.Bytes())
	}
	wal.Close()

	// 壊れた末尾を直接足す。大きな長さを宣言するヘッダだけ書き、payload は書かない。
	// これは、ログ書き込みの途中で電源が落ちた状況に当たる。
	f, _ := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	var torn [8]byte
	binary.LittleEndian.PutUint32(torn[0:4], 9999) // ありもしない長さ
	binary.LittleEndian.PutUint32(torn[4:8], 12345)
	f.Write(torn[:])
	f.Close()

	disk, _ := OpenDisk(dataPath)
	defer disk.Close()
	applied, err := RecoverInto(walPath, disk)
	if err != nil {
		t.Fatalf("RecoverInto failed: %v", err)
	}
	if applied != good {
		t.Fatalf("applied %d records, want %d (torn tail must be ignored)", applied, good)
	}
}

// 同じログを二回流しても、結果は同じ（リカバリは何度やっても安全）。
func TestWALReplayIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	wal, _ := OpenWAL(walPath)
	const N = 10
	for i := 0; i < N; i++ {
		p := NewPage()
		p.Insert([]byte(fmt.Sprintf("v-%d", i)))
		wal.AppendPageImage(i, p.Bytes())
	}
	wal.Close()

	d1, _ := OpenDisk(filepath.Join(dir, "once.db"))
	defer d1.Close()
	RecoverInto(walPath, d1)

	d2, _ := OpenDisk(filepath.Join(dir, "twice.db"))
	defer d2.Close()
	RecoverInto(walPath, d2)
	RecoverInto(walPath, d2) // もう一度

	for i := 0; i < N; i++ {
		p1, _ := d1.ReadPage(i)
		p2, _ := d2.ReadPage(i)
		g1, _ := p1.Get(0)
		g2, _ := p2.Get(0)
		if string(g1) != string(g2) {
			t.Fatalf("page %d differs after double replay: %q vs %q", i, g1, g2)
		}
	}
}
