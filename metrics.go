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
