package minidb

import "math/rand"

// HitStats は、ページ参照のヒットとミスを数えた結果。
type HitStats struct {
	Hits   int
	Misses int
}

// Total は、参照の総数。
func (s HitStats) Total() int { return s.Hits + s.Misses }

// HitRate は、参照全体に対するヒットの割合。0 から 1。
func (s HitStats) HitRate() float64 {
	if s.Total() == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Total())
}

// MeasureHitRate は、ページ参照列 refs を、容量 size のプールに流して、ヒット率を測る。
// すでにプールに載っていればヒット、なければディスクから読むのでミス。
// 各参照はすぐ離す。次の参照で追い出せるようにするため。
func MeasureHitRate(disk *DiskManager, size int, refs []int) (HitStats, error) {
	bp := NewBufferPool(disk, size)
	var s HitStats
	for _, pageID := range refs {
		if _, resident := bp.table[pageID]; resident {
			s.Hits++
		} else {
			s.Misses++
		}
		if _, err := bp.FetchPage(pageID); err != nil {
			return s, err
		}
		bp.Unpin(pageID, false)
	}
	return s, nil
}

// LocalReferences は、偏りのある参照列を作る。
// hot 本のページに参照の hotShare を集め、残りを全体へ散らす。
// 「よく触る一部」と「たまに触る全体」という、現実の偏りの再現。
func LocalReferences(rng *rand.Rand, total, hot, count int, hotShare float64) []int {
	refs := make([]int, count)
	for i := range refs {
		if rng.Float64() < hotShare {
			refs[i] = rng.Intn(hot)
		} else {
			refs[i] = rng.Intn(total)
		}
	}
	return refs
}

// BackendMemory は、接続数と 1 接続あたりのメモリ（MiB）から、見込みの総メモリを返す。
// PostgreSQL は接続ごとにプロセスを持つので、メモリは接続数に比例して増える。
func BackendMemory(connections int, perConnMiB float64) float64 {
	return float64(connections) * perConnMiB
}

// PoolingCapsBackends は、接続プールを挟んだときの実 backend 接続数を返す。
// 表側の接続がいくつあっても、backend は poolSize までに収まる。PgBouncer の効き目。
func PoolingCapsBackends(clientConnections, poolSize int) int {
	if clientConnections < poolSize {
		return clientConnections
	}
	return poolSize
}
