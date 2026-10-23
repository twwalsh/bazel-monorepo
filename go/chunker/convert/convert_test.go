package convert

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"google.golang.org/protobuf/proto"
)

// Property: for any record whose text has no surrounding whitespace, JSONL -> Corpus -> Record is the identity,
// and the proto survives a Marshal/Unmarshal round trip. testing/quick generates the records.
func TestRoundTripProperty(t *testing.T) {
	prop := func(id, source, title, url, text string) bool {
		if id == "" {
			id = "x"
		}
		text = strings.TrimSpace(text)
		in := Record{ID: id, Source: source, Title: title, URL: url, Text: text}
		line, err := json.Marshal(in)
		if err != nil {
			return false
		}
		corpus, err := ReadJSONL(bytes.NewReader(append(line, '\n')))
		if err != nil || len(corpus.Chunks) != 1 {
			return false
		}
		b, err := proto.Marshal(corpus)
		if err != nil {
			return false
		}
		var back = corpus.ProtoReflect().New().Interface()
		if err := proto.Unmarshal(b, back); err != nil {
			return false
		}
		got := FromChunk(corpus.Chunks[0])
		return reflect.DeepEqual(in, got) && proto.Equal(corpus, back)
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 2000, Rand: rand.New(rand.NewSource(1))}); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedLineIsAnError(t *testing.T) {
	_, err := ReadJSONL(strings.NewReader(`{"id":"a","text":"ok"}` + "\n" + `{"id":` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("want a line-2 error, got %v", err)
	}
}

func TestMissingIDIsAnError(t *testing.T) {
	_, err := ReadJSONL(strings.NewReader(`{"text":"no id"}` + "\n"))
	if err == nil {
		t.Fatal("want an error for a chunk without an id")
	}
}

func TestRealCorpus(t *testing.T) {
	f, err := os.Open("../../../data/chunks.jsonl")
	if err != nil {
		t.Skip("corpus not available in this sandbox")
	}
	defer f.Close()
	corpus, err := ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Chunks) != 219 {
		t.Fatalf("want 219 chunks, got %d", len(corpus.Chunks))
	}
}

// Benchmark: parse the real corpus. Run with `bazel run //go/chunker/convert:convert_test -- -test.bench=. -test.run=^$`
// or through //tools:bench, which records the result in CI.
func BenchmarkReadJSONL(b *testing.B) {
	data, err := os.ReadFile("../../../data/chunks.jsonl")
	if err != nil {
		b.Skip("corpus not available")
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ReadJSONL(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}
