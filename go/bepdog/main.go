// bepdog turns a Bazel build into Datadog metrics, through the Datadog Agent's DogStatsD listener.
//
//	bazel test //... --build_event_json_file=/tmp/bep.json
//	bazel run //go/bepdog -- -bep /tmp/bep.json -tag config:local -tag commit:$(git rev-parse --short HEAD)
//
// It reads Bazel's Build Event Protocol stream (one JSON object per line) and emits, under the `bazel.`
// namespace: wall time, actions created and executed, a count per runner (local sandbox, remote, remote
// cache hit, action cache hit, internal) and a cache hit ratio, each test's duration and status, and, with
// -bench, the ns/op, MB/s and allocs/op of Go benchmark output. Metrics go to the Agent over UDP on
// localhost:8125 (DogStatsD); with -api they are posted to the Datadog metrics API instead, for CI runners
// that have no Agent. -dry prints the datagrams and sends nothing.
//
// This is the developer-infrastructure telemetry an SDLC team runs on: how long builds take, how much the
// cache is doing, which tests are slow, whether the benchmark moved, per commit and per config.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// A point is one metric sample with its tags; the sinks format it for DogStatsD or the API.
type point struct {
	name  string
	value float64
	tags  []string
}

type sink interface {
	send(p point) error
	close() error
}

// --- DogStatsD over UDP (the Agent) ---

type statsd struct{ conn net.Conn }

func newStatsd(addr string) (*statsd, error) {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	return &statsd{conn: c}, nil
}

func datagram(p point) string {
	return fmt.Sprintf("%s:%g|g|#%s", p.name, p.value, strings.Join(p.tags, ","))
}

func (s *statsd) send(p point) error { _, err := s.conn.Write([]byte(datagram(p))); return err }
func (s *statsd) close() error       { return s.conn.Close() }

// --- Datadog metrics API (CI runners without an Agent) ---

type apiSink struct {
	site, key string
	series    []map[string]any
	host      string
}

func (a *apiSink) send(p point) error {
	a.series = append(a.series, map[string]any{
		"metric":    p.name,
		"type":      3, // gauge
		"points":    []map[string]any{{"timestamp": time.Now().Unix(), "value": p.value}},
		"tags":      p.tags,
		"resources": []map[string]string{{"name": a.host, "type": "host"}},
	})
	return nil
}

func (a *apiSink) close() error {
	body, _ := json.Marshal(map[string]any{"series": a.series})
	req, err := http.NewRequest("POST", fmt.Sprintf("https://api.%s/api/v2/series", a.site), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DD-API-KEY", a.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("datadog api: %s: %s", resp.Status, b)
	}
	return nil
}

// --- dry run ---

type dry struct{}

func (dry) send(p point) error { fmt.Println(datagram(p)); return nil }
func (dry) close() error       { return nil }

// --- BEP parsing: only the fields used, named as Bazel's JSON encoding names them ---

type event struct {
	ID struct {
		TestResult *struct {
			Label string `json:"label"`
		} `json:"testResult"`
	} `json:"id"`
	Started *struct {
		Command         string `json:"command"`
		StartTimeMillis string `json:"startTimeMillis"`
	} `json:"started"`
	Finished *struct {
		ExitCode struct {
			Name string `json:"name"`
			Code int    `json:"code"`
		} `json:"exitCode"`
	} `json:"finished"`
	BuildMetrics *struct {
		ActionSummary struct {
			ActionsCreated  flexNum `json:"actionsCreated"`
			ActionsExecuted flexNum `json:"actionsExecuted"`
			ActionData      []struct {
				Mnemonic        string  `json:"mnemonic"`
				ActionsCreated  flexNum `json:"actionsCreated"`
				ActionsExecuted flexNum `json:"actionsExecuted"`
			} `json:"actionData"`
			RunnerCount []struct {
				Name  string  `json:"name"`
				Count flexNum `json:"count"`
			} `json:"runnerCount"`
		} `json:"actionSummary"`
		TimingMetrics struct {
			WallTimeInMs flexNum `json:"wallTimeInMs"`
			CPUTimeInMs  flexNum `json:"cpuTimeInMs"`
		} `json:"timingMetrics"`
		TargetMetrics struct {
			TargetsConfigured flexNum `json:"targetsConfigured"`
		} `json:"targetMetrics"`
	} `json:"buildMetrics"`
	TestResult *struct {
		Status                    string  `json:"status"`
		TestAttemptDurationMillis flexNum `json:"testAttemptDurationMillis"`
		TestAttemptDuration       string  `json:"testAttemptDuration"` // newer Bazel: "0.123s"
		CachedLocally             bool    `json:"cachedLocally"`
		ExecutionInfo             struct {
			CachedRemotely bool `json:"cachedRemotely"`
		} `json:"executionInfo"`
	} `json:"testResult"`
}

// flexNum accepts the number-or-string encodings the BEP JSON uses for int64 fields (Bazel writes
// protobuf int64 as strings and int32 as numbers, and has changed its mind between versions).
type flexNum float64

func (n *flexNum) UnmarshalJSON(b []byte) error {
	t := strings.Trim(string(b), `"`)
	if t == "" || t == "null" {
		*n = 0
		return nil
	}
	var f float64
	if _, err := fmt.Sscanf(t, "%g", &f); err != nil {
		return err
	}
	*n = flexNum(f)
	return nil
}

func num(n flexNum) float64 { return float64(n) }

func seconds(d string) float64 {
	if d == "" {
		return 0
	}
	v, err := time.ParseDuration(d)
	if err != nil {
		return 0
	}
	return v.Seconds()
}

