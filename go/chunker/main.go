// chunker converts the site's JSONL chunk export into a Corpus protobuf binary.
//
//	bazel run //go/chunker -- -in data/chunks.jsonl -out /tmp/corpus.binpb
//
// It is the one producer in the graph: //data:corpus is a genrule over this binary, and the Python and C++
// targets consume its output, so a schema change in proto/support/v1 rebuilds all three languages.
package main

import (
	"flag"
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/twwalsh/bazel-monorepo/go/chunker/convert"
)

func main() {
	in := flag.String("in", "", "JSONL file of chunks")
	out := flag.String("out", "", "output Corpus protobuf binary")
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: chunker -in chunks.jsonl -out corpus.binpb")
		os.Exit(2)
	}
	f, err := os.Open(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	corpus, err := convert.ReadJSONL(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, err := proto.Marshal(corpus)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %d chunks (%d bytes) to %s\n", len(corpus.Chunks), len(b), *out)
}
