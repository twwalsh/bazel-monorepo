// corpusstats: read a Corpus protobuf binary and report size statistics per source, including how many
// chunks would be truncated by a 512-token embedding window. The C++ consumer of the shared schema.
//
//   bazel run //cc/textstats:corpusstats -- $(bazel info bazel-bin)/data/corpus.binpb
#include <cstdio>
#include <fstream>
#include <iterator>
#include <map>
#include <string>

#include "cc/textstats/textstats.h"
#include "proto/support/v1/chunk.pb.h"

int main(int argc, char** argv) {
  if (argc != 2) {
    std::fprintf(stderr, "usage: corpusstats corpus.binpb\n");
    return 2;
  }
  std::ifstream in(argv[1], std::ios::binary);
  std::string bytes((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
  support::v1::Corpus corpus;
  if (!corpus.ParseFromString(bytes)) {
    std::fprintf(stderr, "could not parse %s\n", argv[1]);
    return 1;
  }
  struct Agg { int n = 0; std::size_t words = 0; std::size_t tokens = 0; int over512 = 0; };
  std::map<std::string, Agg> by_source;
  for (const auto& c : corpus.chunks()) {
    auto s = textstats::Measure(c.text());
    auto& a = by_source[c.source()];
    ++a.n;
    a.words += s.words;
    a.tokens += s.approx_tokens;
    if (s.approx_tokens > 512) ++a.over512;
  }
  std::printf("%-10s %6s %9s %10s %8s\n", "source", "chunks", "words", "~tokens", ">512tok");
  for (const auto& [src, a] : by_source)
    std::printf("%-10s %6d %9zu %10zu %8d\n", src.c_str(), a.n, a.words, a.tokens, a.over512);
  std::printf("%-10s %6d\n", "total", corpus.chunks_size());
  return 0;
}
