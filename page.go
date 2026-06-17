package minidb

import (
	"encoding/binary"
	"errors"
)

// PageSize は 1 ページの大きさ。PostgreSQL に合わせて 8KB にする。
const PageSize = 8192

// ページ先頭のヘッダの大きさ。slotCount(2) + freeSpaceEnd(2)。
const headerSize = 4

// 1 スロットの大きさ。offset(2) + length(2)。
const slotSize = 4

// ErrPageFull はページに空きが足りないときに返す。
var ErrPageFull = errors.New("page is full")

// ErrSlotNotFound は指定したスロットが空、または範囲外のときに返す。
var ErrSlotNotFound = errors.New("slot not found")

// Page はスロット付きページ。固定長 PageSize のバイト列として持つ。
// レイアウトは、先頭からヘッダ、続いてスロット配列が前へ伸び、
// レコード本体がページの末尾から前へ向かって積まれる。
type Page struct {
	data []byte
}

// NewPage は空のページを作る。
func NewPage() *Page {
	p := &Page{data: make([]byte, PageSize)}
	p.setSlotCount(0)
	p.setFreeSpaceEnd(PageSize)
	return p
}

// LoadPage はディスクから読んだバイト列をページとして読み込む。
func LoadPage(b []byte) *Page {
	d := make([]byte, PageSize)
	copy(d, b)
	return &Page{data: d}
}

// Bytes はディスクへ書き出すためのバイト列を返す。
func (p *Page) Bytes() []byte { return p.data }

func (p *Page) slotCount() int      { return int(binary.LittleEndian.Uint16(p.data[0:2])) }
func (p *Page) setSlotCount(n int)  { binary.LittleEndian.PutUint16(p.data[0:2], uint16(n)) }
func (p *Page) freeSpaceEnd() int   { return int(binary.LittleEndian.Uint16(p.data[2:4])) }
func (p *Page) setFreeSpaceEnd(n int) { binary.LittleEndian.PutUint16(p.data[2:4], uint16(n)) }

func (p *Page) slotOffset(i int) (off, length int) {
	base := headerSize + i*slotSize
	off = int(binary.LittleEndian.Uint16(p.data[base : base+2]))
	length = int(binary.LittleEndian.Uint16(p.data[base+2 : base+4]))
	return
}

func (p *Page) setSlot(i, off, length int) {
	base := headerSize + i*slotSize
	binary.LittleEndian.PutUint16(p.data[base:base+2], uint16(off))
	binary.LittleEndian.PutUint16(p.data[base+2:base+4], uint16(length))
}

// freeContiguous は、スロット配列の末尾とレコード本体の先頭の間にある、
// 連続した空き領域の大きさを返す。
func (p *Page) freeContiguous() int {
	return p.freeSpaceEnd() - (headerSize + p.slotCount()*slotSize)
}

// Insert はレコードを 1 件入れ、そのスロット番号を返す。
// 空きが足りなければ詰め直し（compaction）を試み、それでも入らなければ ErrPageFull。
func (p *Page) Insert(record []byte) (int, error) {
	need := len(record)

	// 1) 削除済み（length==0）のスロットを再利用できるか探す。
	reuse := -1
	for i := 0; i < p.slotCount(); i++ {
		if _, l := p.slotOffset(i); l == 0 {
			reuse = i
			break
		}
	}

	extraSlot := slotSize
	if reuse >= 0 {
		extraSlot = 0 // スロット配列は増えない
	}

	if p.freeContiguous() < need+extraSlot {
		p.compact()
		if p.freeContiguous() < need+extraSlot {
			return 0, ErrPageFull
		}
	}

	off := p.freeSpaceEnd() - need
	copy(p.data[off:off+need], record)
	p.setFreeSpaceEnd(off)

	if reuse >= 0 {
		p.setSlot(reuse, off, need)
		return reuse, nil
	}
	i := p.slotCount()
	p.setSlot(i, off, need)
	p.setSlotCount(i + 1)
	return i, nil
}

// Get はスロット番号のレコードを返す。削除済みや範囲外なら ErrSlotNotFound。
func (p *Page) Get(slot int) ([]byte, error) {
	if slot < 0 || slot >= p.slotCount() {
		return nil, ErrSlotNotFound
	}
	off, length := p.slotOffset(slot)
	if length == 0 {
		return nil, ErrSlotNotFound
	}
	out := make([]byte, length)
	copy(out, p.data[off:off+length])
	return out, nil
}

// Delete はスロットを削除済みにする（length を 0 にする墓標）。
// バイト領域は、その場では戻さない。次の compact で回収される。
func (p *Page) Delete(slot int) error {
	if slot < 0 || slot >= p.slotCount() {
		return ErrSlotNotFound
	}
	if _, l := p.slotOffset(slot); l == 0 {
		return ErrSlotNotFound
	}
	p.setSlot(slot, 0, 0)
	return nil
}

// compact は生きているレコードだけを末尾へ詰め直し、削除跡の空きを回収する。
func (p *Page) compact() {
	end := PageSize
	for i := 0; i < p.slotCount(); i++ {
		off, length := p.slotOffset(i)
		if length == 0 {
			continue
		}
		rec := make([]byte, length)
		copy(rec, p.data[off:off+length])
		newOff := end - length
		copy(p.data[newOff:end], rec)
		p.setSlot(i, newOff, length)
		end = newOff
	}
	p.setFreeSpaceEnd(end)
}
