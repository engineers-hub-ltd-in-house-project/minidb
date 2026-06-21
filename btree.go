package minidb

import (
	"encoding/binary"
	"sort"
)

// maxKeys は 1 ノードに入れるキーの最大数。
// 本物はページに入るだけ大きくするが、ここでは分割やマージを目で追えるよう、
// わざと小さく（最大 4 個）してある。5 個目で分割する。
const maxKeys = 4

// minKeys は根以外のノードが保てるキーの最小数。これを割ると、
// 借用かマージで埋める。maxKeys=4 のとき 2。
const minKeys = 2

// nodeHeaderSize は、ノードのページ先頭に置く固定ヘッダの大きさ。
// isLeaf(1) + 予約(1) + keyCount(2) + next(4)。
const nodeHeaderSize = 8

// bnode は B+tree の 1 ノード。1 ノードが 1 ページに対応する。
// 葉と内部ノードの二種類を、isLeaf で振り分ける。
type bnode struct {
	pageID   int
	isLeaf   bool
	keys     []int64
	values   []RecordID // 葉のときだけ使う。keys[i] の行の住所。
	children []int      // 内部ノードのときだけ使う。len == len(keys)+1。
	next     int        // 葉どうしを横につなぐ。次の葉の pageID。なければ -1。
}

// BPlusTree は、バッファプールの上に積んだ B+tree の索引。
// キー（int64）から RecordID（行の住所、PostgreSQL の ctid に当たる）を引く。
type BPlusTree struct {
	bp     *BufferPool
	rootID int
}

// NewBPlusTree は空の B+tree を作る。葉だけの根を 1 ページ確保する。
// 根のページ番号はメモリ上にだけ持つ。再オープン時に引き継ぐための
// メタページは、まだ作らない（次回以降）。
func NewBPlusTree(bp *BufferPool) (*BPlusTree, error) {
	t := &BPlusTree{bp: bp}
	root := &bnode{isLeaf: true, next: -1}
	id, err := t.allocNode(root)
	if err != nil {
		return nil, err
	}
	t.rootID = id
	return t, nil
}

// encode は bnode をページの生バイト列へ書き込む。
// ページを丸ごと 1 ノードが使い、先頭の固定ヘッダに続けてキーの配列、
// その後ろに、葉なら住所、内部なら子ページ番号を詰める。
func encode(n *bnode, p *Page) {
	b := p.Bytes()
	if n.isLeaf {
		b[0] = 1
	} else {
		b[0] = 0
	}
	b[1] = 0
	binary.LittleEndian.PutUint16(b[2:4], uint16(len(n.keys)))
	next := n.next // 内部ノードの next は使わない。-1 を書いておく。
	if !n.isLeaf {
		next = -1
	}
	binary.LittleEndian.PutUint32(b[4:8], uint32(int32(next)))
	off := nodeHeaderSize
	for _, k := range n.keys {
		binary.LittleEndian.PutUint64(b[off:off+8], uint64(k))
		off += 8
	}
	if n.isLeaf {
		for _, v := range n.values {
			binary.LittleEndian.PutUint32(b[off:off+4], uint32(int32(v.PageID)))
			binary.LittleEndian.PutUint32(b[off+4:off+8], uint32(int32(v.Slot)))
			off += 8
		}
	} else {
		for _, c := range n.children {
			binary.LittleEndian.PutUint32(b[off:off+4], uint32(int32(c)))
			off += 4
		}
	}
}

// decode はページの生バイト列から bnode を復元する。
// スライスはすべて作り直すので、ページのバイト列とは切り離された複製になる。
// だから、この後ページを Unpin して追い出されても、ノードは壊れない。
func decode(p *Page, id int) *bnode {
	b := p.Bytes()
	n := &bnode{pageID: id}
	n.isLeaf = b[0] == 1
	k := int(binary.LittleEndian.Uint16(b[2:4]))
	n.next = int(int32(binary.LittleEndian.Uint32(b[4:8])))
	off := nodeHeaderSize
	n.keys = make([]int64, k)
	for i := 0; i < k; i++ {
		n.keys[i] = int64(binary.LittleEndian.Uint64(b[off : off+8]))
		off += 8
	}
	if n.isLeaf {
		n.values = make([]RecordID, k)
		for i := 0; i < k; i++ {
			n.values[i] = RecordID{
				PageID: int(int32(binary.LittleEndian.Uint32(b[off : off+4]))),
				Slot:   int(int32(binary.LittleEndian.Uint32(b[off+4 : off+8]))),
			}
			off += 8
		}
	} else {
		n.children = make([]int, k+1)
		for i := 0; i <= k; i++ {
			n.children[i] = int(int32(binary.LittleEndian.Uint32(b[off : off+4])))
			off += 4
		}
	}
	return n
}

