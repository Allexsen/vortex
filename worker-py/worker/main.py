import os
import logging
import threading
import uvicorn

from . import embedder
from . import db as dbmod
from . import consumer
from . import api

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)


def main():
    model_name = os.environ.get("EMBEDDING_MODEL", "all-mpnet-base-v2")
    device = os.environ.get("EMBEDDING_DEVICE", "cuda")
    
    model = embedder.Embedder(model_name=model_name, device=device)
    db = dbmod.Database(os.environ.get("POSTGRES_URL", "postgresql://vortex:vortex@localhost:5432/vortex"))
    threading.Thread(target=consumer.start, args=(model, db)).start()

    app = api.create_app(model)
    uvicorn.run(app, host="0.0.0.0", port=8000)

if __name__ == "__main__":
    main()