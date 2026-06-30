package minidb

import (
	"math/rand"
	"path/filepath"
	"testing"
)

// allocPages は、一時ファイルに n ページ確保したディスクを返す。
func allocPages(t *testing.T, n int) (*DiskManager, func()) {
	t.Helper()
	disk, err := OpenDisk(filepath.Join(t.TempDir(), "tuning.db"))
	if err != nil {
		t.Fatalf("OpenDisk failed: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := disk.AllocatePage(); err != nil {
			disk.Close()
			t.Fatalf("AllocatePage failed: %v", err)
		}
	}
	return disk, func() { disk.Close() }
}

// プールを大きくするとヒット率は上がる。ただし、よく触る一部（hot）を
// 収めたあとは伸びが鈍る。容量とヒット率の、現実に近い関係。
func TestHitRateRisesWithDiminishingReturns(t *testing.T) {
	const total, hot, refs = 100, 10, 5000
	disk, cleanup := allocPages(t, total)
	defer cleanup()

	rng := rand.New(rand.NewSource(1))
	pattern := LocalReferences(rng, total, hot, refs, 0.85)

	rate := func(size int) float64 {
		s, err := MeasureHitRate(disk, size, pattern)
		if err != nil {
			t.Fatalf("MeasureHitRate(%d) failed: %v", size, err)
		}
		return s.HitRate()
	}

	hr2 := rate(2)
	hr10 := rate(10) // よく触る一部がちょうど収まる容量
	hr40 := rate(40)
	hr100 := rate(100) // 全ページが収まる容量
	t.Logf("hit rate: pool=2 %.3f, 10 %.3f, 40 %.3f, 100 %.3f", hr2, hr10, hr40, hr100)

	// 容量が大きいほど、ヒット率は下がらない。
	if !(hr2 <= hr10 && hr10 <= hr40 && hr40 <= hr100) {
		t.Fatalf("ヒット率が容量に対して単調でない: %.3f %.3f %.3f %.3f", hr2, hr10, hr40, hr100)
	}
	// 足りない容量はヒット率を大きく下げ、十分な容量はほぼ取りこぼさない。
	if hr2 > 0.30 {
		t.Fatalf("容量 2 のヒット率 = %.3f、もっと低いはず", hr2)
	}
	if hr100 < 0.90 {
		t.Fatalf("容量 100 のヒット率 = %.3f、もっと高いはず", hr100)
	}
	// 働く一部を収めるまでの伸びが、収めたあとの伸びより大きい（伸びが鈍る）。
	early := hr10 - hr2
	late := hr100 - hr40
	if !(early > late) {
		t.Fatalf("伸びが鈍るはず: 2->10 が %.3f、40->100 が %.3f", early, late)
	}
}

// 接続プールは、表側の接続がいくつでも、backend を上限に収める。
// その結果、接続数に比例していた総メモリが頭打ちになる。
func TestPoolingCapsBackendMemory(t *testing.T) {
	const perConn = 10.0 // 1 接続あたり 10 MiB と仮定

	// プールなし。1000 接続が、そのまま 1000 backend になる。
	noPool := BackendMemory(1000, perConn)

	// プールあり。backend は 50 までに収まる。
	capped := PoolingCapsBackends(1000, 50)
	withPool := BackendMemory(capped, perConn)

	if capped != 50 {
		t.Fatalf("プール後の backend = %d、本来は 50", capped)
	}
	if !(withPool < noPool) {
		t.Fatalf("プールで総メモリが減るはず: %.0f vs %.0f", withPool, noPool)
	}
	if withPool != 500.0 {
		t.Fatalf("プール後の総メモリ = %.0f MiB、本来は 500", withPool)
	}
}
