// Package metrics 提供极简的 Prometheus 文本格式指标暴露。
//
// 项目刻意不引入 prometheus/client_golang 及其一串传递依赖：当前只需要少量
// 计数器与仪表盘，手写实现不到两百行，且输出格式与 Prometheus 完全兼容，
// 后续接入任何抓取器都不需要改动。
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry 汇总所有指标，并把它们按 Prometheus 文本格式写出。
type Registry struct {
	mu       sync.Mutex
	counters []*Counter
	gauges   []*Gauge
	funcs    []*GaugeFunc
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry { return &Registry{} }

// Counter 是只增不减的计数器，可带标签。
type Counter struct {
	name, help string
	labelNames []string
	mu         sync.Mutex
	series     map[string]*atomic.Int64
}

// Gauge 是可增可减的瞬时值。
type Gauge struct {
	name, help string
	value      atomic.Int64
}

// GaugeFunc 在抓取时调用函数取值，适合「当前活跃牌桌数」这类由别处持有的状态。
type GaugeFunc struct {
	name, help string
	read       func() int64
}

// NewCounter 注册一个带可选标签的计数器。
func (registry *Registry) NewCounter(name, help string, labelNames ...string) *Counter {
	counter := &Counter{
		name: name, help: help, labelNames: labelNames,
		series: make(map[string]*atomic.Int64),
	}
	registry.mu.Lock()
	registry.counters = append(registry.counters, counter)
	registry.mu.Unlock()
	return counter
}

// NewGauge 注册一个仪表盘。
func (registry *Registry) NewGauge(name, help string) *Gauge {
	gauge := &Gauge{name: name, help: help}
	registry.mu.Lock()
	registry.gauges = append(registry.gauges, gauge)
	registry.mu.Unlock()
	return gauge
}

// NewGaugeFunc 注册一个抓取时求值的仪表盘。
func (registry *Registry) NewGaugeFunc(name, help string, read func() int64) *GaugeFunc {
	gauge := &GaugeFunc{name: name, help: help, read: read}
	registry.mu.Lock()
	registry.funcs = append(registry.funcs, gauge)
	registry.mu.Unlock()
	return gauge
}

// Inc 把对应标签组合的计数加一。标签值数量必须与注册时的标签名一致。
func (counter *Counter) Inc(labelValues ...string) { counter.Add(1, labelValues...) }

// Add 把对应标签组合的计数加 delta。
func (counter *Counter) Add(delta int64, labelValues ...string) {
	if counter == nil {
		return
	}
	key := labelKey(labelValues)
	counter.mu.Lock()
	entry, exists := counter.series[key]
	if !exists {
		entry = &atomic.Int64{}
		counter.series[key] = entry
	}
	counter.mu.Unlock()
	entry.Add(delta)
}

// Value 返回某个标签组合的当前计数，供测试使用。
func (counter *Counter) Value(labelValues ...string) int64 {
	if counter == nil {
		return 0
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if entry, exists := counter.series[labelKey(labelValues)]; exists {
		return entry.Load()
	}
	return 0
}

// Inc / Dec / Set 操作仪表盘。
func (gauge *Gauge) Inc() {
	if gauge != nil {
		gauge.value.Add(1)
	}
}

func (gauge *Gauge) Dec() {
	if gauge != nil {
		gauge.value.Add(-1)
	}
}

func (gauge *Gauge) Set(value int64) {
	if gauge != nil {
		gauge.value.Store(value)
	}
}

// Value 返回仪表盘当前值。
func (gauge *Gauge) Value() int64 {
	if gauge == nil {
		return 0
	}
	return gauge.value.Load()
}

// Handler 返回暴露指标的 HTTP 处理器。
func (registry *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		registry.Write(writer)
	})
}

// Write 按 Prometheus 文本格式输出全部指标，顺序稳定便于比对。
func (registry *Registry) Write(out io.Writer) {
	registry.mu.Lock()
	counters := append([]*Counter(nil), registry.counters...)
	gauges := append([]*Gauge(nil), registry.gauges...)
	funcs := append([]*GaugeFunc(nil), registry.funcs...)
	registry.mu.Unlock()

	for _, counter := range counters {
		fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s counter\n", counter.name, counter.help, counter.name)
		counter.mu.Lock()
		keys := make([]string, 0, len(counter.series))
		for key := range counter.series {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(out, "%s%s %d\n", counter.name, formatLabels(counter.labelNames, key), counter.series[key].Load())
		}
		// 无标签计数器在首次计数前也输出 0：告警规则里「不存在」与「为 0」
		// 是两回事，rate()/increase() 对缺失序列不会产生任何值。
		if len(counter.labelNames) == 0 && len(counter.series) == 0 {
			fmt.Fprintf(out, "%s 0\n", counter.name)
		}
		counter.mu.Unlock()
	}
	for _, gauge := range gauges {
		fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", gauge.name, gauge.help, gauge.name, gauge.name, gauge.Value())
	}
	for _, gauge := range funcs {
		fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", gauge.name, gauge.help, gauge.name, gauge.name, gauge.read())
	}
}

// labelKey 把标签值序列化为 map 键；使用不可能出现在标签值中的分隔符。
func labelKey(values []string) string {
	return strings.Join(values, "\x00")
}

func formatLabels(names []string, key string) string {
	if len(names) == 0 {
		return ""
	}
	values := strings.Split(key, "\x00")
	parts := make([]string, 0, len(names))
	for index, name := range names {
		value := ""
		if index < len(values) {
			value = values[index]
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, name, escapeLabelValue(value)))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabelValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}