// readNode は、ページ番号 id のノードをメモリへ読み出す。
// バッファプールから借りている間に複製を作って返すので、pin を持ち越さない。
// これで、木をたどる間に同時に pin するページは、常に高々 1 枚で済む。
func (t *BPlusTree) readNode(id int) (*bnode, error) {
	p, err := t.bp.FetchPage(id)
	if err != nil {
		return nil, err
	}
	n := decode(p, id)
	t.bp.Unpin(id, false)
	return n, nil
}

// writeNode は、ノードを自分のページへ書き戻す（dirty を立てる）。
// 書き戻す直前に取り直すので、間に追い出されていても、正しいページに書く。
func (t *BPlusTree) writeNode(n *bnode) error {
	p, err := t.bp.FetchPage(n.pageID)
	if err != nil {
		return err
	}
	encode(n, p)
	t.bp.Unpin(n.pageID, true)
	return nil
}

// allocNode は、新しいページを確保してノードを書き、その番号を返す。
func (t *BPlusTree) allocNode(n *bnode) (int, error) {
	id, p, err := t.bp.NewPage()
	if err != nil {
		return 0, err
	}
	n.pageID = id
	encode(n, p)
	t.bp.Unpin(id, true)
	return id, nil
}

// childIndex は、内部ノード n で key を含む部分木の子の番号を返す。
// 仕切りキーは右の部分木の最小キー。key >= 仕切り なら右へ進むので、
// 「key より大きい最初の仕切り」の位置が、そのまま子の番号になる。
// 葉の検索が >= を使うのと非対称だが、これで正しい（直さないこと）。
func childIndex(n *bnode, key int64) int {
	return sort.Search(len(n.keys), func(i int) bool { return n.keys[i] > key })
}

// Search は key を引く。見つかれば住所と true を、なければ false を返す。
// 根の節から葉まで降りるだけ。内部ノードでは、どの子へ降りるかを仕切りで決める。
func (t *BPlusTree) Search(key int64) (RecordID, bool, error) {
	id := t.rootID
	for {
		n, err := t.readNode(id)
		if err != nil {
			return RecordID{}, false, err
		}
		if n.isLeaf {
			i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
			if i < len(n.keys) && n.keys[i] == key {
				return n.values[i], true, nil
			}
			return RecordID{}, false, nil
		}
		id = n.children[childIndex(n, key)]
	}
}

// Scan は、すべての要素をキーの昇順で関数へ渡す。
// 根から最左の葉まで降り、あとは葉の横のつながり（next）をたどるだけ。
func (t *BPlusTree) Scan(fn func(key int64, rid RecordID) error) error {
	id := t.rootID
	for {
		n, err := t.readNode(id)
		if err != nil {
			return err
		}
		if n.isLeaf {
			break
		}
		id = n.children[0]
	}
	for id != -1 {
		n, err := t.readNode(id)
		if err != nil {
			return err
		}
		for i := range n.keys {
			if err := fn(n.keys[i], n.values[i]); err != nil {
				return err
			}
		}
		id = n.next
	}
	return nil
}

// Insert は key -> rid を入れる。すでに同じ key があれば上書きする。
func (t *BPlusTree) Insert(key int64, rid RecordID) error {
	sepKey, rightID, didSplit, err := t.insertRec(t.rootID, key, rid)
	if err != nil {
		return err
	}
	if !didSplit {
		return nil
	}
	// 根が割れた。1 段高い新しい根を作り、木の高さを一つ増やす。
	newRoot := &bnode{
		isLeaf:   false,
		keys:     []int64{sepKey},
		children: []int{t.rootID, rightID},
		next:     -1,
	}
	id, err := t.allocNode(newRoot)
	if err != nil {
		return err
	}
	t.rootID = id
	return nil
}

// insertRec は id の部分木へ key を入れる。
// ノードが割れたら、上へ写し上げる仕切り sepKey と、新しい右ノードの番号
// rightID と、割れた印 didSplit を返す。割れなければ didSplit は false。
func (t *BPlusTree) insertRec(id int, key int64, rid RecordID) (int64, int, bool, error) {
	n, err := t.readNode(id)
	if err != nil {
		return 0, 0, false, err
	}

	if n.isLeaf {
		i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
		if i < len(n.keys) && n.keys[i] == key {
			n.values[i] = rid // 同じキーは上書き
			return 0, 0, false, t.writeNode(n)
		}
		n.keys = insertInt64(n.keys, i, key)
		n.values = insertRID(n.values, i, rid)
		if len(n.keys) <= maxKeys {
			return 0, 0, false, t.writeNode(n)
		}
		return t.splitLeaf(n)
	}

	ci := childIndex(n, key)
	sepKey, rightID, didSplit, err := t.insertRec(n.children[ci], key, rid)
	if err != nil {
		return 0, 0, false, err
	}
	if !didSplit {
		return 0, 0, false, nil
	}
	// 子が割れた。仕切りと新しい子を、この内部ノードへ取り込む。
	n.keys = insertInt64(n.keys, ci, sepKey)
	n.children = insertInt(n.children, ci+1, rightID)
	if len(n.keys) <= maxKeys {
		return 0, 0, false, t.writeNode(n)
	}
	return t.splitInternal(n)
}

