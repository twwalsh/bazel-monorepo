#include "cc/textstats/textstats.h"

#include <cctype>

namespace textstats {

Stats Measure(std::string_view text) {
  Stats s;
  s.chars = text.size();
  bool in_word = false;
  for (unsigned char c : text) {
    if (c == '\n') ++s.lines;
    if (std::isspace(c)) {
      in_word = false;
    } else if (!in_word) {
      in_word = true;
      ++s.words;
    }
  }
  if (!text.empty() && text.back() != '\n') ++s.lines;
  s.approx_tokens = (s.chars + 3) / 4;
  return s;
}

}  // namespace textstats
