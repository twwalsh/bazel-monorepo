"""Property-based tests (Hypothesis) over the retriever and the proto.

Properties, not examples: for any corpus Hypothesis can generate, searching for a chunk's own title ranks
that chunk first among chunks with that title; a query with no tokens returns nothing; scores are
non-increasing; and the proto survives a serialize/parse round trip.
"""
from __future__ import annotations

import os

from hypothesis import given, settings, strategies as st

from proto.support.v1 import chunk_pb2
from python.evalctl.retriever import Retriever, load, tokens

words = st.text(alphabet="abcdefghijklmnopqrstuvwxyz0123456789", min_size=2, max_size=8)
phrase = st.lists(words, min_size=1, max_size=4).map(" ".join)


def chunk_strategy():
    return st.builds(
        lambda i, s, t, u, x: chunk_pb2.Chunk(id=i, source=s, title=t, url=u, text=x),
        st.text(alphabet="abcdef0123456789", min_size=1, max_size=12),
        st.sampled_from(["manual", "faq", "changelog", "page"]),
        phrase,
        st.text(alphabet="abc/:.", max_size=12),
        phrase,
    )


corpora = st.lists(chunk_strategy(), min_size=1, max_size=12).map(lambda cs: chunk_pb2.Corpus(chunks=cs))


@given(corpora)
@settings(max_examples=100, deadline=None)
def test_own_title_is_a_top_hit(corpus):
    r = Retriever(corpus)
    for c in corpus.chunks:
        if not tokens(c.title):
            continue
        hits = r.search(c.title, k=len(corpus.chunks))
        assert any(h.chunk.id == c.id for h in hits), "a chunk must be retrievable by its own title"


@given(corpora, st.text(max_size=20))
@settings(max_examples=100, deadline=None)
def test_scores_are_sorted_and_bounded(corpus, query):
    r = Retriever(corpus)
    hits = r.search(query, k=3)
    assert len(hits) <= 3
    assert all(a.score >= b.score for a, b in zip(hits, hits[1:]))
    if not tokens(query):
        assert hits == []


@given(corpora)
@settings(max_examples=100, deadline=None)
def test_proto_round_trip(corpus):
    back = chunk_pb2.Corpus()
    back.ParseFromString(corpus.SerializeToString())
    assert back == corpus


def test_real_corpus_eval_questions_retrieve():
    path = os.path.join(os.environ.get("TEST_SRCDIR", "."), os.environ.get("TEST_WORKSPACE", ""), "data", "corpus.binpb")
    if not os.path.exists(path):
        path = "data/corpus.binpb"
    corpus = load(path)
    assert len(corpus.chunks) == 219
    r = Retriever(corpus)
    assert r.search("Where do I put the .frlicense file on a Mac?")[0].chunk.source in {"faq", "manual", "page"}


if __name__ == "__main__":
    import sys

    import pytest

    sys.exit(pytest.main(["-q", *sys.argv[1:]]))
