from fastapi import FastAPI
from pydantic import BaseModel


class EmbedRequest(BaseModel):
    text: str

def create_app(embedder):
    app = FastAPI()

    @app.post("/embed")
    def embed(request: EmbedRequest):
        result = embedder.encode([request.text])
        return {"embedding": result[0].tolist()}
    
    return app