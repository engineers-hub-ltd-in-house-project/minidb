package minidb

// OldestActive は、いま回収してよい境界を返す。
// いちばん古い現役のトランザクション番号。これより前に消された版は、もう誰からも見えない。
// 進行中が一つも無ければ、次に配る番号を返す（いま消えている版は全部回収してよい）。
func (m *TxManager) OldestActive() XID {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldest := m.nextXID
	for x, s := range m.state {
		if s == txInProgress && x < oldest {
			oldest = x
		}
	}
	return oldest
}

// Vacuum は、境界より前に消された版を表から取り除き、回収した数を返す。
// 残った版で行を詰め直し、版が一つも残らなかった行は表から消す。
// 回収できる境界は、いつもいちばん古い現役（OldestActive）が決める。
func Vacuum(t *MVCCTable, oldestActive XID) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	removed := 0
	for rowID, vs := range t.rows {
		var kept []*version
		for _, v := range vs {
			if v.xmax != 0 && v.xmax < oldestActive {
				removed++ // 誰からも見えない。捨てる。
				continue
			}
			kept = append(kept, v)
		}
		if len(kept) == 0 {
			delete(t.rows, rowID)
		} else {
			t.rows[rowID] = kept
		}
	}
	return removed
}

// FrozenXID は、何に対しても過去とみなす特別な番号。
// 周回の説明のため、64 ビットの XID とは別に、32 ビットの小さなモデルとして閉じてある。
const FrozenXID = uint32(2)

// xidPrecedes は、番号 a が b より前かを、大小ではなく距離で決める。
// int32(a-b) が肝。番号は 32 ビットで回り、差を符号付きで見て負なら過去とみなす。
// 素朴な大小比較は一周した瞬間に壊れるが、これは約半周ぶんだけ前を過去とみなす。
func xidPrecedes(a, b uint32) bool {
	if a == FrozenXID {
		return true
	}
	if b == FrozenXID {
		return false
	}
	return int32(a-b) < 0
}

// frozenRow は、周回の説明に絞った、作成番号だけの行。
type frozenRow struct {
	xmin uint32
}

// visibleAt は、いまの番号から見て、この行が見えるかを返す。
func (r frozenRow) visibleAt(current uint32) bool {
	return xidPrecedes(r.xmin, current)
}

// freeze は、十分に古くなった行の作成番号を FrozenXID に置き換える。
// 凍結した行は、いまの番号がいくつでも、一周しても、過去のままでいられる。
func freeze(r frozenRow) frozenRow {
	r.xmin = FrozenXID
	return r
}
