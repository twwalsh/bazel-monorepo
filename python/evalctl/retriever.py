"""A deliberately simple lexical retriever over the Corpus proto, so the property tests have something real
to hold to. It is not the production retriever (that is Vectorize on Cloudflare and Vector Search on
Databricks); it is the smallest thing that makes "the eval questions find their passages" a testable claim.
"""
from __future__ import annotations

import re
from collections import Counter
from dataclasses import dataclass

from proto.support.v1 import chunk_pb2

TOKEN = re.compile(r"[a-z0-9][a-z0-9.+'-]*")
STOP = frozenset("a an and are as at be by for from how i in is it of on or the to what where which with you your my do does".split())


def tokens(text: str) -> list[str]:
    return [t for t in TOKEN.findall(text.lower()) if t not in STOP]


@dataclass(frozen=True)
class Hit:
    chunk: chunk_pb2.Chunk
    score: float


class Retriever:
    def __init__(self, corpus: chunk_pb2.Corpus):
        self.chunks = list(corpus.chunks)
        self._tf = [Counter(tokens(f"{c.title} {c.text}")) for c in self.chunks]
        df: Counter[str] = Counter()
        for tf in self._tf:
            df.update(tf.keys())
        n = max(1, len(self.chunks))
        self._idf = {t: 1.0 + (n / (1 + d)) ** 0.5 for t, d in df.items()}

    def search(self, query: str, k: int = 5) -> list[Hit]:
        q = tokens(query)
        if not q:
            return []
        scored = []
        for chunk, tf in zip(self.chunks, self._tf):
            s = sum(tf[t] * self._idf.get(t, 1.0) for t in q)
            if s > 0:
                scored.append(Hit(chunk, s))
        scored.sort(key=lambda h: (-h.score, h.chunk.id))
        return scored[:k]


def load(path: str) -> chunk_pb2.Corpus:
    corpus = chunk_pb2.Corpus()
    with open(path, "rb") as f:
        corpus.ParseFromString(f.read())
    return corpus
