package minidb

// RecordID はレコードの住所。ページ番号とスロット番号の組。
// PostgreSQL の ctid に当たるもの。
type RecordID struct {
	PageID int
	Slot   int
}

// HeapFile はページを並べてレコードを溜める、いちばん素朴な置き場所。
type HeapFile struct {
	disk *DiskManager
}

// NewHeapFile はディスクマネージャの上にヒープファイルを作る。
func NewHeapFile(d *DiskManager) *HeapFile {
	return &HeapFile{disk: d}
}

// Insert はレコードを 1 件入れ、その住所を返す。
// 末尾のページから空きを探し、どこにも入らなければ新しいページを足す。
func (h *HeapFile) Insert(record []byte) (RecordID, error) {
	n, err := h.disk.NumPages()
	if err != nil {
		return RecordID{}, err
	}

	for id := 0; id < n; id++ {
		p, err := h.disk.ReadPage(id)
		if err != nil {
			return RecordID{}, err
		}
		slot, err := p.Insert(record)
		if err == ErrPageFull {
			continue
		}
		if err != nil {
			return RecordID{}, err
		}
		if err := h.disk.WritePage(id, p); err != nil {
			return RecordID{}, err
		}
		return RecordID{PageID: id, Slot: slot}, nil
	}

	// どのページにも入らなかったので、新しいページを足す。
	id, err := h.disk.AllocatePage()
	if err != nil {
		return RecordID{}, err
	}
	p, err := h.disk.ReadPage(id)
	if err != nil {
		return RecordID{}, err
	}
	slot, err := p.Insert(record)
	if err != nil {
		return RecordID{}, err
	}
	if err := h.disk.WritePage(id, p); err != nil {
		return RecordID{}, err
	}
	return RecordID{PageID: id, Slot: slot}, nil
}

// Get は住所を指定してレコードを 1 件読む。
func (h *HeapFile) Get(rid RecordID) ([]byte, error) {
	p, err := h.disk.ReadPage(rid.PageID)
	if err != nil {
		return nil, err
	}
	return p.Get(rid.Slot)
}

// Scan は全ページ、全スロットを順にたどり、生きているレコードを関数へ渡す。
// PostgreSQL の Seq Scan に当たる、いちばん素朴な全件走査。
func (h *HeapFile) Scan(fn func(rid RecordID, record []byte) error) error {
	n, err := h.disk.NumPages()
	if err != nil {
		return err
	}
	for id := 0; id < n; id++ {
		p, err := h.disk.ReadPage(id)
		if err != nil {
			return err
		}
		for slot := 0; slot < p.slotCount(); slot++ {
			rec, err := p.Get(slot)
			if err == ErrSlotNotFound {
				continue // 削除済みは飛ばす
			}
			if err != nil {
				return err
			}
			if err := fn(RecordID{PageID: id, Slot: slot}, rec); err != nil {
				return err
			}
		}
	}
	return nil
}
