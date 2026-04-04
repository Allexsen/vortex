from fastapi import FastAPI
from pydantic import BaseModel


class EmbedRequest(BaseModel):
    text: str

def create_app(model):
    app = FastAPI()

    @app.get("/health")
    def health():
        return {"status": "ok"}

    @app.post("/embed")
    def embed(request: EmbedRequest):
        result = model.encode([request.text])
        return {"embedding": result[0].tolist()}

    return app