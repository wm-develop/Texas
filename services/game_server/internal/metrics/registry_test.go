package metrics

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCounterGaugeAndFuncRenderInPrometheusTextFormat(t *testing.T) {
	registry := NewRegistry()
	requests := registry.NewCounter("texas_http_requests_total", "HTTP requests.", "route", "status")
	active := registry.NewGauge("texas_ws_active", "Active sockets.")
	tables := registry.NewGaugeFunc("texas_tables_active", "Active tables.", func() int64 { return 3 })

	requests.Inc("GET /healthz", "200")
	requests.Inc("GET /healthz", "200")
	requests.Add(5, "POST /v1/auth/login", "429")
	active.Inc()
	active.Inc()
	active.Dec()

	var buffer bytes.Buffer
	registry.Write(&buffer)
	output := buffer.String()

	for _, expected := range []string{
		"# TYPE texas_http_requests_total counter",
		`texas_http_requests_total{route="GET /healthz",status="200"} 2`,
		`texas_http_requests_total{route="POST /v1/auth/login",status="429"} 5`,
		"# TYPE texas_ws_active gauge",
		"texas_ws_active 1",
		"texas_tables_active 3",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("missing %q in:\n%s", expected, output)
		}
	}
	if requests.Value("GET /healthz", "200") != 2 || active.Value() != 1 {
		t.Fatal("accessor values disagree with rendered output")
	}
	_ = tables
}

func TestLabelValuesAreEscaped(t *testing.T) {
	registry := NewRegistry()
	counter := registry.NewCounter("c", "h", "l")
	counter.Inc(`a"b\c` + "\n")
	var buffer bytes.Buffer
	registry.Write(&buffer)
	if !strings.Contains(buffer.String(), `c{l="a\"b\\c\n"} 1`) {
		t.Fatalf("label escaping failed:\n%s", buffer.String())
	}
}

func TestHandlerSetsPrometheusContentType(t *testing.T) {
	registry := NewRegistry()
	registry.NewGauge("g", "h").Set(7)
	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4") {
		t.Fatalf("content type %q", got)
	}
	if !strings.Contains(recorder.Body.String(), "g 7") {
		t.Fatalf("body:\n%s", recorder.Body.String())
	}
}

func TestNilMetricsAreNoOps(t *testing.T) {
	var counter *Counter
	var gauge *Gauge
	counter.Inc("x")
	gauge.Inc()
	gauge.Set(3)
	if counter.Value("x") != 0 || gauge.Value() != 0 {
		t.Fatal("nil metrics must be inert so instrumentation can be optional")
	}
}
