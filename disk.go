package minidb

import (
	"os"
)

// DiskManager はページ単位でファイルを読み書きする。
// ファイルを PageSize ごとに区切り、ページ番号で位置を決める。
type DiskManager struct {
	file *os.File
}

// OpenDisk はファイルを開く（なければ作る）。
func OpenDisk(path string) (*DiskManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &DiskManager{file: f}, nil
}

// Close はファイルを閉じる。
func (d *DiskManager) Close() error { return d.file.Close() }

// NumPages はファイルが今いくつのページ分あるかを返す。
func (d *DiskManager) NumPages() (int, error) {
	fi, err := d.file.Stat()
	if err != nil {
		return 0, err
	}
	return int(fi.Size() / PageSize), nil
}

// ReadPage はページ番号 id のページを読む。
func (d *DiskManager) ReadPage(id int) (*Page, error) {
	buf := make([]byte, PageSize)
	_, err := d.file.ReadAt(buf, int64(id)*PageSize)
	if err != nil {
		return nil, err
	}
	return LoadPage(buf), nil
}

// WritePage はページ番号 id の位置へページを書く。
func (d *DiskManager) WritePage(id int, p *Page) error {
	_, err := d.file.WriteAt(p.Bytes(), int64(id)*PageSize)
	if err != nil {
		return err
	}
	return d.file.Sync()
}

// AllocatePage は末尾に空のページを足し、その番号を返す。
func (d *DiskManager) AllocatePage() (int, error) {
	n, err := d.NumPages()
	if err != nil {
		return 0, err
	}
	if err := d.WritePage(n, NewPage()); err != nil {
		return 0, err
	}
	return n, nil
}
