package minidb

import "testing"

// mustSee は、トランザクションから行が want に見えることを確かめる。
func mustSee(t *testing.T, table *MVCCTable, tx *Tx, rowID int, want string) {
	t.Helper()
	got, ok := table.Read(tx, rowID)
	if !ok {
		t.Fatalf("row %d: 見えるはずが見えなかった (want %q)", rowID, want)
	}
	if got != want {
		t.Fatalf("row %d: got %q want %q", rowID, got, want)
	}
}

// mustGone は、トランザクションから行が見えないことを確かめる。
func mustGone(t *testing.T, table *MVCCTable, tx *Tx, rowID int) {
	t.Helper()
	if got, ok := table.Read(tx, rowID); ok {
		t.Fatalf("row %d: 見えないはずが %q が見えた", rowID, got)
	}
}

// スナップショットを取った後に他のトランザクションがコミットしても、
// その変更は見えない。後から始まったトランザクションには見える。
func TestMVCCSnapshotIsolation(t *testing.T) {
	mgr := NewTxManager()
	table := NewMVCCTable()

	// A が先にスナップショットを取る。
	a := mgr.Begin()

	// B が A より後に行を入れてコミットする。
	b := mgr.Begin()
	table.Insert(b, 1, "b-row")
	mgr.Commit(b)

	// A のスナップショットには、B の挿入は含まれない。
	mustGone(t, table, a, 1)

	// B のコミット後に始まった C には見える。
	c := mgr.Begin()
	mustSee(t, table, c, 1, "b-row")
}

// 自分の Insert / Update / Delete は、自分の Read に即座に反映される。
func TestMVCCReadsOwnWrites(t *testing.T) {
	mgr := NewTxManager()
	table := NewMVCCTable()

	a := mgr.Begin()

	table.Insert(a, 1, "v1")
	mustSee(t, table, a, 1, "v1")

	if !table.Update(a, 1, "v2") {
		t.Fatalf("Update が対象行を見つけられなかった")
	}
	mustSee(t, table, a, 1, "v2")

	if !table.Delete(a, 1) {
		t.Fatalf("Delete が対象行を見つけられなかった")
	}
	mustGone(t, table, a, 1)
}

// Update は古いバージョンを残す。更新前にスナップショットを取った
// トランザクションは旧バージョン、後から始まったものは新バージョンを見る。
func TestMVCCUpdateKeepsOldVersionVisible(t *testing.T) {
	mgr := NewTxManager()
	table := NewMVCCTable()

	// 最初の行を作ってコミットする。
	w := mgr.Begin()
	table.Insert(w, 1, "v1")
	mgr.Commit(w)

	// reader は更新前にスナップショットを取る。
	reader := mgr.Begin()

	// updater が新バージョンへ書き換えてコミットする。
	updater := mgr.Begin()
	if !table.Update(updater, 1, "v2") {
		t.Fatalf("Update が対象行を見つけられなかった")
	}
	mgr.Commit(updater)

	// reader は古いスナップショットのまま v1 を見る。
	mustSee(t, table, reader, 1, "v1")

	// 更新後に始まった newcomer は v2 を見る。
	newcomer := mgr.Begin()
	mustSee(t, table, newcomer, 1, "v2")
}

// Delete をコミットしても、削除前にスナップショットを取った
// トランザクションにはまだ行が見える。後から始まったものには見えない。
func TestMVCCDeleteHidesFromNewTx(t *testing.T) {
	mgr := NewTxManager()
	table := NewMVCCTable()

	w := mgr.Begin()
	table.Insert(w, 1, "row")
	mgr.Commit(w)

	// reader は削除前にスナップショットを取る。
	reader := mgr.Begin()

	deleter := mgr.Begin()
	if !table.Delete(deleter, 1) {
		t.Fatalf("Delete が対象行を見つけられなかった")
	}
	mgr.Commit(deleter)

	// reader にはまだ見える。
	mustSee(t, table, reader, 1, "row")

	// 削除後に始まった newcomer には見えない。
	newcomer := mgr.Begin()
	mustGone(t, table, newcomer, 1)
}

// Abort したトランザクションの変更は、他のトランザクションから見えない。
func TestMVCCAbortIsInvisible(t *testing.T) {
	mgr := NewTxManager()
	table := NewMVCCTable()

	a := mgr.Begin()
	table.Insert(a, 1, "dead")
	mgr.Abort(a)

	// コミットされていないので、後から始まった c には見えない。
	c := mgr.Begin()
	mustGone(t, table, c, 1)
}

// DeadVersions は、いちばん古い現役のトランザクションより前に消された
// バージョン（不要タプル）の数を数える。
func TestMVCCDeadVersions(t *testing.T) {
	mgr := NewTxManager()
	table := NewMVCCTable()

	// XID=1 で挿入。
	a := mgr.Begin()
	table.Insert(a, 1, "a")
	mgr.Commit(a)

	// XID=2 で更新。旧バージョンに xmax=2 が付く。
	b := mgr.Begin()
	if !table.Update(b, 1, "a2") {
		t.Fatalf("Update が対象行を見つけられなかった")
	}
	mgr.Commit(b)

	// XID=3 で削除。いま見えているバージョンに xmax=3 が付く。
	c := mgr.Begin()
	if !table.Delete(c, 1) {
		t.Fatalf("Delete が対象行を見つけられなかった")
	}
	mgr.Commit(c)

	// 消された番号は 2 と 3。oldestActive をどこに置くかで回収対象が変わる。
	if got := table.DeadVersions(2); got != 0 {
		t.Fatalf("DeadVersions(2): got %d want 0", got)
	}
	if got := table.DeadVersions(3); got != 1 {
		t.Fatalf("DeadVersions(3): got %d want 1", got)
	}
	if got := table.DeadVersions(4); got != 2 {
		t.Fatalf("DeadVersions(4): got %d want 2", got)
	}
}
