from fastapi import FastAPI
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

app = FastAPI()
model = SentenceTransformer('all-MiniLM-L6-v2')

class TextRequest(BaseModel):
    text: str

class BatchRequest(BaseModel):
    texts: list[str]

@app.get("/health")
async def health():
    return {"status": "ok"}

@app.post("/embed")
async def embed(request: TextRequest):
    vector = model.encode(request.text).tolist()
    return {"embedding": vector}

@app.post("/embed_batch")
async def embed_batch(request: BatchRequest):
    vectors = model.encode(request.texts).tolist()
    return {"embeddings": vectors}
