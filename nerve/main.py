import asyncio
import io
import logging
import threading
from concurrent.futures import ThreadPoolExecutor

import fitz  # PyMuPDF
import grpc
import pdfplumber
from PIL import Image
from sentence_transformers import SentenceTransformer
from transformers import BlipForConditionalGeneration, BlipProcessor

import nerve_pb2
import nerve_pb2_grpc

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger(__name__)

CHUNK_WORDS = 300
CHUNK_OVERLAP = 50

# Models are loaded lazily on first use so the gRPC server can bind to its port
# immediately. On a fresh install this avoids timing out the health-check while
# HuggingFace downloads ~1GB of weights in the background.
_model_lock = threading.Lock()
_embed_model = None
_blip_processor = None
_blip_model = None


def _ensure_models() -> None:
    global _embed_model, _blip_processor, _blip_model
    if _embed_model is not None:
        return
    with _model_lock:
        if _embed_model is not None:
            return
        log.info("Loading all-MiniLM-L6-v2 embedding model...")
        _embed_model = SentenceTransformer("all-MiniLM-L6-v2")
        log.info("Loading BLIP-base captioning model...")
        _blip_processor = BlipProcessor.from_pretrained("Salesforce/blip-image-captioning-base")
        _blip_model = BlipForConditionalGeneration.from_pretrained("Salesforce/blip-image-captioning-base")
        log.info("Models ready")


def _split_into_chunks(text: str) -> list[str]:
    words = text.split()
    chunks = []
    i = 0
    while i < len(words):
        chunks.append(" ".join(words[i : i + CHUNK_WORDS]))
        i += CHUNK_WORDS - CHUNK_OVERLAP
    return [c for c in chunks if c.strip()]


def _extract_pdf_sync(file_path: str, document_id: str) -> tuple[list[nerve_pb2.Chunk], int]:
    _ensure_models()
    chunks: list[nerve_pb2.Chunk] = []
    fitz_doc = fitz.open(file_path)

    with pdfplumber.open(file_path) as pdf:
        total_pages = len(pdf.pages)
        for page_num, page in enumerate(pdf.pages, start=1):
            raw_text = page.extract_text() or ""
            text_chunks = _split_into_chunks(raw_text)
            page_bbox = page.bbox  # (x0, top, x1, bottom)

            for chunk_idx, chunk_text in enumerate(text_chunks):
                vec = _embed_model.encode(chunk_text).tolist()
                chunks.append(
                    nerve_pb2.Chunk(
                        text=chunk_text,
                        page_number=page_num,
                        chunk_index=chunk_idx,
                        source_type="text",
                        embedding=vec,
                        bbox_x=float(page_bbox[0]),
                        bbox_y=float(page_bbox[1]),
                        bbox_w=float(page_bbox[2] - page_bbox[0]),
                        bbox_h=float(page_bbox[3] - page_bbox[1]),
                    )
                )

            fitz_page = fitz_doc[page_num - 1]
            base_chunk_idx = len(text_chunks)

            for img_offset, img_info in enumerate(fitz_page.get_images(full=True)):
                try:
                    xref = img_info[0]
                    base_image = fitz_doc.extract_image(xref)
                    img = Image.open(io.BytesIO(base_image["image"])).convert("RGB")

                    rects = fitz_page.get_image_rects(xref)
                    rect = rects[0] if rects else fitz.Rect(0, 0, 0, 0)

                    inputs = _blip_processor(img, return_tensors="pt")
                    out = _blip_model.generate(**inputs, max_new_tokens=50)
                    caption = _blip_processor.decode(out[0], skip_special_tokens=True)

                    vec = _embed_model.encode(caption).tolist()
                    chunks.append(
                        nerve_pb2.Chunk(
                            text=caption,
                            page_number=page_num,
                            chunk_index=base_chunk_idx + img_offset,
                            source_type="image_caption",
                            embedding=vec,
                            bbox_x=float(rect.x0),
                            bbox_y=float(rect.y0),
                            bbox_w=float(rect.width),
                            bbox_h=float(rect.height),
                        )
                    )
                except Exception as exc:
                    log.warning(
                        "BLIP caption failed for image on page %d (doc=%s): %s",
                        page_num, document_id, exc,
                    )

    fitz_doc.close()
    return chunks, total_pages


def _extract_image_sync(file_path: str, document_id: str) -> tuple[str, list[float]]:
    _ensure_models()
    img = Image.open(file_path).convert("RGB")
    inputs = _blip_processor(img, return_tensors="pt")
    out = _blip_model.generate(**inputs, max_new_tokens=50)
    caption = _blip_processor.decode(out[0], skip_special_tokens=True)
    embedding = _embed_model.encode(caption).tolist()
    return caption, embedding


class NerveServicer(nerve_pb2_grpc.NerveServiceServicer):
    def __init__(self):
        self._executor = ThreadPoolExecutor(max_workers=4)

    async def Embed(self, request, context):
        loop = asyncio.get_event_loop()
        vec = await loop.run_in_executor(
            self._executor,
            lambda: (_ensure_models(), _embed_model.encode(request.text).tolist())[1],
        )
        return nerve_pb2.EmbedResponse(embedding=vec)

    async def EmbedBatch(self, request, context):
        loop = asyncio.get_event_loop()
        vecs = await loop.run_in_executor(
            self._executor,
            lambda: (_ensure_models(), _embed_model.encode(list(request.texts)).tolist())[1],
        )
        embeddings = [nerve_pb2.EmbedVector(elements=v) for v in vecs]
        return nerve_pb2.BatchEmbedResponse(embeddings=embeddings)

    async def ExtractPDF(self, request, context):
        loop = asyncio.get_event_loop()
        try:
            chunks, total_pages = await loop.run_in_executor(
                self._executor,
                lambda: _extract_pdf_sync(request.file_path, request.document_id),
            )
        except FileNotFoundError:
            await context.abort(grpc.StatusCode.NOT_FOUND, f"file not found: {request.file_path}")
            return
        except Exception as exc:
            log.error("ExtractPDF failed for %s: %s", request.file_path, exc)
            await context.abort(grpc.StatusCode.INTERNAL, str(exc))
            return

        return nerve_pb2.ExtractPDFResponse(chunks=chunks, total_pages=total_pages)

    async def ExtractImage(self, request, context):
        loop = asyncio.get_event_loop()
        try:
            caption, embedding = await loop.run_in_executor(
                self._executor,
                lambda: _extract_image_sync(request.file_path, request.document_id),
            )
        except FileNotFoundError:
            await context.abort(grpc.StatusCode.NOT_FOUND, f"file not found: {request.file_path}")
            return
        except Exception as exc:
            log.error("ExtractImage failed for %s: %s", request.file_path, exc)
            await context.abort(grpc.StatusCode.INTERNAL, str(exc))
            return
        return nerve_pb2.ExtractImageResponse(caption=caption, embedding=embedding)


async def serve():
    server = grpc.aio.server()
    nerve_pb2_grpc.add_NerveServiceServicer_to_server(NerveServicer(), server)
    server.add_insecure_port("[::]:8000")
    await server.start()
    log.info("Nerve gRPC server listening on :8000")
    await server.wait_for_termination()


if __name__ == "__main__":
    asyncio.run(serve())
