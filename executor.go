package minidb

import (
	"strconv"
	"strings"
)

// Tuple は段から段へ流れる、一行ぶんの値。
type Tuple struct {
	Values map[string]string
}

// Operator は Volcano モデルのイテレータ。開く・一件出す・閉じるの三つで揃える。
// PostgreSQL の実行ノードに当たる。
type Operator interface {
	Open()
	Next() (*Tuple, bool) // 二つ目が false なら、もう無い
	Close()
}

// SeqScan は表を頭から読む段。第 12 回の可視性をくぐらせ、見える行だけを返す。
// PostgreSQL の Seq Scan に当たる。
type SeqScan struct {
	table *MVCCTable
	tx    *Tx
	rows  []*Tuple
	pos   int
}

// NewSeqScan は表とトランザクションを束ねた走査の段を作る。
func NewSeqScan(table *MVCCTable, tx *Tx) *SeqScan {
	return &SeqScan{table: table, tx: tx}
}

// Open は表を一周し、このトランザクションから見える版だけを並べる。
func (s *SeqScan) Open() {
	s.rows = nil
	s.pos = 0
	s.table.mu.Lock()
	defer s.table.mu.Unlock()
	for _, versions := range s.table.rows {
		for _, v := range versions {
			if s.tx.sees(v) { // 第 12 回の可視性判定
				s.rows = append(s.rows, decodeRow(v.data))
			}
		}
	}
}

// Next は並べた行を、前から一件ずつ返す。
func (s *SeqScan) Next() (*Tuple, bool) {
	if s.pos >= len(s.rows) {
		return nil, false
	}
	t := s.rows[s.pos]
	s.pos++
	return t, true
}

func (s *SeqScan) Close() { s.rows = nil }

// Filter は条件で振り分ける段。合うものだけを上へ通す。
type Filter struct {
	child Operator
	pred  func(*Tuple) bool
}

// NewFilter は下の段と条件を束ねた振り分けの段を作る。
func NewFilter(child Operator, pred func(*Tuple) bool) *Filter {
	return &Filter{child: child, pred: pred}
}

func (f *Filter) Open() { f.child.Open() }

// Next は、条件に合う一件が来るまで下の段に出させ続ける。
func (f *Filter) Next() (*Tuple, bool) {
	for {
		t, ok := f.child.Next()
		if !ok {
			return nil, false // 下が尽きたら、こちらも尽き
		}
		if f.pred(t) {
			return t, true // 合うものだけ、上へ
		}
	}
}

func (f *Filter) Close() { f.child.Close() }

// Project は列を絞る段。要る列だけを残す。
type Project struct {
	child Operator
	cols  []string
}

// NewProject は下の段と残す列を束ねた絞り込みの段を作る。
func NewProject(child Operator, cols []string) *Project {
	return &Project{child: child, cols: cols}
}

func (p *Project) Open() { p.child.Open() }

// Next は一件もらって、指定された列だけを残して返す。
func (p *Project) Next() (*Tuple, bool) {
	t, ok := p.child.Next()
	if !ok {
		return nil, false
	}
	out := &Tuple{Values: map[string]string{}}
	for _, c := range p.cols {
		out.Values[c] = t.Values[c]
	}
	return out, true
}

func (p *Project) Close() { p.child.Close() }

// Run はてっぺんの段を尽きるまで引いて、結果を集める。
// この一回の引きが、下の段まで伝わって全体を動かす。
func Run(root Operator) []*Tuple {
	root.Open()
	defer root.Close()
	var out []*Tuple
	for {
		t, ok := root.Next()
		if !ok {
			break
		}
		out = append(out, t)
	}
	return out
}

// decode は行のバイト列を、名前付きの値に開く。
// 第 8 回でページに詰めた並びを列に戻す役。今回は name=Alice,age=35 形式を切るだけ。
func decodeRow(data string) *Tuple {
	t := &Tuple{Values: map[string]string{}}
	for _, pair := range strings.Split(data, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			t.Values[kv[0]] = kv[1]
		}
	}
	return t
}

// atoiOr は列の値を整数として読む。読めなければ既定値を返す。条件式の補助。
func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
