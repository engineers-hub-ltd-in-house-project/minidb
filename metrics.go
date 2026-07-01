package minidb

import "time"

// 観測を、行き当たりばったりに眺めない。二つの型で整理する。
// 資源は USE（使用率・飽和・エラー）、処理は RED（流量・失敗・所要時間）。
// これまでの回で測った値は、どれかの枠に必ず収まる。

// REDStat は、処理（リクエスト）の観測。Rate, Errors, Duration。
type REDStat struct {
	Count     int           // 流量：処理した数
	Errors    int           // 失敗：失敗した数
	TotalTime time.Duration // 所要時間の合計
}

// Record は、1 件の処理の所要時間と結果を足し込む。err が非 nil なら失敗。
func (r *REDStat) Record(d time.Duration, err error) {
	r.Count++
	r.TotalTime += d
	if err != nil {
		r.Errors++
	}
}

// ErrorRate は、処理全体に対する失敗の割合。0 から 1。
func (r REDStat) ErrorRate() float64 {
	if r.Count == 0 {
		return 0
	}
	return float64(r.Errors) / float64(r.Count)
}

// AvgDuration は、1 件あたりの平均所要時間。
func (r REDStat) AvgDuration() time.Duration {
	if r.Count == 0 {
		return 0
	}
	return r.TotalTime / time.Duration(r.Count)
}

// USEStat は、資源の観測。Utilization, Saturation, Errors。
type USEStat struct {
	Utilization float64 // 使用率：資源のうち使っている割合。0 から 1
	Saturation  float64 // 飽和：処理しきれず待たされている度合い
	Errors      int     // エラー：資源が返した失敗の数
}

// ObserveBuffer は、参照列を bp に流し、ヒット統計を返す。
// bp は呼び出し側が保持するので、流したあとの使用率を BufferPoolUSE で読める。
func ObserveBuffer(bp *BufferPool, refs []int) (HitStats, error) {
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

// BufferPoolUSE は、バッファプールを資源として USE で表す。
// 使用率 = 載っているページ数 / フレーム数。飽和 = ミス率。ミスが多いほど、容量に働きが詰まっている。
func BufferPoolUSE(bp *BufferPool, s HitStats) USEStat {
	capacity := len(bp.frames)
	util := 0.0
	if capacity > 0 {
		util = float64(len(bp.table)) / float64(capacity)
	}
	sat := 0.0
	if s.Total() > 0 {
		sat = float64(s.Misses) / float64(s.Total())
	}
	return USEStat{Utilization: util, Saturation: sat}
}

// versionCounts は、表の生きた版と不要な版を数える。
func versionCounts(t *MVCCTable, oldestActive XID) (live, dead int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, vs := range t.rows {
		for _, v := range vs {
			if v.xmax != 0 && v.xmax < oldestActive {
				dead++
			} else {
				live++
			}
		}
	}
	return live, dead
}

// StorageUSE は、表を資源として USE で表す。
// 飽和 = 不要タプルの割合。回収が追いつかず溜まっているほど高い。第 14 回の回収の遅れが、ここに出る。
func StorageUSE(t *MVCCTable, oldestActive XID) USEStat {
	live, dead := versionCounts(t, oldestActive)
	total := live + dead
	sat := 0.0
	if total > 0 {
		sat = float64(dead) / float64(total)
	}
	return USEStat{Saturation: sat}
}

// Observation は、ある時点の観測をひとまとめにしたもの。
// 資源は USE、処理は RED。定期的に取れば、状態の移り変わりが読める。
type Observation struct {
	Buffer  USEStat // 資源：バッファプール
	Storage USEStat // 資源：表（不要タプルの溜まり）
	Query   REDStat // 処理：クエリ
}
