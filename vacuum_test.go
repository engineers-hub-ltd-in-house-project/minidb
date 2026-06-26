package minidb

import "testing"

// TestVacuumReclaimsDeadVersions は、回収が不要タプルを実際に減らすことを確かめる。
func TestVacuumReclaimsDeadVersions(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	seedUsers(m, tbl)

	del := m.Begin()
	if !tbl.Delete(del, 2) {
		t.Fatal("delete failed")
	}
	m.Commit(del)

	oldest := m.OldestActive()
	if got := tbl.DeadVersions(oldest); got != 1 {
		t.Fatalf("回収前の不要タプル数 = %d, 本来は 1", got)
	}
	if removed := Vacuum(tbl, oldest); removed != 1 {
		t.Fatalf("Vacuum が回収した数 = %d, 本来は 1", removed)
	}
	if got := tbl.DeadVersions(m.OldestActive()); got != 0 {
		t.Fatalf("回収後の不要タプル数 = %d, 本来は 0", got)
	}
}

// TestVacuumBlockedByOldTx は、古い現役が一本開いている間は回収できないことを確かめる。
// 回収を止めるのは書き込みの量ではなく、いちばん古い現役の存在。
func TestVacuumBlockedByOldTx(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	seedUsers(m, tbl)

	old := m.Begin() // 古い現役を開いたままにする

	del := m.Begin()
	if !tbl.Delete(del, 2) {
		t.Fatal("delete failed")
	}
	m.Commit(del)

	// 境界は old で止まる。del は old より後なので回収できない。
	if removed := Vacuum(tbl, m.OldestActive()); removed != 0 {
		t.Fatalf("古い tx が開いている間の回収数 = %d, 本来は 0", removed)
	}

	// 古い現役を閉じた途端、同じ版が回収できる。
	m.Commit(old)
	if removed := Vacuum(tbl, m.OldestActive()); removed != 1 {
		t.Fatalf("古い tx を閉じた後の回収数 = %d, 本来は 1", removed)
	}
}
