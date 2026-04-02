-- Enable pgvector extension (teaches Postgres the "vector" type)
CREATE EXTENSION IF NOT EXISTS vector;

-- Full crawled pages, one row per URL
CREATE TABLE articles (
    id          BIGSERIAL PRIMARY KEY,
    trace_id    TEXT NOT NULL,
    url         TEXT NOT NULL UNIQUE,
    content     TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Chunked text + embedding vectors, many per article
CREATE TABLE chunks (
    id          BIGSERIAL PRIMARY KEY,
    article_id  BIGINT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    chunk_index INT NOT NULL,
    chunk_text  TEXT NOT NULL,
    embedding   vector(768) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Semantic similarity search (cosine distance, IVFFlat index)
CREATE INDEX idx_chunks_embedding ON chunks
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- Fast URL lookups for dedup
CREATE INDEX idx_articles_url ON articles (url);

-- Fast chunk lookups by article
CREATE INDEX idx_chunks_article_id ON chunks (article_id);
