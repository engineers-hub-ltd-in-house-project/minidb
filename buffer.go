package minidb

import "errors"

// ErrNoFreeFrame は、すべてのフレームが pin されていて、
// 追い出せるものが一つもないときに返す。
var ErrNoFreeFrame = errors.New("no free frame available (all pinned)")

// frame は、バッファプールの中の一区画。1 ページぶんをメモリに載せる。
type frame struct {
	pageID   int
	page     *Page
	pinCount int  // 使用中の数。0 より大きいと追い出せない。
	dirty    bool // メモリ上で書き換えられ、ディスクとずれているか。
	refBit   bool // clock のための参照ビット。
	valid    bool // このフレームが使われているか。
}

// BufferPool は、限られた数のフレームを持ち、ディスクのページをメモリに載せる。
// 同じページが要るときは、ディスクへ行かずにメモリから返す。
type BufferPool struct {
	disk   *DiskManager
	frames []frame
	table  map[int]int // pageID から、それを載せているフレーム番号への対応。
	hand   int         // clock の針。
}

// NewBufferPool は、size 枚のフレームを持つバッファプールを作る。
func NewBufferPool(disk *DiskManager, size int) *BufferPool {
	return &BufferPool{
		disk:   disk,
		frames: make([]frame, size),
		table:  make(map[int]int),
	}
}

// FetchPage は、ページをメモリに用意して返し、pin する。
// すでにメモリにあれば、それを返す（ヒット）。なければディスクから読む（ミス）。
func (bp *BufferPool) FetchPage(pageID int) (*Page, error) {
	if i, ok := bp.table[pageID]; ok {
		f := &bp.frames[i]
		f.pinCount++
		f.refBit = true
		return f.page, nil
	}
	i, err := bp.victim()
	if err != nil {
		return nil, err
	}
	p, err := bp.disk.ReadPage(pageID)
	if err != nil {
		return nil, err
	}
	bp.frames[i] = frame{pageID: pageID, page: p, pinCount: 1, refBit: true, valid: true}
	bp.table[pageID] = i
	return p, nil
}

// NewPage は、ディスクに新しいページを足し、それをメモリに載せて返す（pin 済み）。
func (bp *BufferPool) NewPage() (int, *Page, error) {
	pageID, err := bp.disk.AllocatePage()
	if err != nil {
		return 0, nil, err
	}
	p, err := bp.FetchPage(pageID)
	if err != nil {
		return 0, nil, err
	}
	return pageID, p, nil
}

// Unpin は、ページの使用を終える。dirty が true なら、書き換えた印をつける。
func (bp *BufferPool) Unpin(pageID int, dirty bool) {
	if i, ok := bp.table[pageID]; ok {
		f := &bp.frames[i]
		if f.pinCount > 0 {
			f.pinCount--
		}
		if dirty {
			f.dirty = true
		}
	}
}

// FlushPage は、指定ページが書き換えられていれば、ディスクへ書き戻す。
func (bp *BufferPool) FlushPage(pageID int) error {
	if i, ok := bp.table[pageID]; ok {
		f := &bp.frames[i]
		if f.dirty {
			if err := bp.disk.WritePage(f.pageID, f.page); err != nil {
				return err
			}
			f.dirty = false
		}
	}
	return nil
}

// FlushAll は、書き換えられた全フレームをディスクへ書き戻す。
// PostgreSQL のチェックポイントに当たる動き。
func (bp *BufferPool) FlushAll() error {
	for i := range bp.frames {
		f := &bp.frames[i]
		if f.valid && f.dirty {
			if err := bp.disk.WritePage(f.pageID, f.page); err != nil {
				return err
			}
			f.dirty = false
		}
	}
	return nil
}

// victim は、新しいページを載せるフレームを一つ選ぶ。
// 空きがあればそれを使い、なければ clock で追い出す相手を選ぶ。
func (bp *BufferPool) victim() (int, error) {
	n := len(bp.frames)

	// 1) 使われていないフレームがあれば、それを使う。
	for i := 0; i < n; i++ {
		if !bp.frames[i].valid {
			return i, nil
		}
	}

	// 2) clock で追い出す相手を探す。
	// pin されているものは飛ばす。参照ビットが立っていれば倒して次へ。
	// 参照ビットが倒れているものを、追い出す。
	for scanned := 0; scanned < 2*n; scanned++ {
		i := bp.hand
		bp.hand = (bp.hand + 1) % n
		f := &bp.frames[i]
		if f.pinCount > 0 {
			continue
		}
		if f.refBit {
			f.refBit = false
			continue
		}
		// 追い出す。書き換えられていたら、先にディスクへ書き戻す。
		if f.dirty {
			if err := bp.disk.WritePage(f.pageID, f.page); err != nil {
				return 0, err
			}
		}
		delete(bp.table, f.pageID)
		f.valid = false
		return i, nil
	}
	return 0, ErrNoFreeFrame
}