// runnerTag normalizes Bazel's runner names ("remote cache hit", "darwin-sandbox") into tag-safe values.
func runnerTag(name string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(name))
}

func readBEP(r io.Reader, tags []string, out sink) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	n := 0
	emit := func(name string, v float64, extra ...string) {
		p := point{name: name, value: v, tags: append(append([]string{}, tags...), extra...)}
		if err := out.send(p); err != nil {
			fmt.Fprintln(os.Stderr, "send:", err)
		}
		n++
	}
	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // not every line of a BEP file is an event we model
		}
		switch {
		case e.Finished != nil:
			emit("bazel.build.exit_code", float64(e.Finished.ExitCode.Code), "exit:"+strings.ToLower(e.Finished.ExitCode.Name))
		case e.BuildMetrics != nil:
			m := e.BuildMetrics
			emit("bazel.build.wall_ms", num(m.TimingMetrics.WallTimeInMs))
			emit("bazel.build.cpu_ms", num(m.TimingMetrics.CPUTimeInMs))
			created := num(m.ActionSummary.ActionsCreated)
			if created == 0 { // newer Bazel reports creation counts per mnemonic only
				for _, a := range m.ActionSummary.ActionData {
					created += num(a.ActionsCreated)
				}
			}
			emit("bazel.build.actions.created", created)
			emit("bazel.build.actions.executed", num(m.ActionSummary.ActionsExecuted))
			if t := num(m.TargetMetrics.TargetsConfigured); t > 0 {
				emit("bazel.build.targets.configured", t)
			}
			var hits, work float64
			for _, rc := range m.ActionSummary.RunnerCount {
				c := num(rc.Count)
				emit("bazel.build.runner.count", c, "runner:"+runnerTag(rc.Name))
				if strings.Contains(rc.Name, "cache hit") {
					hits += c
				}
				if rc.Name != "internal" && rc.Name != "total" {
					work += c
				}
			}
			if work > 0 {
				emit("bazel.build.cache.hit_ratio", hits/work)
			}
		case e.TestResult != nil && e.ID.TestResult != nil:
			t := e.TestResult
			d := num(t.TestAttemptDurationMillis)
			if d == 0 {
				d = seconds(t.TestAttemptDuration) * 1000
			}
			label := strings.TrimPrefix(e.ID.TestResult.Label, "//")
			cached := "none"
			if t.CachedLocally {
				cached = "local"
			} else if t.ExecutionInfo.CachedRemotely {
				cached = "remote"
			}
			emit("bazel.test.duration_ms", d, "target:"+label, "status:"+strings.ToLower(t.Status), "cached:"+cached)
			pass := 0.0
			if t.Status == "PASSED" {
				pass = 1
			}
			emit("bazel.test.passed", pass, "target:"+label)
		}
	}
	return n, sc.Err()
}

// Go benchmark output: "BenchmarkReadJSONL-12    694   1715610 ns/op   126.80 MB/s   2016546 B/op   3031 allocs/op"
var benchLine = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op(?:\s+([\d.]+) MB/s)?(?:\s+(\d+) B/op)?(?:\s+(\d+) allocs/op)?`)

func readBench(r io.Reader, tags []string, out sink) (int, error) {
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		m := benchLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		t := append(append([]string{}, tags...), "bench:"+m[1])
		send := func(name, v string) {
			if v == "" {
				return
			}
			var f float64
			fmt.Sscanf(v, "%g", &f)
			if err := out.send(point{name: name, value: f, tags: t}); err != nil {
				fmt.Fprintln(os.Stderr, "send:", err)
			}
			n++
		}
		send("bazel.bench.ns_per_op", m[2])
		send("bazel.bench.mb_per_s", m[3])
		send("bazel.bench.bytes_per_op", m[4])
		send("bazel.bench.allocs_per_op", m[5])
	}
	return n, sc.Err()
}

type tagList []string

func (t *tagList) String() string     { return strings.Join(*t, ",") }
func (t *tagList) Set(s string) error { *t = append(*t, s); return nil }

func main() {
	bep := flag.String("bep", "", "Bazel --build_event_json_file to read")
	bench := flag.String("bench", "", "file of Go benchmark output to read")
	addr := flag.String("statsd", "127.0.0.1:8125", "DogStatsD address (the Agent)")
	api := flag.Bool("api", false, "post to the Datadog API (DD_API_KEY, DD_SITE) instead of DogStatsD")
	host := flag.String("host", "", "host name to attach when posting via -api")
	dryRun := flag.Bool("dry", false, "print datagrams, send nothing")
	var tags tagList
	flag.Var(&tags, "tag", "tag to attach to every metric (repeatable), e.g. -tag config:remote")
	flag.Parse()
	if *bep == "" && *bench == "" {
		fmt.Fprintln(os.Stderr, "usage: bepdog -bep file [-bench file] [-tag k:v]... [-statsd host:port | -api | -dry]")
		os.Exit(2)
	}

	var out sink
	var err error
	switch {
	case *dryRun:
		out = dry{}
	case *api:
		key := os.Getenv("DD_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-api needs DD_API_KEY")
			os.Exit(2)
		}
		site := os.Getenv("DD_SITE")
		if site == "" {
			site = "datadoghq.com"
		}
		h := *host
		if h == "" {
			h, _ = os.Hostname()
		}
		out = &apiSink{site: site, key: key, host: h}
	default:
		out, err = newStatsd(*addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	total := 0
	if *bep != "" {
		f, err := os.Open(*bep)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		n, err := readBEP(f, tags, out)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		total += n
	}
	if *bench != "" {
		f, err := os.Open(*bench)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		n, err := readBench(f, tags, out)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		total += n
	}
	if err := out.close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "bepdog: %d metrics\n", total)
}
