# Vortex

A distributed web crawler and semantic search engine. Go handles the asynchronous crawling, bounding, and orchestration; Python handles the transformer-based text embeddings. Results are stored as dense vectors in Postgres (`pgvector`) and served through a cosine-similarity search API.

👉 **[Read the System Design & Architecture Document](docs/DESIGN.md)**  
*Details the backpressure hysteresis loop, load testing telemetry, and queue scaling rationale.*

---

## Project Status

**Stable & Load-Tested.** The system architecture has been verified under sustained network load. The core hysteresis control loop successfully regulates 50 concurrent Go workers against a Python ML inference "bottleneck" without memory exhaustion or rate-limit violations.

## Tech Stack

* **Ingest & Orchestration:** Go, RabbitMQ, Redis (Lua token-bucket rate limiting)
* **ML & Embedding:** Python, FastAPI, `sentence-transformers` (`all-mpnet-base-v2`)
* **Storage & Search:** PostgreSQL (`pgvector`), HNSW Indexing
* **Telemetry:** Prometheus, Grafana

## Quick Setup

```bash
# 1. Clone and enter
git clone https://github.com/Allexsen/vortex.git
cd vortex

# 2. Boot the infra
docker compose up -d postgres rabbitmq redis prometheus grafana

# 3. Boot the application cluster
docker compose up -d crawler embedder search

# 4. Or just boot everything up right away :D
docker compose up -d
```

## Access Points

* **Search API:** `http://localhost:8080/search?q=your+query`
* **Grafana Dashboards:** `http://localhost:3000` (admin/admin)
* **RabbitMQ UI:** `http://localhost:15672` (guest/guest)

## Configuration

Core system limits are managed via environment variables (see [`docker-compose.yml`](./docker-compose.yml):
* `CRAWLER_WORKERS`: Bounded concurrency limit (Default: 50)
* `RATE_LIMIT_REQ_SEC`: Global per-domain politeness limit (Default: 1)
* `QUEUE_HIGH_WATERMARK`: Auto-pause threshold for processing queue (Default: 5000)

## Scripts & Control

The crawlers & embedder can be manually paused and resumed at runtime without killing containers:

```bash
# Control flow
./scripts/vortex-ctl.sh <crawler|embedder> <pause|resume|auto>

# Check hysteresis state
./scripts/vortex-ctl.sh <crawler|embedder> status
```

## Development

```bash
# Build Go binaries
go build ./cmd/...

# Run test suite
go test ./...
```
