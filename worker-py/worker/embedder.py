from sentence_transformers import SentenceTransformer
from prometheus_client import Counter, Histogram

EMBED_LATENCY = Histogram(
    "vortex_embedder_latency_seconds",
    "Time to encode chunks",
    buckets=[0.1, 0.25, 0.5, 1, 2.5, 5, 10]
)

EMBED_CHUNKS_ENCODED_TOTAL = Counter(
    "vortex_embedder_chunks_encoded_total",
    "Total number of chunks encoded"
)


class Embedder:
    def __init__(self, model_name, device):
        self.model = SentenceTransformer(model_name, device=device)

    def encode(self, chunks):
        EMBED_CHUNKS_ENCODED_TOTAL.inc(len(chunks))
        with EMBED_LATENCY.time():
            return self.model.encode(chunks)