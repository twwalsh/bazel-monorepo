# bazel-monorepo: one schema, three languages, one cache

A small monorepo under **Bazel** where Go, Python and C++ targets share a single protobuf schema, build
hermetically, and share a **remote cache on Google Cloud Storage** between laptops and CI, and run on
**BuildBuddy remote executors** with `--config=remote`. Releases carry
**SLSA build provenance** and a **keyless Sigstore signature**, and every build's timings, cache hits and
test latencies go to **Datadog** through the Agent. Tests include **property-based tests**
(Hypothesis in Python, `testing/quick` in Go, a seeded randomized check in C++) and a **benchmark** recorded
on every CI run.

It exists to answer a specific question with working code rather than a claim: what does a multi-language
SDLC look like when the build is one dependency graph, the cache is shared, and the artifact that ships can
prove where it came from. The content is the support corpus from [fruitlessresearch.com](https://fruitlessresearch.com),
the same 219 chunks and 30 evaluation questions the [Cloudflare](https://fruitlessresearch.com) and
[Databricks](https://github.com/twwalsh/databricks-rag) RAG builds use, so the three projects line up.

## The graph

```
proto/support/v1/chunk.proto          one schema: Chunk, Corpus
   ├── chunk_go_proto  ─▶ //go/chunker          Go: JSONL export ─▶ Corpus protobuf binary
   │                        └─▶ //data:corpus   genrule: runs the Go binary, produces corpus.binpb
   ├── chunk_py_proto  ─▶ //python/evalctl          Python: lexical retriever + the 30 eval questions
   └── chunk_cc_proto  ─▶ //cc/textstats        C++: per-source size stats, 512-token truncation count
```

Change one field in the `.proto` and Bazel rebuilds exactly the Go producer, regenerates the artifact, and
rebuilds and retests the Python and C++ consumers. Nothing else. That is the point of the graph.

## Run it

```bash
bazel test //...                                       # everything, hermetic Go/Python/C++ toolchains
bazel run //cc/textstats:corpusstats -- $PWD/bazel-bin/data/corpus.binpb
bazel run //python/evalctl -- --corpus $PWD/bazel-bin/data/corpus.binpb --eval $PWD/data/eval.jsonl
bazel run //go/chunker/convert:convert_test -- -test.run='^$' -test.bench=.      # the benchmark
```

Bazel is pinned by `.bazelversion`; `bazelisk` fetches it. Go, Python 3.12 and their dependencies are
fetched by Bazel from `MODULE.bazel`; the C++ toolchain is the host's.

## The remote cache

`.bazelrc` defines `--config=gcs`: Bazel treats a GCS bucket as an HTTP cache and uploads local results.

```bash
gcloud auth application-default login          # once, locally
bazel test --config=gcs //...                  # second machine, or CI: cache hits instead of builds
```

Measured on this repo's CI: the first Linux run executed all 699 actions in 284 s; the next run, with nothing
changed, hit the cache for 699 of 699 and finished in 38 s.

CI authenticates to the bucket through **Workload Identity Federation**: GitHub's OIDC token is exchanged for
a short-lived token for a service account that can read and write that one bucket and nothing else. There is
no service-account key in the repo, in GitHub secrets, or on disk. `scripts/gcp-setup.sh` creates the bucket
(14-day object lifecycle), the account, the pool and the provider, and prints the two secret values.

## Remote execution

`--config=remote` runs every action on **BuildBuddy**'s hosted Linux x86_64 executors instead of the local
machine, with results streamed to the BuildBuddy UI (per-action timeline, cache hit rates, test logs):

```bash
bazel test --config=remote //...        # API key in user.bazelrc: build:remote --remote_header=x-buildbuddy-api-key=…
```

The pieces that make it work from a Mac, each of which bit on the way in:

- the execution platform must be the **explicit** `platform_linux_x86_64`, not the `:platform` alias, which
  resolves to the host (darwin arm64) and schedules actions the pool cannot run;
- `BAZEL_DO_NOT_DETECT_CPP_TOOLCHAIN=1`, so Apple clang is never configured and BuildBuddy's Ubuntu gcc
  toolchain is the only C++ toolchain in play;
- a **Linux Go SDK** alongside the host one (`go_sdk.download(goos = "linux", goarch = "amd64")`), so Go
  tools run on the executors;
- pip wheels resolved for **both** `linux_x86_64` and `osx_aarch64` (`target_platforms` on `pip.parse`, with
  the index URL so resolution uses the hashed lock rather than the host's pip).

BuildBuddy brings its own cache, so `--config=remote` uses that one and `--config=gcs` uses the bucket; the two
are never combined. A real team runs one backend. Both are kept here because they show different things: the
bucket demonstrates a keyless, federated-identity cache a team can own outright; BuildBuddy demonstrates
execution and the per-action view that comes with an RBE service.

Measured: from the laptop, a cold `bazel test --config=remote //...` ran 351 actions remotely and took 348
from BuildBuddy's cache, all three test suites passing on Linux executors; the CI job `remote-execution`
runs the same 699-action suite remotely on every push. The key lives in a gitignored `user.bazelrc` and a
CI secret, never in the repo.

## Build telemetry to Datadog

`go/bepdog` turns a build into metrics. Bazel writes its Build Event Protocol stream to a file
(`--build_event_json_file`); the tool reads it and emits, under `bazel.*`: wall and CPU time, actions created
and executed, a count per runner (local sandbox, remote, remote cache hit, internal) and the cache hit ratio,
each test's duration, status and whether it was served from cache, and, from Go benchmark output, ns/op, MB/s
and allocs/op. Every metric is tagged with commit, branch, config and repo.

```bash
tools/bazel-dd.sh test --config=gcs //...      # run, then ship through the local Datadog Agent (DogStatsD)
BEPDOG_SINK=api tools/bazel-dd.sh test //...   # CI: post straight to the metrics API (DD_API_KEY)
BEPDOG_SINK=dry tools/bazel-dd.sh test //...   # print the datagrams, send nothing
```

Locally the metrics go to the Datadog Agent on the machine over UDP, which is the shape the Agent is built
for; in CI the `Ship build metrics to Datadog` step posts the same metrics to the API, since runners carry no
Agent. The point is the same one the rest of this repo makes: build time, cache effectiveness, test latency
and benchmark drift are things an SDLC team should see on a graph per commit, not reconstruct from logs.

## Provenance and signing

`.github/workflows/ci.yml` builds the release binary and the corpus artifact and then:

- attaches a **SLSA build provenance** attestation (`actions/attest-build-provenance`), verifiable with
  `gh attestation verify dist/chunker-linux-amd64 --owner twwalsh`;
- signs the binary with **cosign, keyless**, against the workflow's own OIDC identity, and verifies the
  signature in the same job so a broken signing step fails the build rather than shipping unsigned.

A consumer can therefore check that an artifact was built by this workflow, in this repository, from a given
commit, before running it. That is the shape of supply-chain control a public repository that ships into
other people's infrastructure needs; this is the small version.

## Correctness

- **Property-based tests**: Hypothesis generates corpora and queries for the Python retriever (a chunk is
  retrievable by its own title; scores are sorted; empty queries return nothing; the proto round-trips).
  Go's `testing/quick` generates records for the JSONL-to-proto round trip. The C++ test checks invariants over
  five thousand random strings with a fixed seed.
- **Benchmark**: `BenchmarkReadJSONL` parses the real corpus; CI records ns/op and MB/s in the job summary on
  every run, so a regression is visible in history before anyone decides whether to gate on it.
- **Eval**: `evalctl` confirms every one of the 30 evaluation questions retrieves a passage from the corpus
  (30/30 with the lexical retriever at k=5). The generation half of that eval lives in the RAG repos.

## What is deliberately small

The corpus is 219 chunks; the binary is one Go program. The mechanics are the real ones (hermetic
multi-language build, content-addressed shared cache, federated identity, attestation, keyless signing,
property tests, recorded benchmarks); the scale is not. Built 2026-10-23.
