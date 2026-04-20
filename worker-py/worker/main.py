import os
import logging
import threading
import uvicorn

from pythonjsonlogger import jsonlogger

from . import embedder
from . import db as dbmod
from . import consumer
from . import api

handler = logging.StreamHandler()
handler.setFormatter(jsonlogger.JsonFormatter("%(asctime)s %(levelname)s %(name)s %(message)s"))

logging.basicConfig(
    level=logging.INFO,
    handlers=[handler],
)
logger = logging.getLogger(__name__)


def _env_str(key, default):
    return os.environ.get(key, default)

def _env_int(key, default):
    val = os.environ.get(key)
    if val is None:
        return default
    return int(val)

def _env_seconds(key, default):
    """Parse a duration value that may have a trailing 's' suffix (e.g. '5s' or '5')."""
    val = os.environ.get(key)
    if val is None:
        return default
    return int(val.rstrip("s"))

def _env_float(key, default):
    val = os.environ.get(key)
    if val is None:
        return default
    return float(val)


def main():
    model_name = _env_str("EMBEDDING_MODEL", "all-mpnet-base-v2")
    device = _env_str("EMBEDDING_DEVICE", "cuda")

    rabbitmq_url = _env_str("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
    rabbitmq_max_retries = _env_int("RABBITMQ_MAX_RETRIES", 3)
    rabbitmq_retry_delay = _env_seconds("RABBITMQ_RETRY_DELAY", 5)
    rabbitmq_prefetch = _env_int("RABBITMQ_PREFETCH_COUNT", 1)

    redis_addr = _env_str("REDIS_ADDR", "localhost:6379")
    redis_db = _env_int("REDIS_DB", 0)
    redis_password = _env_str("REDIS_PASSWORD", "")
    redis_socket_timeout = _env_float("REDIS_SOCKET_TIMEOUT", 2.0)

    postgres_url = _env_str("POSTGRES_URL", "postgresql://vortex:vortex@localhost:5432/vortex")
    postgres_max_retries = _env_int("POSTGRES_MAX_RETRIES", 3)
    postgres_retry_delay = _env_seconds("POSTGRES_RETRY_DELAY", 5)

    chunk_size = _env_int("CHUNKER_CHUNK_SIZE", 250)
    chunk_overlap = _env_int("CHUNKER_OVERLAP", 50)

    embedder_host = _env_str("EMBEDDER_HOST", "0.0.0.0")
    embedder_port = _env_int("EMBEDDER_PORT", 8000)

    model = embedder.Embedder(model_name=model_name, device=device)
    db = dbmod.Database(postgres_url, postgres_max_retries, postgres_retry_delay)

    c = consumer.Consumer(
        model, db,
        rabbitmq_url=rabbitmq_url,
        rabbitmq_max_retries=rabbitmq_max_retries,
        rabbitmq_retry_delay=rabbitmq_retry_delay,
        rabbitmq_prefetch=rabbitmq_prefetch,
        redis_addr=redis_addr,
        redis_db=redis_db,
        redis_password=redis_password,
        redis_socket_timeout=redis_socket_timeout,
        chunk_size=chunk_size,
        chunk_overlap=chunk_overlap,
    )
    thread = threading.Thread(target=c.start, daemon=True)
    thread.start()

    app = api.create_app(model)
    uvicorn.run(app, host=embedder_host, port=embedder_port)

    logger.info("Uvicorn stopped, shutting down consumer...")
    c.stop()
    thread.join(timeout=10)
    db.close()
    logger.info("Embedder shutdown complete")

if __name__ == "__main__":
    main()