package minidb

import "sync"

// XID はトランザクション番号。単調に増えていく。
type XID int64

type txState int

const (
	txInProgress txState = iota
	txCommitted
	txAborted
)

// TxManager は、トランザクション番号を配り、各番号の状態を覚える。
type TxManager struct {
	mu      sync.Mutex
	nextXID XID
	state   map[XID]txState
}

func NewTxManager() *TxManager {
	return &TxManager{nextXID: 1, state: map[XID]txState{}}
}

// Snapshot は、ある時点での「何が見えるか」を固定したもの。
// xmax 以上の番号は未来。xip は、その時点で進行中だった番号。どちらも見えない。
type Snapshot struct {
	xmax XID
	xip  map[XID]bool
}

// Tx は、一つのトランザクション。自分の番号とスナップショットを持つ。
type Tx struct {
	id   XID
	snap *Snapshot
	mgr  *TxManager
}

// Begin は、新しいトランザクションを始める。
// このとき、いま進行中の他のトランザクションを覚えたスナップショットを取る。
func (m *TxManager) Begin() *Tx {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextXID
	m.nextXID++
	m.state[id] = txInProgress

	xip := map[XID]bool{}
	for x, s := range m.state {
		if s == txInProgress && x != id {
			xip[x] = true
		}
	}
	return &Tx{id: id, snap: &Snapshot{xmax: m.nextXID, xip: xip}, mgr: m}
}

func (m *TxManager) Commit(tx *Tx) {
	m.mu.Lock()
	m.state[tx.id] = txCommitted
	m.mu.Unlock()
}

func (m *TxManager) Abort(tx *Tx) {
	m.mu.Lock()
	m.state[tx.id] = txAborted
	m.mu.Unlock()
}

func (m *TxManager) isCommitted(x XID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state[x] == txCommitted
}

// effectVisible は、ある番号 x の変更が、このトランザクションから見えるかを返す。
// 自分の変更は見える。未来の番号、進行中だった番号、未確定の番号は見えない。
func (tx *Tx) effectVisible(x XID) bool {
	if x == tx.id {
		return true // 自分の変更は自分に見える
	}
	if x >= tx.snap.xmax {
		return false // スナップショットより後に始まった。未来。
	}
	if tx.snap.xip[x] {
		return false // スナップショットの時点で進行中だった
	}
	return tx.mgr.isCommitted(x) // 確定しているものだけ見える
}

// version は、一つの行の、ある時点のバージョン。
// xmin が作った番号、xmax が消した番号（0 はまだ消されていない）。
type version struct {
	xmin XID
	xmax XID
	data string
}

// sees は、このバージョンがトランザクションから見えるかを返す。
// 作成が見えて、かつ削除が見えないなら、見える。
func (tx *Tx) sees(v *version) bool {
	if !tx.effectVisible(v.xmin) {
		return false
	}
	if v.xmax == 0 {
		return true
	}
	// 削除が見えるなら、この行はもう見えない。
	return !tx.effectVisible(v.xmax)
}

// MVCCTable は、行ごとにバージョンの列を持つ表。
// 書き換えは、古いバージョンを消したことにして、新しいバージョンを足す。
type MVCCTable struct {
	mu   sync.Mutex
	rows map[int][]*version
}

func NewMVCCTable() *MVCCTable {
	return &MVCCTable{rows: map[int][]*version{}}
}

// Read は、トランザクションから見える行のデータを返す。
func (t *MVCCTable) Read(tx *Tx, rowID int) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, v := range t.rows[rowID] {
		if tx.sees(v) {
			return v.data, true
		}
	}
	return "", false
}

// Insert は、新しい行を作る。
func (t *MVCCTable) Insert(tx *Tx, rowID int, data string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rows[rowID] = append(t.rows[rowID], &version{xmin: tx.id, data: data})
}

// Update は、いま見えているバージョンを消したことにして、新しいバージョンを足す。
func (t *MVCCTable) Update(tx *Tx, rowID int, data string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, v := range t.rows[rowID] {
		if v.xmax == 0 && tx.sees(v) {
			v.xmax = tx.id // 古いバージョンに、消した印をつける
			t.rows[rowID] = append(t.rows[rowID], &version{xmin: tx.id, data: data})
			return true
		}
	}
	return false
}

// Delete は、いま見えているバージョンに、消した印をつける。
func (t *MVCCTable) Delete(tx *Tx, rowID int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, v := range t.rows[rowID] {
		if v.xmax == 0 && tx.sees(v) {
			v.xmax = tx.id
			return true
		}
	}
	return false
}

// DeadVersions は、どのトランザクションから見ても、もう要らないバージョンの数を返す。
// 消した番号が、いちばん古い現役のトランザクションより前なら、誰からも見えない。
// これが第 14 回の VACUUM が回収する対象、いわゆる不要タプルに当たる。
func (t *MVCCTable) DeadVersions(oldestActive XID) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, vs := range t.rows {
		for _, v := range vs {
			if v.xmax != 0 && v.xmax < oldestActive {
				count++
			}
		}
	}
	return count
}
