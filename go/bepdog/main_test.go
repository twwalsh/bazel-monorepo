package main

import (
	"strings"
	"testing"
)

type capture struct{ got []point }

func (c *capture) send(p point) error { c.got = append(c.got, p); return nil }
func (c *capture) close() error       { return nil }

const bep = `{"id":{"started":{}},"started":{"command":"test","startTimeMillis":"1"}}
{"id":{"buildMetrics":{}},"buildMetrics":{"actionSummary":{"actionsCreated":"1200","actionsExecuted":"1060","runnerCount":[{"name":"total","count":1063},{"name":"remote cache hit","count":699},{"name":"internal","count":"363"},{"name":"darwin-sandbox","count":1}]},"timingMetrics":{"wallTimeInMs":"38190","cpuTimeInMs":"2000"},"targetMetrics":{"targetsConfigured":"16"}}}
{"id":{"testResult":{"label":"//python/evalctl:retriever_test","run":1,"shard":1,"attempt":1}},"testResult":{"status":"PASSED","testAttemptDurationMillis":"4200","cachedLocally":false,"executionInfo":{"cachedRemotely":true}}}
not json at all
{"id":{"buildFinished":{}},"finished":{"exitCode":{"name":"SUCCESS","code":0}}}
`

func find(ps []point, name string, tag string) (point, bool) {
	for _, p := range ps {
		if p.name != name {
			continue
		}
		if tag == "" || strings.Contains(strings.Join(p.tags, ","), tag) {
			return p, true
		}
	}
	return point{}, false
}

func TestReadBEP(t *testing.T) {
	c := &capture{}
	n, err := readBEP(strings.NewReader(bep), []string{"config:test"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no metrics")
	}
	if p, ok := find(c.got, "bazel.build.wall_ms", ""); !ok || p.value != 38190 {
		t.Fatalf("wall_ms: %+v %v", p, ok)
	}
	if p, ok := find(c.got, "bazel.build.runner.count", "runner:remote_cache_hit"); !ok || p.value != 699 {
		t.Fatalf("runner count: %+v %v", p, ok)
	}
	// 699 hits out of 700 non-internal processes.
	if p, ok := find(c.got, "bazel.build.cache.hit_ratio", ""); !ok || p.value < 0.998 || p.value > 0.999 {
		t.Fatalf("hit ratio: %+v %v", p, ok)
	}
	if p, ok := find(c.got, "bazel.test.duration_ms", "target:python/evalctl:retriever_test"); !ok || p.value != 4200 || !strings.Contains(strings.Join(p.tags, ","), "cached:remote") {
		t.Fatalf("test duration: %+v %v", p, ok)
	}
	if p, ok := find(c.got, "bazel.build.exit_code", "exit:success"); !ok || p.value != 0 {
		t.Fatalf("exit code: %+v %v", p, ok)
	}
	for _, p := range c.got {
		if !strings.Contains(strings.Join(p.tags, ","), "config:test") {
			t.Fatalf("missing common tag on %s", p.name)
		}
	}
}

func TestReadBench(t *testing.T) {
	c := &capture{}
	n, err := readBench(strings.NewReader("goos: darwin\nBenchmarkReadJSONL-12    694   1715610 ns/op   126.80 MB/s   2016546 B/op   3031 allocs/op\nPASS\n"), nil, c)
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if p, ok := find(c.got, "bazel.bench.ns_per_op", "bench:BenchmarkReadJSONL"); !ok || p.value != 1715610 {
		t.Fatalf("ns/op: %+v %v", p, ok)
	}
	if p, ok := find(c.got, "bazel.bench.mb_per_s", ""); !ok || p.value != 126.80 {
		t.Fatalf("MB/s: %+v %v", p, ok)
	}
}

func TestDatagramFormat(t *testing.T) {
	d := datagram(point{name: "bazel.build.wall_ms", value: 38190, tags: []string{"config:gcs", "commit:abc"}})
	if d != "bazel.build.wall_ms:38190|g|#config:gcs,commit:abc" {
		t.Fatal(d)
	}
}
