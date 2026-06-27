package minidb

// この回で足すのは三つ。
// 一つは Index。第 10 回の B+tree を、列の値から行のありかへの索引として使う。
// 一つは IndexScan。索引で一点引きして、該当する行だけを返す走査の段。
// 一つは Planner。同じ条件に対し、全件走査と索引走査の費用を見積もり、安いほうを選ぶ。

// Index は、ある列の値から行 ID への索引。第 10 回の B+tree をそのまま使う。
// MVCC 表では行のありかは rowID そのものなので、RecordID にはその rowID を入れる。
type Index struct {
	col  string
	tree *BPlusTree
}

// NewIndex は、対象の列名と B+tree を束ねた索引を作る。
func NewIndex(col string, tree *BPlusTree) *Index {
	return &Index{col: col, tree: tree}
}

// Add は、キー値と行 ID の対応を索引に入れる。
func (ix *Index) Add(key int64, rowID int) error {
	return ix.tree.Insert(key, RecordID{Slot: rowID})
}

// Lookup は、キー値に対応する行 ID を引く。無ければ二つ目が false。
func (ix *Index) Lookup(key int64) (int, bool, error) {
	rid, ok, err := ix.tree.Search(key)
	if err != nil || !ok {
		return 0, false, err
	}
	return rid.Slot, true, nil
}

// IndexScan は、索引でキーを一点引きし、該当する行だけを返す段。
// 全件を舐めない。索引が指した行 ID の版だけを、可視性をくぐらせて返す。
type IndexScan struct {
	table *MVCCTable
	tx    *Tx
	index *Index
	key   int64
	rows  []*Tuple
	pos   int
}

// NewIndexScan は、表・トランザクション・索引・引くキーを束ねた走査を作る。
func NewIndexScan(table *MVCCTable, tx *Tx, index *Index, key int64) *IndexScan {
	return &IndexScan{table: table, tx: tx, index: index, key: key}
}

// Open は、索引で行 ID を引き、その行の版のうち見えるものだけを並べる。
func (s *IndexScan) Open() {
	s.rows = nil
	s.pos = 0
	rowID, ok, err := s.index.Lookup(s.key)
	if err != nil || !ok {
		return
	}
	s.table.mu.Lock()
	defer s.table.mu.Unlock()
	for _, v := range s.table.rows[rowID] {
		if s.tx.sees(v) {
			s.rows = append(s.rows, decodeRow(v.data))
		}
	}
}

// Next は並べた行を、前から一件ずつ返す。SeqScan と同じ約束。
func (s *IndexScan) Next() (*Tuple, bool) {
	if s.pos >= len(s.rows) {
		return nil, false
	}
	t := s.rows[s.pos]
	s.pos++
	return t, true
}

func (s *IndexScan) Close() { s.rows = nil }

// Stats は、計画を立てるための見積もり材料。表の行数と、列ごとの異なり数。
// PostgreSQL では ANALYZE が集めて pg_statistic に貯める情報に当たる。
type Stats struct {
	RowCount int
	Distinct map[string]int // 列名 → その列の異なる値の数
}

// estimateRows は、col = val の等値条件に一致する行数を見積もる。
// 異なり数が多いほど、一致は少ない。PostgreSQL の選択性見積もりの素朴版。
func (s Stats) estimateRows(col string) int {
	d := s.Distinct[col]
	if d <= 0 {
		return s.RowCount // 異なり数を知らなければ、最悪を見込む
	}
	est := s.RowCount / d
	if est < 1 {
		est = 1
	}
	return est
}
