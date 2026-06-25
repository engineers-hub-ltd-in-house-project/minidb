package minidb

import (
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
