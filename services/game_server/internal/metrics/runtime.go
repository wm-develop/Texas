package metrics

import (
	"runtime"
	"time"
)

// RegisterRuntime 注册进程级运行时指标。
//
// 这三项是 24 小时稳定性观测判断「是否泄漏、是否悄悄重启过」的依据：
// 连接数回落而 goroutine 不回落说明每连接的协程没退出；堆内存单调上涨
// 说明有对象被长期持有；启动时间变化说明容器重启过，此前采集的增长趋势
// 作废，必须先查重启原因。
func (registry *Registry) RegisterRuntime(startedAt time.Time) {
	registry.NewGaugeFunc(
		"texas_goroutines",
		"Goroutines currently running in this process.",
		func() int64 { return int64(runtime.NumGoroutine()) },
	)
	registry.NewGaugeFunc(
		"texas_memory_heap_bytes",
		"Bytes of allocated heap objects.",
		func() int64 {
			// ReadMemStats 会短暂 stop-the-world，只在抓取时调用，
			// 抓取频率由采集方控制（观测脚本默认 5 分钟一次）。
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return int64(stats.HeapAlloc)
		},
	)
	registry.NewGaugeFunc(
		"texas_process_start_time_seconds",
		"Unix time when this process started.",
		func() int64 { return startedAt.Unix() },
	)
}