// splitLeaf は、いっぱいになった葉を二つに分ける。
// 右半分を新しい葉へ移し、右の先頭キーを親へ写し上げる（コピーアップ）。
// 葉には実データ（住所）があるので、キーを抜き出すのではなく写す。
func (t *BPlusTree) splitLeaf(n *bnode) (int64, int, bool, error) {
	mid := len(n.keys) / 2
	right := &bnode{isLeaf: true, next: n.next}
	right.keys = append([]int64{}, n.keys[mid:]...)
	right.values = append([]RecordID{}, n.values[mid:]...)
	n.keys = n.keys[:mid]
	n.values = n.values[:mid]

	rightID, err := t.allocNode(right) // 親より先に、新しい子を書く
	if err != nil {
		return 0, 0, false, err
	}
	n.next = rightID
	if err := t.writeNode(n); err != nil {
		return 0, 0, false, err
	}
	return right.keys[0], rightID, true, nil // 右の先頭を写し上げる
}

// splitInternal は、いっぱいになった内部ノードを二つに分ける。
// 葉と違い、真ん中のキーは親へ押し上げる（移動）。内部ノードのキーは
// ただの道しるべなので、上へ移してよい。
func (t *BPlusTree) splitInternal(n *bnode) (int64, int, bool, error) {
	mid := len(n.keys) / 2
	up := n.keys[mid]
	right := &bnode{isLeaf: false, next: -1}
	right.keys = append([]int64{}, n.keys[mid+1:]...)
	right.children = append([]int{}, n.children[mid+1:]...)
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]

	rightID, err := t.allocNode(right) // 親より先に、新しい子を書く
	if err != nil {
		return 0, 0, false, err
	}
	if err := t.writeNode(n); err != nil {
		return 0, 0, false, err
	}
	return up, rightID, true, nil
}

// Delete は key を取り除く。無ければ何もしない。
func (t *BPlusTree) Delete(key int64) error {
	if _, err := t.deleteRec(t.rootID, key); err != nil {
		return err
	}
	// 根の縮約。根が内部で、キーが空（子が一つだけ）になったら、
	// その子を新しい根にする。木の高さが一段減る。
	root, err := t.readNode(t.rootID)
	if err != nil {
		return err
	}
	if !root.isLeaf && len(root.keys) == 0 {
		t.rootID = root.children[0]
	}
	return nil
}

// deleteRec は id の部分木から key を取り除く。
// 取り除いた結果このノードが下限を割ったら、underflow に true を返す。
// 親（呼び出し側）が、借用かマージで埋める。根は下限を免れる。
func (t *BPlusTree) deleteRec(id int, key int64) (bool, error) {
	n, err := t.readNode(id)
	if err != nil {
		return false, err
	}

	if n.isLeaf {
		i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
		if i >= len(n.keys) || n.keys[i] != key {
			return false, nil // 無いキーは何もしない
		}
		n.keys = removeInt64(n.keys, i)
		n.values = removeRID(n.values, i)
		if err := t.writeNode(n); err != nil {
			return false, err
		}
		return len(n.keys) < minKeys, nil
	}

	ci := childIndex(n, key)
	childUnderflow, err := t.deleteRec(n.children[ci], key)
	if err != nil {
		return false, err
	}
	if childUnderflow {
		if err := t.fixUnderflow(n, ci); err != nil { // 親 n はこの中で書き戻す
			return false, err
		}
	}
	return len(n.keys) < minKeys, nil
}

