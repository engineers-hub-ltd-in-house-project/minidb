package minidb

import (
	"errors"
	"math"
	"math/rand"
	"path/filepath"
	"testing"
	"time"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// RED は、流量・失敗・所要時間を積み上げる。
func TestREDRecordsRateErrorsDuration(t *testing.T) {
	var red REDStat
	red.Record(2*time.Millisecond, nil)
	red.Record(4*time.Millisecond, nil)
	red.Record(6*time.Millisecond, errors.New("boom")) // 失敗

	if red.Count != 3 {
		t.Fatalf("流量 = %d、本来は 3", red.Count)
	}
	if red.Errors != 1 {
		t.Fatalf("失敗 = %d、本来は 1", red.Errors)
	}
	if !approx(red.ErrorRate(), 1.0/3.0) {
		t.Fatalf("失敗率 = %f、本来は 1/3", red.ErrorRate())
	}
	if red.AvgDuration() != 4*time.Millisecond {
		t.Fatalf("平均所要時間 = %v、本来は 4ms", red.AvgDuration())
	}
}

// バッファプールの USE。プールが埋まれば使用率は 1、取りこぼしがあれば飽和が立つ。
func TestBufferPoolUSEReflectsFillAndMisses(t *testing.T) {
	const total, cap = 100, 10
	disk, err := OpenDisk(filepath.Join(t.TempDir(), "use.db"))
	if err != nil {
		t.Fatalf("OpenDisk failed: %v", err)
	}
	defer disk.Close()
	for i := 0; i < total; i++ {
		if _, err := disk.AllocatePage(); err != nil {
			t.Fatalf("AllocatePage failed: %v", err)
		}
	}

	bp := NewBufferPool(disk, cap)
	rng := rand.New(rand.NewSource(1))
	refs := LocalReferences(rng, total, 8, 3000, 0.8)
	stats, err := ObserveBuffer(bp, refs)
	if err != nil {
		t.Fatalf("ObserveBuffer failed: %v", err)
	}

	use := BufferPoolUSE(bp, stats)
	// 容量を超える working set を流したので、プールは満杯。
	if !approx(use.Utilization, 1.0) {
		t.Fatalf("使用率 = %f、本来は 1.0（満杯）", use.Utilization)
	}
	// 全体へ散る参照があるので、取りこぼしはゼロではない。
	if !(use.Saturation > 0 && use.Saturation < 1) {
		t.Fatalf("飽和 = %f、本来は 0 と 1 の間", use.Saturation)
	}
}
