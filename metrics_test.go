package minidb

import (
	"errors"
	"math"
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
