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