// fixUnderflow は、下限を割った子 parent.children[ci] を埋める。
// 兄弟に余裕があれば借り、なければ兄弟とマージする。最後に親を書き戻す。
func (t *BPlusTree) fixUnderflow(parent *bnode, ci int) error {
	child, err := t.readNode(parent.children[ci])
	if err != nil {
		return err
	}

	// 左の兄弟に余裕（> minKeys）があれば、左から借りる。
	if ci > 0 {
		left, err := t.readNode(parent.children[ci-1])
		if err != nil {
			return err
		}
		if len(left.keys) > minKeys {
			if err := t.borrowFromLeft(parent, ci, left, child); err != nil {
				return err
			}
			return t.writeNode(parent)
		}
	}
	// 右の兄弟に余裕があれば、右から借りる。
	if ci < len(parent.children)-1 {
		right, err := t.readNode(parent.children[ci+1])
		if err != nil {
			return err
		}
		if len(right.keys) > minKeys {
			if err := t.borrowFromRight(parent, ci, child, right); err != nil {
				return err
			}
			return t.writeNode(parent)
		}
	}
	// どちらにも余裕がない。兄弟とマージする。
	// 自分が左端なら右を自分へ畳み、そうでなければ左へ畳む。
	if ci > 0 {
		left, err := t.readNode(parent.children[ci-1])
		if err != nil {
			return err
		}
		if err := t.merge(parent, ci-1, left, child); err != nil {
			return err
		}
	} else {
		right, err := t.readNode(parent.children[ci+1])
		if err != nil {
			return err
		}
		if err := t.merge(parent, ci, child, right); err != nil {
			return err
		}
	}
	return t.writeNode(parent)
}

// borrowFromLeft は、左の兄弟から一つ借りて、下限を割った child を埋める。
func (t *BPlusTree) borrowFromLeft(parent *bnode, ci int, left, child *bnode) error {
	if child.isLeaf {
		// 左の末尾を、child の先頭へ移す。親の仕切りを child の新しい先頭に直す。
		k := left.keys[len(left.keys)-1]
		v := left.values[len(left.values)-1]
		left.keys = left.keys[:len(left.keys)-1]
		left.values = left.values[:len(left.values)-1]
		child.keys = insertInt64(child.keys, 0, k)
		child.values = insertRID(child.values, 0, v)
		parent.keys[ci-1] = child.keys[0]
	} else {
		// 内部ノードは、親の仕切りを経由して回す。
		// 親の仕切りを child の先頭へ下ろし、左の末尾キーを親の仕切りへ上げる。
		sep := parent.keys[ci-1]
		child.keys = insertInt64(child.keys, 0, sep)
		lastChild := left.children[len(left.children)-1]
		child.children = insertInt(child.children, 0, lastChild)
		parent.keys[ci-1] = left.keys[len(left.keys)-1]
		left.keys = left.keys[:len(left.keys)-1]
		left.children = left.children[:len(left.children)-1]
	}
	if err := t.writeNode(left); err != nil {
		return err
	}
	return t.writeNode(child)
}

// borrowFromRight は、右の兄弟から一つ借りて、下限を割った child を埋める。
func (t *BPlusTree) borrowFromRight(parent *bnode, ci int, child, right *bnode) error {
	if child.isLeaf {
		k := right.keys[0]
		v := right.values[0]
		right.keys = removeInt64(right.keys, 0)
		right.values = removeRID(right.values, 0)
		child.keys = append(child.keys, k)
		child.values = append(child.values, v)
		parent.keys[ci] = right.keys[0]
	} else {
		sep := parent.keys[ci]
		child.keys = append(child.keys, sep)
		firstChild := right.children[0]
		child.children = append(child.children, firstChild)
		parent.keys[ci] = right.keys[0]
		right.keys = removeInt64(right.keys, 0)
		right.children = removeInt(right.children, 0)
	}
	if err := t.writeNode(child); err != nil {
		return err
	}
	return t.writeNode(right)
}

// merge は、left と right を一つにまとめる。
// 親からは仕切りキーと、右の子への枝を取り除く。
func (t *BPlusTree) merge(parent *bnode, li int, left, right *bnode) error {
	if left.isLeaf {
		left.keys = append(left.keys, right.keys...)
		left.values = append(left.values, right.values...)
		left.next = right.next // 横のつながりを引き継ぐ
	} else {
		left.keys = append(left.keys, parent.keys[li]) // 仕切りを下ろす
		left.keys = append(left.keys, right.keys...)
		left.children = append(left.children, right.children...)
	}
	parent.keys = removeInt64(parent.keys, li)
	parent.children = removeInt(parent.children, li+1)
	return t.writeNode(left)
}

// 以下は、スライスの途中へ差し込む／途中から取り除く小道具。

func insertInt64(s []int64, i int, v int64) []int64 {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertRID(s []RecordID, i int, v RecordID) []RecordID {
	s = append(s, RecordID{})
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertInt(s []int, i int, v int) []int {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func removeInt64(s []int64, i int) []int64 { return append(s[:i], s[i+1:]...) }

func removeRID(s []RecordID, i int) []RecordID { return append(s[:i], s[i+1:]...) }

func removeInt(s []int, i int) []int { return append(s[:i], s[i+1:]...) }
