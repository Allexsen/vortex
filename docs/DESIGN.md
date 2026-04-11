# Vortex Design Document

Last updated: 2026-04-11

---

## 1. System Overview

Vortex is a distributed web crawler and semantic search engine. It crawls web pages, extracts content, generates vector embeddings, and enables semantic similarity search. The system is polyglot: Go handles crawling, HTTP serving, and orchestration; Python handles ML inference for text embeddings.

### Architecture

```
                                   ┌─────────────┐
                                   │   Manager   │◀───────────┐  monitors depth
                                   └──────┬──────┘             │
                                          │                    │
                                          ▼                    │
  ┌────────┐   ┌───────────────┐   ┌──────────────┐   ┌─────────────────┐   ┌──────────┐   ┌──────────┐
  │ Seeder │─▶│ FrontierQueue  │─▶│  Go Workers  │─▶│ ProcessingQueue  │─▶│ Embedder │─▶│ Postgres │
  └────────┘   │  (RabbitMQ)   │   │    (x50)     │   │    (RabbitMQ)   │   │ (Python) │   │+ pgvector│
               └───────▲───────┘   └──────┬───────┘   └─────────────────┘   └──────────┘   └────┬─────┘
                       │                  │                                                     │
                       │                  │ reads / writes                                      ▼
                       │                  ▼                                               ┌──────────┐
                       │           ┌─────────────┐                                        │  Search  │──▶ UI / JSON
                       │ republish │    Redis    │                                        │    API   │
                       │           │ bloom       │                                        └──────────┘
                       │           │ rate limits │
                       │           │ robots      │
                       │           │ cooldown    │
                       │           │ control     │
                       │           └──────┬──────┘
                       │                  │ pops expired
                ┌──────┴──────┐           │
                │   Poller    │◀─────────┘
                │  (5s tick)  │
                └─────────────┘

  Notes
  • Workers also publish extracted URLs back into FrontierQueue (after bloom dedup).
  • FrontierQueue and ProcessingQueue are not directly connected, but the worker manager directly
    influencers workers (and therefore the frontier queue) according to the processing queue depth.
  • Every service exports Prometheus metrics → Grafana dashboards.
```

### Services

| Service       | Language | Purpose                                                          |
|---------------|----------|------------------------------------------------------------------|
| `seeder`      | Go       | Publishes initial seed URLs into the frontier queue              |
| `crawler`     | Go       | Fetches, parses, and extracts URLs; 50 concurrent workers        |
| `embedder`    | Python   | Chunks and embeds page text; writes vectors to Postgres          |
| `search`      | Go       | HTTP API and minimal UI for semantic queries                     |
| RabbitMQ      | —        | Frontier and processing queues with dead-letter queues           |
| Redis         | —        | Bloom filter, token buckets, robots cache, cooldown, control     |
| Postgres      | —        | Article storage + pgvector embedding search                      |
| Prometheus    | —        | Metrics scraping                                                 |
| Grafana       | —        | Dashboards                                                       |

---

## 2. Key Features

**Distributed crawling**
- 50 concurrent crawlers (configurable)
- URL deduplication via RedisBloom
- Bounded BFS with max depth == 3 (configurable)
- Per-domain token bucket rate limiting via Redis
- Cooldown delay "queue" (Redis sorted set) for rate-limited tasks

