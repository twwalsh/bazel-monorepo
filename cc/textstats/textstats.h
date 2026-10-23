#pragma once
#include <cstddef>
#include <string_view>

namespace textstats {

struct Stats {
  std::size_t chars = 0;
  std::size_t words = 0;
  std::size_t lines = 0;
  // A cheap token estimate (about 4 chars per token for English prose): the number the embedding budget
  // actually cares about. bge-base truncates at 512 tokens; gte-large at 8192.
  std::size_t approx_tokens = 0;
};

Stats Measure(std::string_view text);

}  // namespace textstats
