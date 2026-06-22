package minidb

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
)

// WAL は、変更を先に記録する追記専用のログ。
// データページをディスクに書く前に、まずここへ書いて fsync する。
// これが先行書き込み（write-ahead）。落ちても、ログから復旧できる。
type WAL struct {
	f *os.File
}

// OpenWAL は、ログファイルを開く（なければ作る）。追記専用で開く。
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &WAL{f: f}, nil
}

// Close はログを閉じる。
func (w *WAL) Close() error { return w.f.Close() }

// AppendPageImage は、ページの新しい中身をログに追記し、fsync する。
// 1 レコードの形: [payloadLen uint32][crc uint32][pageID int32][image ...]
// crc を付けるのは、末尾が途中で切れた壊れたレコードを、復旧時に見分けるため。
func (w *WAL) AppendPageImage(pageID int, image []byte) error {
	payload := make([]byte, 4+len(image))
	binary.LittleEndian.PutUint32(payload[0:4], uint32(int32(pageID)))
	copy(payload[4:], image)

	var header [8]byte
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[4:8], crc32.ChecksumIEEE(payload))

	if _, err := w.f.Write(header[:]); err != nil {
		return err
	}
	if _, err := w.f.Write(payload); err != nil {
		return err
	}
	// この Sync で初めて、変更が確実にディスクへ残る。ここが先行書き込みの肝。
	return w.f.Sync()
}

// RecoverInto は、ログを先頭から読み、各ページの中身をデータファイルへ書き戻す。
// 途中で切れた末尾や、壊れたレコードに当たったら、そこで安全に止める。
// 適用したレコード数を返す。
func RecoverInto(walPath string, disk *DiskManager) (int, error) {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // ログがなければ、復旧することは何もない
		}
		return 0, err
	}
	defer f.Close()

	applied := 0
	var header [8]byte
	for {
		if _, err := io.ReadFull(f, header[:]); err != nil {
			break // EOF、または途中で切れたヘッダ。ここで終わり。
		}
		plen := binary.LittleEndian.Uint32(header[0:4])
		wantCRC := binary.LittleEndian.Uint32(header[4:8])

		payload := make([]byte, plen)
		if _, err := io.ReadFull(f, payload); err != nil {
			break // 末尾の payload が途中で切れている。安全に無視する。
		}
		if crc32.ChecksumIEEE(payload) != wantCRC {
			break // 壊れたレコード。ここで止める。
		}

		pageID := int(int32(binary.LittleEndian.Uint32(payload[0:4])))
		image := payload[4:]
		if err := disk.WritePage(pageID, LoadPage(image)); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}
