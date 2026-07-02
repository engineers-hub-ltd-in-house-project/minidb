package minidb

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
)

// この回で足すのは二つ。
// 一つは PITR。第 11 回の WAL を、ある時点まで適用して止める復旧。
// 一つは SLO とエラーバジェット。守ると約束した可用率から、許される停止時間を出す。

// WALLength は、ログに入っている正常なレコードの数を返す。
// いまどこまで進んでいるかの目印。PostgreSQL の LSN に当たる。
func WALLength(walPath string) (int, error) {
	return replayWAL(walPath, nil, -1)
}

// RecoverUntil は、ログを先頭から target 個のレコードまで適用して止める。
// target の先にレコードが残っていても、適用しない。ある時点まで進めて、そこで止める。
// これが PITR（point-in-time recovery）の骨格。誤った操作の手前まで巻き戻せる。
func RecoverUntil(walPath string, disk *DiskManager, target int) (int, error) {
	return replayWAL(walPath, disk, target)
}

// replayWAL は、ログを読みながらレコードを数え、disk が非 nil なら書き戻す。
// limit が 0 以上なら、その数だけ適用して止める。適用（または数えた）レコード数を返す。
// レコードの形と壊れ判定は、第 11 回の RecoverInto と同じ。
func replayWAL(walPath string, disk *DiskManager, limit int) (int, error) {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // ログがなければ、することは何もない
		}
		return 0, err
	}
	defer f.Close()

	applied := 0
	var header [8]byte
	for {
		if limit >= 0 && applied >= limit {
			break // 目標の時点まで来た。ここで止める。
		}
		if _, err := io.ReadFull(f, header[:]); err != nil {
			break // EOF、または途中で切れたヘッダ
		}
		plen := binary.LittleEndian.Uint32(header[0:4])
		wantCRC := binary.LittleEndian.Uint32(header[4:8])

		payload := make([]byte, plen)
		if _, err := io.ReadFull(f, payload); err != nil {
			break // 末尾が途中で切れている
		}
		if crc32.ChecksumIEEE(payload) != wantCRC {
			break // 壊れたレコード
		}

		if disk != nil {
			pageID := int(int32(binary.LittleEndian.Uint32(payload[0:4])))
			image := payload[4:]
			if err := disk.WritePage(pageID, LoadPage(image)); err != nil {
				return applied, err
			}
		}
		applied++
	}
	return applied, nil
}
