package minidb

import (
	"strconv"
	"strings"
	"testing"
)

// seedCities は、id（一意）と city（少数の値）を持つ行を n 件入れ、
// id に対する索引を張って返す。プランナの選択を見るための土台。
func seedCities(t *testing.T, m *TxManager, tbl *MVCCTable, n int) *Index {
	t.Helper()
	tree, cleanup := newTestTree(t, 16)
	t.Cleanup(cleanup)
	idx := NewIndex("id", tree)

	tx := m.Begin()
	cities := []string{"tokyo", "osaka"}
	for i := 1; i <= n; i++ {
		tbl.Insert(tx, i, "id="+strconv.Itoa(i)+",city="+cities[i%2])
		if err := idx.Add(int64(i), i); err != nil {
			t.Fatalf("index add failed: %v", err)
		}
	}
	m.Commit(tx)
	return idx
}

// 一意なキーへの等値条件は、選択性が高い。プランナは索引走査を選ぶ。
func TestPlannerPicksIndexForSelective(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	const n = 100
	idx := seedCities(t, m, tbl, n)

	stats := Stats{RowCount: n, Distinct: map[string]int{"id": n, "city": 2}}
	pl := NewPlanner(tbl, stats, map[string]*Index{"id": idx})

	tx := m.Begin()
	plan := pl.PlanEquals(tx, "id", 42)
	if plan.Node != "Index Scan" {
		t.Fatalf("選ばれた走査 = %q、本来は Index Scan（選択性が高い）\n%s", plan.Node, plan.Explain())
	}
	if plan.EstRows != 1 {
		t.Fatalf("見積もり行数 = %d、本来は 1", plan.EstRows)
	}

	// 選んだ計画が正しい結果を返すことも確かめる。
	rows := Run(plan.Root())
	if len(rows) != 1 || rows[0].Values["id"] != "42" {
		t.Fatalf("索引走査の結果 = %v、本来は id=42 の 1 件", rows)
	}
}

// 少数の値しか持たない列への等値条件は、多くの行に当たる。
// 索引があっても、全件走査のほうが安い。プランナは Seq Scan を選ぶ。
func TestPlannerPicksSeqForNonSelective(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	const n = 100
	seedCities(t, m, tbl, n)

	// city にも索引があると仮定するが、異なり数が 2 しかない。
	tree, cleanup := newTestTree(t, 16)
	t.Cleanup(cleanup)
	cityIdx := NewIndex("city", tree)

	stats := Stats{RowCount: n, Distinct: map[string]int{"id": n, "city": 2}}
	pl := NewPlanner(tbl, stats, map[string]*Index{"city": cityIdx})

	tx := m.Begin()
	plan := pl.PlanEquals(tx, "city", 0) // city=tokyo 相当。約半数に当たる。
	if plan.Node != "Seq Scan" {
		t.Fatalf("選ばれた走査 = %q、本来は Seq Scan（選択性が低い）\n%s", plan.Node, plan.Explain())
	}
	if plan.EstRows != n/2 {
		t.Fatalf("見積もり行数 = %d、本来は %d", plan.EstRows, n/2)
	}
}

// 索引が無い列は、選択性が高くても全件走査になる。
func TestPlannerFallsBackWithoutIndex(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	const n = 100
	seedCities(t, m, tbl, n)

	stats := Stats{RowCount: n, Distinct: map[string]int{"id": n, "city": 2}}
	pl := NewPlanner(tbl, stats, map[string]*Index{}) // 索引なし

	tx := m.Begin()
	plan := pl.PlanEquals(tx, "id", 42)
	if plan.Node != "Seq Scan" {
		t.Fatalf("選ばれた走査 = %q、索引が無いので本来は Seq Scan", plan.Node)
	}
	// それでも結果は正しい。
	rows := Run(plan.Root())
	if len(rows) != 1 || rows[0].Values["id"] != "42" {
		t.Fatalf("全件走査の結果 = %v、本来は id=42 の 1 件", rows)
	}
}

// Explain は、選んだ走査と見積もりを読める形で返す。
func TestPlanExplainShowsChoice(t *testing.T) {
	m := NewTxManager()
	tbl := NewMVCCTable()
	const n = 100
	idx := seedCities(t, m, tbl, n)

	stats := Stats{RowCount: n, Distinct: map[string]int{"id": n, "city": 2}}
	pl := NewPlanner(tbl, stats, map[string]*Index{"id": idx})

	tx := m.Begin()
	line := pl.PlanEquals(tx, "id", 42).Explain()
	if !strings.Contains(line, "Index Scan") || !strings.Contains(line, "rows=1") {
		t.Fatalf("Explain = %q、Index Scan と rows=1 を含むはず", line)
	}
}
