import asyncio
import io
import logging
import threading
from concurrent.futures import ThreadPoolExecutor

import grpc

import nerve_pb2
import nerve_pb2_grpc

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger(__name__)

CHUNK_WORDS = 300
CHUNK_OVERLAP = 50

# Image extraction limits — BLIP on CPU is expensive; cap images aggressively
# to keep large PDFs from timing out.
_MAX_IMAGES_TOTAL = 20    # hard cap across the whole document
_MAX_IMAGES_PER_PAGE = 3  # skip decorative/repeated images in image-heavy pages
_MIN_IMAGE_PIXELS = 64    # skip icons, logos, and HR rules
_BLIP_BATCH = 8           # images per BLIP forward pass
_EMBED_BATCH = 64         # texts per SentenceTransformer forward pass

# Heavy ML libraries (torch, sentence_transformers, transformers) are imported
# inside _ensure_models() so the gRPC server can bind its port immediately on
# startup. Without this, torch import alone can take 30-90 s, causing the
# WaitReady health-check to time out before the server is even listening.
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
        from sentence_transformers import SentenceTransformer
        from transformers import BlipForConditionalGeneration, BlipProcessor

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
    """Two-pass extraction: collect everything first, then batch-infer.

    Pass 1 — I/O only (fast): walk every page with PyMuPDF, accumulate raw
    text chunks and PIL images.  No model inference happens here.

    Pass 2 — single batch encode: call _embed_model.encode() once for ALL
    collected text chunks.  SentenceTransformer amortises tokenisation and
    GPU/CPU SIMD across the whole batch, making this ~batch_size× faster
    than the old one-call-per-chunk loop.

    Pass 3 — batched BLIP + single encode: caption all collected images in
    groups of _BLIP_BATCH, then encode all captions in one shot.
    """
    import fitz  # PyMuPDF — deferred to avoid slowing server startup
    from PIL import Image

    _ensure_models()

    doc = fitz.open(file_path)
    total_pages = len(doc)

    # ── Pass 1: collect text rows and image rows (pure I/O, no inference) ────
    # text_rows:  (page_num, chunk_idx, text, page_rect)
    # image_rows: (page_num, base_chunk_idx, pil_image, image_rect)
    text_rows: list[tuple[int, int, str, fitz.Rect]] = []
    image_rows: list = []
    images_total = 0

    for page_idx in range(total_pages):
        page = doc[page_idx]
        page_num = page_idx + 1
        page_rect = page.rect

        # PyMuPDF text extraction is C++-backed and ~10× faster than pdfplumber
        raw_text = page.get_text("text") or ""
        text_chunks = _split_into_chunks(raw_text)
        for chunk_idx, chunk_text in enumerate(text_chunks):
            text_rows.append((page_num, chunk_idx, chunk_text, page_rect))

        if images_total >= _MAX_IMAGES_TOTAL:
            continue

        images_on_page = 0
        for img_info in page.get_images(full=True):
            if images_total >= _MAX_IMAGES_TOTAL or images_on_page >= _MAX_IMAGES_PER_PAGE:
                break
            try:
                xref = img_info[0]
                base_image = doc.extract_image(xref)
                pil = Image.open(io.BytesIO(base_image["image"])).convert("RGB")
                if pil.width < _MIN_IMAGE_PIXELS or pil.height < _MIN_IMAGE_PIXELS:
                    continue  # skip icons, rules, decorations
                rects = page.get_image_rects(xref)
                rect = rects[0] if rects else fitz.Rect(0, 0, 0, 0)
                base_chunk_idx = len(text_chunks) + images_on_page
                image_rows.append((page_num, base_chunk_idx, pil, rect))
                images_total += 1
                images_on_page += 1
            except Exception as exc:
                log.warning("image load failed page=%d doc=%s: %s", page_num, document_id, exc)

    doc.close()

    # ── Pass 2: batch-encode ALL text chunks in one shot ─────────────────────
    text_vecs: list[list[float]] = []
    if text_rows:
        all_texts = [row[2] for row in text_rows]
        text_vecs = _embed_model.encode(
            all_texts, batch_size=_EMBED_BATCH, show_progress_bar=False, convert_to_numpy=True
        ).tolist()

    # ── Pass 3: batch-BLIP then batch-encode captions ─────────────────────────
    image_captions: list[str] = []
    image_vecs: list[list[float]] = []
    if image_rows:
        all_pil = [row[2] for row in image_rows]
        captions: list[str] = []
        for i in range(0, len(all_pil), _BLIP_BATCH):
            batch = all_pil[i : i + _BLIP_BATCH]
            try:
                inputs = _blip_processor(batch, return_tensors="pt", padding=True)
                out = _blip_model.generate(**inputs, max_new_tokens=50)
                for o in out:
                    captions.append(_blip_processor.decode(o, skip_special_tokens=True))
            except Exception as exc:
                log.warning("BLIP batch failed (%d imgs, doc=%s): %s", len(batch), document_id, exc)
                captions.extend([""] * len(batch))
        image_captions = captions
        if any(c.strip() for c in captions):
            image_vecs = _embed_model.encode(
                captions, batch_size=_EMBED_BATCH, show_progress_bar=False, convert_to_numpy=True
            ).tolist()
        else:
            image_vecs = [[0.0] * 384] * len(captions)

    # ── Assemble proto chunks ─────────────────────────────────────────────────
    chunks: list[nerve_pb2.Chunk] = []

    for i, (page_num, chunk_idx, chunk_text, rect) in enumerate(text_rows):
        chunks.append(nerve_pb2.Chunk(
            text=chunk_text,
            page_number=page_num,
            chunk_index=chunk_idx,
            source_type="text",
            embedding=text_vecs[i],
            bbox_x=float(rect.x0),
            bbox_y=float(rect.y0),
            bbox_w=float(rect.width),
            bbox_h=float(rect.height),
        ))

    for i, (page_num, chunk_idx, _pil, rect) in enumerate(image_rows):
        cap = image_captions[i] if i < len(image_captions) else ""
        if not cap.strip():
            continue
        chunks.append(nerve_pb2.Chunk(
            text=cap,
            page_number=page_num,
            chunk_index=chunk_idx,
            source_type="image_caption",
            embedding=image_vecs[i],
            bbox_x=float(rect.x0),
            bbox_y=float(rect.y0),
            bbox_w=float(rect.width),
            bbox_h=float(rect.height),
        ))

    return chunks, total_pages


def _extract_image_sync(file_path: str, document_id: str) -> tuple[str, list[float]]:
    from PIL import Image

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
