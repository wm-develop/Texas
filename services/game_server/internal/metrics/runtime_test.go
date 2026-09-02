package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestRuntimeMetricsExposeGoroutinesHeapAndStartTime(t *testing.T) {
	startedAt := time.Unix(1_756_000_000, 0)
	registry := NewRegistry()
	registry.RegisterRuntime(startedAt)

	var builder strings.Builder
	registry.Write(&builder)
	rendered := builder.String()

	for _, name := range []string{
		"texas_goroutines",
		"texas_memory_heap_bytes",
		"texas_process_start_time_seconds",
	} {
		if !strings.Contains(rendered, "# TYPE "+name+" gauge") {
			t.Fatalf("%s missing from /metrics output:\n%s", name, rendered)
		}
	}
	if !strings.Contains(rendered, "texas_process_start_time_seconds 1756000000") {
		t.Fatalf("start time must be reported verbatim:\n%s", rendered)
	}
	// 观测脚本靠这两个值判断泄漏，取到 0 会让整轮观测失去意义
	for _, line := range strings.Split(rendered, "\n") {
		for _, name := range []string{"texas_goroutines ", "texas_memory_heap_bytes "} {
			if strings.HasPrefix(line, name) && strings.HasSuffix(line, " 0") {
				t.Fatalf("%s reported zero: %q", name, line)
			}
		}
	}
}
