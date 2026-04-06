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


def main():
    model_name = os.environ.get("EMBEDDING_MODEL", "all-mpnet-base-v2")
    device = os.environ.get("EMBEDDING_DEVICE", "cuda")

    model = embedder.Embedder(model_name=model_name, device=device)
    db = dbmod.Database(os.environ.get("POSTGRES_URL", "postgresql://vortex:vortex@localhost:5432/vortex"))

    c = consumer.Consumer(model, db)
    thread = threading.Thread(target=c.start, daemon=True)
    thread.start()

    app = api.create_app(model)
    uvicorn.run(app, host="0.0.0.0", port=8000)

    logger.info("Uvicorn stopped, shutting down consumer...")
    c.stop()
    thread.join(timeout=10)
    db.close()
    logger.info("Embedder shutdown complete")

if __name__ == "__main__":
    main()