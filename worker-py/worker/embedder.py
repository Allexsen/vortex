import os
from sentence_transformers import SentenceTransformer


class Embedder:
    def __init__(self, model_name, device):
        self.model = SentenceTransformer(model_name, device=device)

    def encode(self, chunks):
        return self.model.encode(chunks)