# Vortex

A distributed web crawler and semantic search engine. Go handles crawling, orchestration, and HTTP serving; Python handles transformer-based text embeddings. Results are stored as dense vectors in Postgres (pgvector) and served through a cosine-similarity search API.

Built to explore production concerns in distributed systems: backpressure, rate limiting, bounded concurrency, graceful degradation, and observability.

---

## Overview

Vortex crawls the web starting from a set of seed URLs, extracts text content, chunks and embeds it into a vector space, and serves semantic similarity queries over the resulting corpus. It is designed to run as a small cluster of cooperating services under Docker Compose, with horizontal scaling inside the worker pool.

- **Crawl**: 50 concurrent workers fan out BFS-style from seed URLs, respecting `robots.txt`, per-domain rate limits, and a bounded crawl depth.
- **Process**: crawled pages are chunked, embedded via a `sentence-transformers` model, and written to Postgres with a 768-dimensional vector.
- **Search**: an HTTP API embeds the query and runs HNSW-indexed cosine similarity search over the `chunks` table.
- **Observe**: every service exports Prometheus metrics; Grafana dashboards visualize throughput, latency, queue depth, and error rates.

---

## Architecture

```
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
  • FrontierQueue and ProcessingQueue are not directly connected at the data layer.
    The only coupling between them is the control plane: a Manager goroutine polls
    ProcessingQueue depth and a Redis control key, then toggles a gate that
    pauses/resumes Workers and the Poller when the downstream embedder falls behind.
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

## Key Features

**Distributed crawling**
- Bounded BFS with configurable max depth
- 50 workers consuming the same queue with RabbitMQ `QoS(1)` prefetch to bound in-flight work per worker
- URL deduplication via Redis `BF.ADD` (RedisBloom) — constant-time, space-efficient
- Per-domain token bucket rate limiting implemented as an atomic Lua script in Redis
- Cooldown delay queue (Redis sorted set) for rate-limited tasks, republished by a poller

**Polite crawling**
- Full `robots.txt` compliance via [`temoto/robotstxt`](https://github.com/temoto/robotstxt)
- Concurrent `robots.txt` fetches deduplicated via an in-process state machine so 50 workers hitting the same unseen host result in one actual HTTP fetch
- Cached allow/deny decisions with separate TTLs for the success and denied paths
- Fetcher enforces SSRF protection, content-type and size checks, and configurable user-agent

**Backpressure-aware auto-throttle**
- A `Manager` goroutine polls the processing-queue depth and automatically pauses the crawler pool when the downstream embedder falls behind
- Hysteresis thresholds (pause at 5000, resume at 2500) prevent flapping
- Pause coordination uses a lock-free `atomic.Pointer[chan struct{}]` gate; workers block via a `select` that also honors context cancellation
- Manual override via Redis control key takes priority over the auto-throttle

**Semantic search**
- Sentence-transformer embeddings (768-dim) via `sentence-transformers`
- HNSW index on `pgvector` for sub-linear cosine similarity queries
- Go search API embeds the query through the Python service and returns ranked chunks with their source articles
- Per-IP rate limiting in the search API

**Observability**
- Structured JSON logging (`slog`) with propagated trace IDs across the pipeline
- Prometheus metrics at every stage: pages crawled, URLs discovered, task outcomes, cooldown republishes, fetch durations, search latency, rate-limit decisions
- Grafana dashboards provisioned automatically via Docker Compose
- Graceful shutdown across every service via a shared `context.Context` tree

---

## Tech Stack

- **Go 1.25** — crawler, seeder, search API
- **Python 3.11+** — embedder (`sentence-transformers`, FastAPI, `pika`)
- **RabbitMQ 3** — message broker with dead-letter queues
- **Redis Stack** — bloom filter, rate limits, cache, delay queue, control plane
- **PostgreSQL 16 + pgvector** — vector store with HNSW index
- **Prometheus + Grafana** — metrics and dashboards
- **Docker Compose** — local orchestration

---

## Quick Start

### Prerequisites

- Docker and Docker Compose
- (Optional) NVIDIA GPU with container runtime — the embedder will use CUDA if available, otherwise falls back to CPU

### Run

```bash
# 1. Copy environment template
cp .env.example .env

