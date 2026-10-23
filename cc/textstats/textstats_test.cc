#include "cc/textstats/textstats.h"

#include <cstdio>
#include <cstdlib>
#include <random>
#include <string>

// Property checks without a test framework: for random strings, words <= chars, lines <= chars + 1,
// tokens ~ chars/4, and the empty string measures as all zeros. Deterministic seed so a failure reproduces.
static void Check(bool ok, const char* what) {
  if (!ok) {
    std::fprintf(stderr, "FAIL: %s\n", what);
    std::exit(1);
  }
}

int main() {
  using textstats::Measure;
  auto z = Measure("");
  Check(z.chars == 0 && z.words == 0 && z.lines == 0 && z.approx_tokens == 0, "empty string is zero");
  auto a = Measure("one two  three\nfour");
  Check(a.words == 4 && a.lines == 2, "counts words and lines");

  std::mt19937 rng(7);
  std::uniform_int_distribution<int> len(0, 400), ch(0, 127);
  for (int i = 0; i < 5000; ++i) {
    std::string s;
    int n = len(rng);
    for (int j = 0; j < n; ++j) s.push_back(static_cast<char>(ch(rng)));
    auto m = Measure(s);
    Check(m.chars == s.size(), "chars == size");
    Check(m.words <= m.chars, "words <= chars");
    Check(m.lines <= m.chars + 1, "lines <= chars + 1");
    Check(m.approx_tokens == (m.chars + 3) / 4, "token estimate is chars/4 rounded up");
  }
  std::puts("ok");
  return 0;
}