**Polite crawling**
- Full `robots.txt` compliance via [`temoto/robotstxt`](https://github.com/temoto/robotstxt)
- Cached in Redis for 24 hours
- Errors are treated as transient. Next worker retries the fetch
- Not found, 5xx and unparsable are treated as allow all
- Internal limit of rate = 1req/sec/domain, burst = 5

**Backpressure-aware auto-throttle**
- A `Manager` goroutine controls crawlers via processing queue depth
- Hysteresis thresholds (pause at 5000, resume at 2500) to prevent flapping
- Workers & Manager still honor the context cancellation
- Manual control override takes priority over the auto-throttle

**Semantic search**
- Sentence-transformer embeddings (768-dim) via `sentence-transformers`
- HNSW index on `pgvector` for sub-linear cosine similarity queries
- Go search API embeds the query through the Python service and returns ranked chunks with their source articles
- Per-IP rate limiting in the search API

**Observability**
- Structured JSON logging (`slog`) with propagated trace IDs across the pipeline
- Prometheus metrics at every relevant stage
- Grafana dashboards provisioned automatically via Docker Compose
- Graceful shutdown across every service via a shared `context.Context` tree

---

## 3. Some details & decision-making

### General
1. A bit of "premature optimization/abstraction": Cleaner code, better & easier mental modeling, separation by functionalities & labeling. An argument could be made this should've been applied more, but I prioritized flow readability in certain cases.
2. Almost everything being a config or a package level constant: No hardcoded values. Each variable gets to have a name/label, and each control variable is defined in an easily accessible way, to the best of my judgement of balancing between flexibility, robustness and simplicity.
Note: As of writing this doc there still are some leftover hardcoded values and the cleanup is planned.
3. Little to no comments: A lot of effort went into the self-descriptive coding style. Too complex, invisible or easy to miss logic segments still contain a bit of reminder comments. An argument could be made to add comments for exported functions. Left to be cycled back.

### Seeder
1. embedded file over passed args and/or mounted file: It's baked in the  binary, virtually impossible to mess up routing. The seeder URL list could be wrong, not suitable for args.

### Worker
1. Crawl delay behavior: time.Sleep(crawlDelay) idles the worker for that duration. This rarely is more than 10s. If not, the task times out (60s default), gets retried up to 3 times, and dropped into the frontier DLQ unless allowed. Prioritized simplicity over a rare case speed/resource optimization.
2. Writing directly into the frontier: Simplicity, again. An argument could be made for 2 alternating queues to clearly separate crawl depths. However, I don't think that adds any value in the scope of this project.
3. Bloom Filter over DB: Speed, memory, and simplicity. For the scope of Vortex, Bloom Filter is perfectly suited. The overhead of full db schema isn't worth to fix a potential ~1% false negatives.

### Embedder
1. Python over Go: Much more mature ML ecosystem.
2. Directly writes to Postgres: Simplicity & horizontal scaling. Could potentially return the embeddings of a chunk and handle the Postgres to Go entirely. Could communicate either via HTTP or another RabbitMQ queue. RabbitMQ isn't worth the overhead, and HTTP would disallow container scaling.

---

## 4. Message Schemas

### CrawlTask (Seeder/Worker to FrontierQueue)

```json
{
  "trace_id": "uuid-v4",
  "url": "https://example.com/page",
  "attempt": 0,
  "depth": 0,
  "enqueued_at": "2026-04-07T12:00:00Z"
}
```

- `trace_id`: Unique identifier for the original seed URL's crawl tree
- `attempt`: Retry counter, incremented on transient errors (max 3)
- `depth`: How many hops from the seed URL (max 3)

### CrawlResult (Worker to ProcessingQueue)

```json
{
  "trace_id": "uuid-v4",
  "url": "https://example.com/page",
  "content": "extracted plain text...",
  "created_at": "2026-04-07T12:00:05Z"
}
```

---

## 5. Database Schema

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE articles (
    id          BIGSERIAL PRIMARY KEY,
    trace_id    TEXT NOT NULL,
    url         TEXT NOT NULL UNIQUE,
    content     TEXT NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE chunks (
    id          BIGSERIAL PRIMARY KEY,
    article_id  BIGINT REFERENCES articles(id) ON DELETE CASCADE,
    chunk_index INT NOT NULL,
    chunk_text  TEXT NOT NULL,
    embedding   vector(768) NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_chunks_embedding ON chunks
    USING hnsw (embedding vector_cosine_ops);

CREATE INDEX idx_articles_url ON articles (url);
CREATE INDEX idx_chunks_article_id ON chunks (article_id);
```

**Two-table design**: `articles` stores the full text and URL (deduplicated). `chunks` stores the chunked text and embedding vectors. This separation allows re-embedding with a different model (delete chunks, keep articles) and efficient URL-based lookups without scanning the vector index.

**HNSW index**: Hierarchical Navigable Small World graph for approximate nearest neighbor search. Faster than IVFFlat for the expected data scale and does not require training on the dataset.

**768 dimensions**: `all-mpnet-base-v2` outputs 768-dimensional vectors. This is the best quality-to-size ratio in the sentence-transformers ecosystem. 384d (MiniLM) to 768d improves quality a lot; 768 to 1024 has diminishing returns for double the storage cost.

---

## 6. Redis Data Structures

| Key Pattern | Type | Purpose | TTL |
|-------------|------|---------|-----|
| `vortex:frontier:seen` | Bloom filter | URL deduplication via `BF.ADD` | None |
| `vortex:frontier:robots:<domain>` | String | Cached robots.txt content or `ROBOTS_DENIED` marker | 24h (2h if denied) |
| `vortex:frontier:limit:<domain>:<unix_sec>` | Counter | Per-domain, per-second request count | rate_limit_window |
| `vortex:frontier:cooldown` | Sorted set | Rate-limited tasks awaiting retry. Score = retry timestamp | None (poller drains) |
| `vortex:control:crawler` | String | Runtime crawler pause/resume/auto control | None |
| `vortex:control:embedder` | String | Runtime embedder pause/resume/auto control | None |

The Bloom filter requires Redis Stack (not plain Redis) for the `BF.ADD` command.

---

## 7. Backpressure & Flow Control

The most interesting part of the system, and the one that took the most iteration.

### The problem

Crawlers are cheap (50 goroutines sharing one Go process, almost all of their time is spent on network I/O). Embedders are expensive (GPU-bound transformer inference, one model instance per container). If crawlers run unthrottled, the processing queue fills faster than the embedder can drain it. RabbitMQ will happily absorb millions of messages, but at some point memory pressure kicks in, flow control trips, and then *everything* slows down — including publish calls from the crawler, which blocks the consume loop, which blocks ack, which blocks QoS credit, which stalls the whole fleet in a way that's hard to observe.

Rather than let RabbitMQ's flow control be the safety net, I wanted an explicit, observable backpressure loop: **pause the crawlers before the downstream queue becomes a problem, resume them once it drains.**

### The design

A single `Manager` goroutine runs in the worker process. Every 2 seconds it:

1. Reads `vortex:control:crawler` from Redis.
2. If the key is set to `pause` or `resume`, that wins — manual override.
3. Otherwise it calls `QueueDeclarePassive` on the processing queue to read its depth.
4. Compares depth against two thresholds: `pause_at = 5000`, `resume_at = 2500`.
5. Calls its own `pause()` or `resume()` helper accordingly, or leaves state alone if depth is between the thresholds.

Workers and the cooldown poller both hold a `Pauser` interface with a single method: `WaitIfPaused(ctx)`. Workers call it right after receiving a message from the frontier queue, before doing any real work. The poller calls it before popping expired tasks out of Redis — popped tasks only exist in memory, so pausing *after* a pop would risk data loss.

### The pause gate

Implementation is an `atomic.Pointer[chan struct{}]` on the Manager:

- **Running state**: the pointer is `nil`. `WaitIfPaused` loads the pointer, sees `nil`, returns immediately. That's the hot path, and it's a single atomic load — no mutex, no syscall, nothing that touches the scheduler.
- **Paused state**: the pointer is non-`nil` and points to an open channel. `WaitIfPaused` loads the pointer and does a `select` on the channel *and* the caller's context. Callers block until either the channel closes (resume) or the context cancels (shutdown).
- **Pausing**: `pause()` atomically CASes a fresh channel into the pointer. If the pointer was already non-`nil`, it's a no-op.
- **Resuming**: `resume()` atomically swaps the pointer out, then closes the old channel. Closing the channel wakes every blocked `WaitIfPaused` at once.

Why this over a `sync.Cond` or a mutex-protected bool:

- Mutex-protected bool forces every worker to take a lock on every message, even when not paused. For a fleet of 50 workers pulling at full throughput, that's wasted CPU and scheduler pressure.
- `sync.Cond` doesn't integrate with `context.Context` cancellation. You'd have to wrap it in a goroutine that signals a channel, at which point you're reinventing this design but worse.
- The atomic pointer approach is lock-free on the hot path, zero-overhead when running, and cleanly context-aware when paused. The cost is that it requires the single-writer invariant: only `Manager.Run` calls `pause()` and `resume()`. That's easy to enforce and easy to verify.

### Hysteresis

Two thresholds instead of one. The gap between `pause_at = 5000` and `resume_at = 2500` prevents flapping: if you paused at 5K and resumed at 5K, any small drain-refill cycle would flip state constantly, spam metrics, and burn CPU on coordination. 2500/5000 gives the system a 2.5K-message "dead zone" where it stays in whatever state it was in. The absolute numbers are tunable; the ratio matters more than the values.

### Fail-open

If the Redis read in the Manager tick fails, the Manager logs and continues. If `QueueDeclarePassive` fails, same. A broken control plane should never halt the data plane — the crawler keeps crawling, the embedder keeps embedding, and the operator gets a metric alert instead of a silent outage. The one exception is Redis returning `redis.Nil` for the control key (meaning the key doesn't exist), which is treated as "no manual override, fall through to auto mode." This was a bug initially — I was treating `redis.Nil` as a fatal error and skipping the tick entirely, which meant auto mode never ran unless the operator set the key first.

### Embedder side

The embedder has its own pause loop, mirroring the crawler but simpler: Pika's `BlockingConnection` runs one callback per message. At the top of the handler, the consumer polls `vortex:control:embedder` and, if paused, calls `channel.connection.sleep(1.0)` in a loop until it isn't. The `connection.sleep` is important — it keeps the AMQP heartbeat alive. A naive `time.sleep(1.0)` would work for short pauses but would drop the connection after ~60s.

There is no auto-throttle on the embedder side. The embedder is manual-only, because there's no clean "downstream is overwhelmed" signal to drive it (Postgres happily absorbs inserts up to its buffer limits, and monitoring that is its own problem). Manual pause is enough for the scale of this project.

---

## 8. Observability

Every service exports Prometheus metrics and structured JSON logs. Dashboards are provisioned via the Grafana sidecar so `docker compose up` brings everything online without UI clicks.

### Metrics taxonomy

- **Counters** for events (`pages_crawled_total`, `tasks_processed_total{status}`, `manager_throttle_total{action,reason}`, etc.). Use `rate(...[1m])` or `increase(...[5m])` on these.
- **Histograms** for latencies (`page_process_seconds`, `robots_fetch_duration_seconds`). Query with `histogram_quantile(0.95, rate(..._bucket[1m]))`.
- **Gauges** for instantaneous state (`manager_paused`, `frontier_queue_depth`, `processing_queue_depth`). These are the ones you overlay on the same chart to tell the backpressure story.

Metrics are grouped by subsystem (`vortex_worker_*`, `vortex_robots_*`, `vortex_embedder_*`, `vortex_search_*`) so each dashboard row filters cleanly.

### Trace IDs

Every seed URL gets a `trace_id` (UUID v4). It propagates through:

- The `CrawlTask` JSON as it moves through the frontier queue
- Worker logs at every pipeline step (`slog.Info("...", "task_id", task.TraceID)`)
- The `CrawlResult` JSON into the processing queue
- The embedder's log lines (via the same field)
- The `articles.trace_id` column in Postgres

This lets you grep a single UUID from seed to storage and see the full path. It's not full distributed tracing (no parent/child spans, no OTel integration), but for a single-operator debugging experience it's enough. A proper OTel/Tempo might be implemented in the future.

### Graceful shutdown

Every service has a single root `context.Context` tree rooted at a `signal.NotifyContext(..., SIGINT, SIGTERM)`. Every goroutine either derives its context from that tree or honors the parent via `select { case <-ctx.Done(): return }`. On shutdown:

- The root context cancels.
- Workers finish their current task (bounded by `taskTimeout`), ack the message, and exit.
- The Manager exits its tick loop.
- The Poller exits its ticker.
- RabbitMQ channels and the AMQP connection close cleanly.
- Prometheus scrapes get a final data point before the metrics server shuts down.

No goroutine leaks, no half-processed tasks, no requeue storms on restart.

---

## 9. Load Test

One real run, not a benchmark suite. Goal was to verify the pipeline survives end-to-end under realistic crawl conditions and to observe the backpressure loop fire.

### Setup

- **Hardware**: i7-14700KF, 32 GB DDR5, NVMe M.2, RTX 5070 (12 GB VRAM) for the embedder.
- **Network**: ~60 Mbps symmetric. Downstream bottleneck, confirmed by watching interface throughput flatline during peak crawl.
- **Stack**: full Docker Compose, all services on one host.
- **Runtime**: ~45 minutes total, three phases:
    1. **0–11 min**: 10 workers (baseline).
    2. **11–17 min**: 30 workers (scale up).
    3. **17–45 min**: 50 workers (full fleet), including a deliberate backpressure test.

### Headline numbers

Worker counters are process-local and reset on restart, which happened at the two scale-up transitions (10 → 30 → 50 workers). All counter totals below are from `increase(vortex_..._total[24h])`, which handles resets by summing deltas across counter lifecycles.

| Metric | Value |
|---|---|
| Pages crawled | ~14,993 |
| URLs discovered | ~1,016,006 |
| Articles in Postgres | 14,986 |
| Chunks in Postgres | 63,354 |
| Avg chunks per article | ~4.2 |
| Tasks processed (success) | ~87,381 |
| Tasks processed (transient_error) | ~1,154 |
| Tasks dropped (DLQ) | 0 |
| Cooldown republishes | ~63,317 |
| Peak frontier depth | 443,296 |
| Peak processing depth | 5,028 |
| Manager auto-pauses | 1 |
| Manager auto-resumes | 1 |

Pages crawled and `articles` row count agree at ~15K, which is the right sanity check: every successfully fetched page should produce exactly one `articles` row via the embedder.

### Robots.txt numbers

| Metric | Value |
|---|---|
| Actual fetches over the wire | ~5,162 |
| CanCrawl calls (total) | ~88,203 |
| Inflight dedup saves | ~20,175 |
| Cache hits | ~63,015 |
| Cache misses | ~5,389 |
| Cache hit rate | ~92% |
| CanCrawl: allowed | ~79,001 |
| CanCrawl: denied | ~8,354 |
| CanCrawl: parse_error | ~848 |

The inflight dedup line is the one I care about most. Without the state machine, ~20K extra HTTP fetches would have gone out for robots.txt files that were already being fetched by another goroutine. At a typical 400–500ms per fetch that's roughly 2.5 hours of wasted wall-clock work on a single crawler process, plus the rudeness of hammering the same origin in parallel.

### Latencies

Measured via histogram quantiles over 1-minute windows during steady state.

| Metric | p50 | p95 |
|---|---|---|
| Page process | 50–70 ms | ~1.5 s* |
| Robots fetch | 400–500 ms | 1.9–2.3 s |
| Search (end to end) | 64 ms | 212 ms |

*The page process p95 was periodically pinned at the 10-second ceiling. Root cause: a handful of sites advertise large `Crawl-delay` values in their robots.txt (15 seconds or more). The current implementation honors the delay with `time.Sleep(crawlDelay)` inside the task budget (The current buckets do not properly support `taskTimeout=60s` and should be updated). Some of the delays overflow `taskTimeout=60s`, cancelling the taks. The task then hits the transient-error path and gets requeued. This is a known behavior, documented in §3, and accepted for this scope (see §10 for the follow-up fix if it ever matters).

Search latency was measured over ~15–20 ad-hoc queries, not a concurrent load test. The p95 is optimistic — under concurrent load the Python embedder and pgvector index would start queueing. Not tested.

### The backpressure test

The most interesting few minutes of the run.

| Time (min from start) | Event |
|---|---|
| ~26 | Manually paused the embedder via `vortex:control:embedder = pause`. |
| ~26 → ~30 | Processing queue filled. |
| ~30 | PQ depth crossed 5,000. Manager fired `pause (auto)`. `manager_paused` gauge went from 0 to 1. Crawlers stopped consuming from the frontier. Cooldown poller stopped republishing. |
| ~32 | Manually resumed the embedder. PQ started draining. |
| ~36 | PQ crossed 2,500. Manager fired `resume (auto)`. `manager_paused` gauge went from 1 to 0. Crawlers resumed. |
| ~42 | PQ hit 0. System back to steady state. |
| ~45 | Stopped the test. |

One full cycle of the state machine, cleanly observed end-to-end. `manager_throttle_total{action="pause",reason="auto"} = 1` and `manager_throttle_total{action="resume",reason="auto"} = 1` at the end of the run — exactly what you'd expect.

The bug this test caught: the `Throttle Events` Grafana panel was using `increase(...[5m])` on a counter that only fires a handful of times per run. The increase rolls off as the 5-minute window advances, so the panel read ~0 shortly after each event. Fix: switch that panel to display the raw counter, not the windowed increase. Infrequent events need a different query shape than high-rate events.

### What I didn't test

- **Embedder horizontal scaling**. Single embedder instance throughout. The architecture supports `docker compose up --scale embedder=N` for competing consumers, but I didn't run that configuration.
- **Multi-host**. Single-machine Docker Compose. No real network partitions, no cross-host latency, no container orchestrator.
- **CPU saturation**. Network was the bottleneck at ~60 Mbps, so Go CPU usage was low and the i7 was never stressed. A faster network would change the scaling story.
- **Search under concurrent load**. ~15–20 ad-hoc queries only. Under sustained QPS, the embed call and pgvector HNSW query would be the two interesting latencies to watch.
- **Long-run stability**. 45 minutes. No overnight run, no week-long soak test, no memory leak surfacing.
- **Adversarial input**. No deliberately malformed robots.txt, no slowloris origins, no sites trying to trap the crawler with infinite redirects.

These are listed not as apologies but as honest scope boundaries. A real production system would test all of them; this is a one-person learning project.

---

## 10. Known Issues & Future Work

In rough order of "how much it would bother me to ship this."

### Crawl-delay inside taskTimeout

When a site advertises a large `Crawl-delay`, the worker calls `time.Sleep(crawlDelay)` inside the task's context, consuming the timeout budget. Long delays guarantee a `context.DeadlineExceeded` and a requeue. This is a known behavior, accepted for simplicity, documented in §3.

The proper fix is to treat "crawl delay > some threshold" the same as "rate limited": push the task into the cooldown delay queue with a score of `now + crawlDelay`, and let the poller republish it. Zero blocking in-flight, clean separation between "polite waiting" and "task processing."

### Frontier queue unbounded growth

The frontier hit 443K during the run and was still climbing when I stopped. BFS with 50 workers and real web pages (lots of outbound links) naturally grows faster than it drains. The bloom filter prevents true duplicates, but "net new URLs discovered per page fetched" is still well above 1 for a long time.

For this project's scope it's fine — RabbitMQ handles 443K messages without noticing, and the queue is persistent. For a real crawler you'd want either:

- A depth-based priority queue that drains shallow URLs first.
- A discovery-rate cap that pauses URL extraction when the frontier grows too fast.
- Sharded frontier queues by domain or hash, with per-shard rate limiting.

None of these are implemented.

### No distributed tracing

Trace IDs propagate, but there's no OTel/Tempo/Jaeger integration. You can grep logs for a UUID and reconstruct the path manually, which works for single-operator debugging but doesn't scale. A proper OTel SDK integration with spans for fetch, parse, embed, and insert would be a clean win.

### Single embedder instance

Not tested at all with multiple replicas. The architecture supports it (RabbitMQ competing consumers), but I haven't verified that two embedders writing to the same Postgres table don't trip the `url` unique constraint or produce duplicate chunks for the same article. Worth testing before claiming horizontal scaling actually works.

### Manager is a single-instance control plane

One Manager goroutine in one worker process. If the worker process dies, the auto-throttle dies with it. Manual override via the Redis key still works (embedder-side pause is independent), but the auto loop is gone until the worker restarts.

For a single-node deployment this is fine. For a multi-worker-host deployment you'd want either a leader-elected Manager (Raft, etcd) or a stateless Manager that runs as a sidecar per host and coordinates via Redis.

### No integration tests for the crawl pipeline

`go test ./...` passes, but coverage is uneven. Pure functions (parser, sanitizer, ratelimit Lua wrapper, robots state machine) are well-tested. The full crawl pipeline (`Worker.processTask`) has a few unit tests but no integration test against a real RabbitMQ + Redis stack. A `testcontainers` suite would be the right fix.

### Search API has no concurrent-load test

Covered in §9 already. Listed here so the future-work list is complete.

### Minor

- Some hardcoded values still leaking through in a couple of spots (see §3.1.2).
- Dockerfile is a single shared multi-stage build with a `SERVICE` build arg. Works, but `docker compose build` rebuilds everything on any Go file change. Per-service Dockerfiles would cache better.
- No CI. `go test`, `go vet`, and Python lint should run on every push. Not set up.
- No Dependabot or equivalent. Manual dep bumps.

---

## 11. Rejected Alternatives

Things I thought about and decided against. Including the reasoning because "why you *didn't* do X" is often more informative than "why you did Y."

### Kafka instead of RabbitMQ

Kafka gives you ordered partitions, replay, and better throughput ceilings. None of which this project needs. RabbitMQ gives you per-message ack, dead-letter queues, and a management UI that a human can actually use. The operational overhead of running Kafka (brokers, ZooKeeper or KRaft, partition management, consumer group rebalancing) is real, and the payoff doesn't exist at this scale.

### RabbitMQ delayed exchange plugin instead of a Redis sorted set for cooldown

The delayed-message plugin would let me publish a task with an `x-delay` header and have RabbitMQ hold it until the delay expires. Cleaner than running a separate poller.

Rejected because:
1. It's a plugin, not core RabbitMQ. Another thing to install, configure, and worry about.
2. Redis is already in the critical path for bloom, rate limits, and robots cache. Adding one more data structure (a sorted set) has zero operational cost.
3. The poller is simple (5-second tick, `ZRANGEBYSCORE`, republish) and demonstrates the "delay queue" pattern explicitly in code, which is more educational than hiding it behind a plugin.

### Mutex + bool for the pause gate

Already covered in §7. Forces a lock on every hot-path message. Rejected on performance grounds.

### `sync.Cond` for the pause gate

Doesn't compose with `context.Context`. You'd need a shim goroutine to translate `Wait()` into a channel, at which point the atomic-pointer design is strictly simpler.

### Writing the crawler in Python

I just like Go much more. Plus it's far more "hands-dirty" than the Python abstraction. (and much better suited for the given tasks :D)

### Writing the embedder in Go

The other direction. Rejected because the Go ML ecosystem is thin. `sentence-transformers` gives you a state-of-the-art model with three lines of Python. The equivalent in Go would be wrapping ONNX Runtime or calling out to a C++ library. Not worth the pain.

### Storing embeddings in a dedicated vector DB (Qdrant, Milvus, Weaviate)

Considered. Rejected because:
1. Another service to run.
2. `pgvector` is already good enough for the scales this project operates at. HNSW index, cosine similarity, returns in ~tens of ms.
3. I already needed Postgres for the `articles` table. Keeping everything in one database simplifies backups, transactions, and operator mental model.

The trade-off is real: a dedicated vector DB would scale better past ~10M vectors and supports more exotic index types. Not a concern at this scope.

### Mounting the seed file instead of embedding it

Covered in §3. Embedding the seed list in the binary means "deploy the binary = deploy the seed list," which is harder to mess up operationally. For a production crawler with dynamic seed sources, you'd want an external config; for this project, baked-in is fine.

### A dashboard per service

Instead of one Vortex dashboard, one dashboard each for crawler, embedder, search. Rejected because the most interesting chart crosses service boundaries (processing queue depth + manager paused + page process latency). Splitting dashboards would have fragmented the backpressure story across tabs.

---

## 12. Appendix: Useful Commands

### Runtime control

```bash
# Pause the crawler pool
./scripts/vortex-ctl.sh crawler pause

# Resume the crawler pool (auto-throttle takes over)
./scripts/vortex-ctl.sh crawler resume

# Return to fully automatic mode (clears the control key)
./scripts/vortex-ctl.sh crawler auto

# Same three commands for the embedder
./scripts/vortex-ctl.sh embedder pause
./scripts/vortex-ctl.sh embedder resume
./scripts/vortex-ctl.sh embedder auto

# Check current control state
./scripts/vortex-ctl.sh crawler status
./scripts/vortex-ctl.sh embedder status
```

### Inspecting the system

```bash
# Count articles and chunks in Postgres
docker exec vortex-vector_store-1 psql -U vortex -d vortex -c \
  "SELECT count(*) FROM articles; SELECT count(*) FROM chunks;"

# Query Prometheus for a cumulative counter at a point in time
curl -sG "http://localhost:9090/api/v1/query" \
  --data-urlencode 'query=max_over_time(vortex_worker_pages_crawled_total[24h])'

# Check RabbitMQ queue depths
curl -u guest:guest http://localhost:15672/api/queues | jq '.[] | {name, messages}'
```

---