# 2. Start the full stack
docker compose up -d

# 3. Publish seed URLs into the frontier
docker compose run --rm seeder
```

The system will begin crawling, embedding, and indexing automatically.

### Access points

| Service                  | URL                          | Credentials        |
|--------------------------|------------------------------|--------------------|
| Search UI                | http://localhost:8080        | —                  |
| Search API               | http://localhost:8080/search?q=your+query | —     |
| Grafana                  | http://localhost:3000        | `admin` / `admin`  |
| Prometheus               | http://localhost:9090        | —                  |
| RabbitMQ Management      | http://localhost:15672       | `guest` / `guest`  |

---

## Configuration

All services are configured via environment variables. See [.env.example](.env.example) for the complete list.

Key settings:

| Variable                         | Default | Purpose                                     |
|----------------------------------|---------|---------------------------------------------|
| `WORKER_COUNT`                   | 50      | Number of concurrent crawl workers          |
| `WORKER_TASK_TIMEOUT`            | 60s     | Per-task processing budget                  |
| `CRAWLER_MAX_DEPTH`              | 3       | Max BFS depth from seed                     |
| `CRAWLER_RATE_LIMIT`             | 1       | Tokens/sec per domain                       |
| `CRAWLER_RATE_BURST`             | 5       | Token bucket capacity per domain            |
| `MANAGER_PROCESSING_PAUSE_AT`    | 5000    | Processing queue depth that triggers pause  |
| `MANAGER_PROCESSING_RESUME_AT`   | 2500    | Processing queue depth that triggers resume |
| `ROBOTS_CACHE_TTL`               | 24h     | `robots.txt` cache TTL                      |
| `FETCHER_TIMEOUT`                | 30s     | HTTP fetch timeout                          |

### Runtime crawl control

The crawler pool can be paused and resumed at runtime without restarting any service, via a Redis control key:

```bash
# Pause all crawlers (workers + cooldown poller)
redis-cli SET vortex:control:crawler pause

# Resume
redis-cli SET vortex:control:crawler resume

# Return to auto-throttle mode (based on processing queue depth)
redis-cli DEL vortex:control:crawler
```

Manual override takes priority over the backpressure-driven auto-throttle.

---

## Project Layout

```
vortex/
├── cmd/
│   ├── seeder/       # Seeds the frontier queue with initial URLs
│   ├── worker/       # Crawler worker service (50x workers + manager + poller)
│   └── search/       # Search HTTP API + minimal UI
├── internal/
│   ├── cache/        # Bloom filter and Redis cache helpers
│   ├── config/       # Env-based configuration loading
│   ├── cooldown/     # Redis sorted-set delay queue
│   ├── fetcher/      # HTTP fetcher with SSRF protection and size/content-type checks
│   ├── infra/        # RabbitMQ/Redis setup, logger, metrics server
│   ├── keys/         # Redis key and queue name constants
│   ├── models/       # CrawlTask and CrawlResult types
│   ├── parser/       # HTML extraction: URLs, text, sanitization
│   ├── ratelimit/    # Per-domain token bucket (Lua script)
│   ├── robots/       # robots.txt fetcher with inflight dedup state machine
│   └── worker/       # Worker, Poller, Manager, and the crawl pipeline
├── worker-py/
│   └── worker/       # Python embedder: consumer, chunker, embedder, db, FastAPI
├── docs/
│   └── DESIGN.md     # Full design document
├── grafana/          # Grafana dashboards and datasource provisioning
├── docker-compose.yml
├── Dockerfile        # Shared build for all Go services (SERVICE build arg)
├── init.sql          # Postgres schema + pgvector index
└── prometheus.yml    # Prometheus scrape configuration
```

---

## Development

### Build Go services locally

```bash
go build ./cmd/...
```

### Run tests

```bash
go test ./...
```

### Rebuild a single service in the stack

```bash
docker compose up -d --build crawler
```

---

## Design Document

For architectural details, design trade-offs, and performance analysis, see the full design document:

[docs/DESIGN.md](docs/DESIGN.md)

<!-- TODO: link the polished/published version here once available -->

---

## Status

Under active development. Core crawl, embed, and search paths are functional; ongoing work focuses on load testing, telemetry coverage, and operational polish.
