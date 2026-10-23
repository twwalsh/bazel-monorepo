"""evalctl: check that every evaluation question retrieves at least one passage from the corpus.

    bazel run //py/evalctl -- --corpus $(bazel info bazel-bin)/data/corpus.binpb --eval data/eval.jsonl

Exit 1 if any question retrieves nothing. This is the retrieval half of the 30-case eval the two RAG builds
share; the generation half needs a model and lives in those repos.
"""
from __future__ import annotations

import argparse
import json
import sys

from python.evalctl.retriever import Retriever, load


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus", required=True)
    ap.add_argument("--eval", required=True)
    ap.add_argument("--k", type=int, default=5)
    args = ap.parse_args()

    r = Retriever(load(args.corpus))
    misses = 0
    n = 0
    with open(args.eval, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            n += 1
            case = json.loads(line)
            hits = r.search(case["q"], args.k)
            if not hits:
                misses += 1
                print(f"MISS  {case['q']}")
            else:
                print(f"ok    {case['q'][:60]:60}  -> {hits[0].chunk.title[:50]}")
    print(f"\n{n - misses}/{n} questions retrieve a passage (k={args.k})")
    return 1 if misses else 0


if __name__ == "__main__":
    sys.exit(main())
