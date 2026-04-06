from fastapi import FastAPI
from pydantic import BaseModel

from prometheus_client import generate_latest, CONTENT_TYPE_LATEST
from fastapi.responses import Response


class EmbedRequest(BaseModel):
    text: str

def create_app(model):
    app = FastAPI()

    @app.get("/health")
    def health():
        return {"status": "ok"}

    @app.get("/metrics")
    def metrics():
        return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)

    @app.post("/embed")
    def embed(request: EmbedRequest):
        result = model.encode([request.text])
        return {"embedding": result[0].tolist()}

    return app