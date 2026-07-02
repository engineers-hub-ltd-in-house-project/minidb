package minidb

import (
	"path/filepath"
	"testing"
)

// pageWith は、短い文字列を 1 件だけ入れたページの画像を返す。
func pageWith(t *testing.T, s string) []byte {
	t.Helper()
	p := NewPage()
	if _, err := p.Insert([]byte(s)); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	return p.Bytes()
}

// readSlot0 は、ディスク上のページの先頭スロットを文字列で読む。
func readSlot0(t *testing.T, disk *DiskManager, pageID int) string {
	t.Helper()
	p, err := disk.ReadPage(pageID)
	if err != nil {
		t.Fatalf("ReadPage failed: %v", err)
	}
	b, err := p.Get(0)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	return string(b)
}

// PITR は、誤った上書きの手前まで巻き戻す。全適用と対比する。
func TestPITRRecoversToPointBeforeBadWrite(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL failed: %v", err)
	}
	// ここまでが健全な時点。
	wal.AppendPageImage(1, pageWith(t, "good"))    // LSN 1
	wal.AppendPageImage(2, pageWith(t, "other-a")) // LSN 2
	target, err := WALLength(walPath)
	if err != nil {
		t.Fatalf("WALLength failed: %v", err)
	}
	if target != 2 {
		t.Fatalf("健全な時点の LSN = %d、本来は 2", target)
	}
	// この先で、誤って page 1 を上書きしてしまう。
	wal.AppendPageImage(1, pageWith(t, "BAD"))     // LSN 3
	wal.AppendPageImage(2, pageWith(t, "other-b")) // LSN 4
	wal.Close()

	// PITR：target の時点まで戻す。誤った上書き（LSN 3）は適用されない。
	diskA, err := OpenDisk(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatalf("OpenDisk failed: %v", err)
	}
	defer diskA.Close()
	n, err := RecoverUntil(walPath, diskA, target)
	if err != nil {
		t.Fatalf("RecoverUntil failed: %v", err)
	}
	if n != target {
		t.Fatalf("適用数 = %d、本来は %d", n, target)
	}
	if got := readSlot0(t, diskA, 1); got != "good" {
		t.Fatalf("PITR 後の page 1 = %q、本来は good（誤上書きの手前）", got)
	}

	// 対比：全部適用すると、誤った上書きまで入ってしまう。
	diskB, err := OpenDisk(filepath.Join(dir, "b.db"))
	if err != nil {
		t.Fatalf("OpenDisk failed: %v", err)
	}
	defer diskB.Close()
	if _, err := RecoverInto(walPath, diskB); err != nil {
		t.Fatalf("RecoverInto failed: %v", err)
	}
	if got := readSlot0(t, diskB, 1); got != "BAD" {
		t.Fatalf("全適用後の page 1 = %q、本来は BAD（誤上書きが入る）", got)
	}
}
