package minidb

import (
	"sort"
	"testing"
)

// seedUsers は、可視な行を数件入れたユーザ表を用意する。
func seedUsers(m *TxManager, tbl *MVCCTable) {
	tx := m.Begin()
	tbl.Insert(tx, 1, "name=Alice,age=35")
	tbl.Insert(tx, 2, "name=Bob,age=28")
	tbl.Insert(tx, 3, "name=Carol,age=41")
	m.Commit(tx)
}

// collect は結果から指定列を取り出し、並べ替えて返す。
// 表は map なので走査順が決まらない。順序に依らない形で比べる。
func collect(tuples []*Tuple, col string) []string {
	var out []string
	for _, t := range tuples {
		out = append(out, t.Values[col])
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SeqScan は、第 12 回の可視性をくぐらせ、削除済みの行を返さない。
func TestSeqScanVisibility(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	seedUsers(m, tbl)

	// 別のトランザクションで 1 件削除し、確定する。
	del := m.Begin()
	if !tbl.Delete(del, 2) {
		t.Fatal("delete failed")
	}
	m.Commit(del)

	// 後から始めたトランザクションは、削除済みを見ない。
	reader := m.Begin()
	got := collect(Run(NewSeqScan(tbl, reader)), "name")
	want := []string{"Alice", "Carol"}
	if !sameStrings(got, want) {
		t.Fatalf("SeqScan は %v を返した、本来は %v", got, want)
	}
}

// Filter は、条件に合う行だけを上へ通す。
func TestFilter(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	seedUsers(m, tbl)

	reader := m.Begin()
	plan := NewFilter(NewSeqScan(tbl, reader), func(t *Tuple) bool {
		return atoiOr(t.Values["age"], 0) > 30
	})
	got := collect(Run(plan), "name")
	want := []string{"Alice", "Carol"} // 35 と 41
	if !sameStrings(got, want) {
		t.Fatalf("Filter は %v を返した、本来は %v", got, want)
	}
}

// 三段を積み、てっぺんから引くと SELECT name FROM users WHERE age > 30 になる。
func TestProjectAndPipeline(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	seedUsers(m, tbl)

	reader := m.Begin()
	plan := NewProject(
		NewFilter(NewSeqScan(tbl, reader), func(t *Tuple) bool {
			return atoiOr(t.Values["age"], 0) > 30
		}),
		[]string{"name"},
	)
	rows := Run(plan)

	got := collect(rows, "name")
	want := []string{"Alice", "Carol"}
	if !sameStrings(got, want) {
		t.Fatalf("pipeline は %v を返した、本来は %v", got, want)
	}

	// Project が age を落とし、name を残していることを確かめる。
	for _, r := range rows {
		if _, ok := r.Values["age"]; ok {
			t.Fatalf("Project が列 age を残してしまった: %v", r.Values)
		}
		if _, ok := r.Values["name"]; !ok {
			t.Fatalf("Project が列 name を落としてしまった: %v", r.Values)
		}
	}
}
