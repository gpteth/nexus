package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestHistogramCumulativeBuckets(t *testing.T) {
	reg := NewRegistry()
	h := NewHistogramVec("lat", "latency", []float64{0.1, 0.5, 1.0})
	reg.MustRegister(h)

	h.Observe(0.05) // 落入 0.1
	h.Observe(0.2)  // 落入 0.5
	h.Observe(0.8)  // 落入 1.0

	var buf bytes.Buffer
	if _, err := reg.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	expected := map[string]string{
		`lat_bucket{le="0.1"}`:  "1",
		`lat_bucket{le="0.5"}`:  "2",
		`lat_bucket{le="1"}`:    "3",
		`lat_bucket{le="+Inf"}`: "3",
		`lat_count`:             "3",
	}
	for prefix, want := range expected {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, " "+want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected line %s ... %s\nfull output:\n%s", prefix, want, out)
		}
	}
}

func TestCounterAndGauge(t *testing.T) {
	reg := NewRegistry()
	c := NewCounterVec("hits", "hits", "code")
	g := NewGaugeVec("size", "size")
	reg.MustRegister(c)
	reg.MustRegister(g)

	c.Inc("200")
	c.Add(4, "200")
	c.Inc("500")
	g.Set(7)

	var buf bytes.Buffer
	_, _ = reg.WriteTo(&buf)
	out := buf.String()
	if !strings.Contains(out, `hits{code="200"} 5`) {
		t.Errorf("counter value wrong:\n%s", out)
	}
	if !strings.Contains(out, `hits{code="500"} 1`) {
		t.Errorf("counter label wrong:\n%s", out)
	}
	if !strings.Contains(out, "size 7") {
		t.Errorf("gauge wrong:\n%s", out)
	}
}

func TestDuplicateRegistration(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(NewCounterVec("x", "x"))
	if err := reg.Register(NewCounterVec("x", "x")); err == nil {
		t.Fatal("duplicate registration should fail")
	}
}
