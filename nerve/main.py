import asyncio
import concurrent.futures
import io
import logging
import os
import threading
from concurrent.futures import ThreadPoolExecutor

import grpc

import nerve_pb2
import nerve_pb2_grpc

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger(__name__)

CHUNK_WORDS = 300
CHUNK_OVERLAP = 50

# ── Tunable constants ─────────────────────────────────────────────────────────
_MAX_IMAGES_TOTAL = 20    # hard cap across the whole document
_MAX_IMAGES_PER_PAGE = 3  # per-page image cap
_MIN_IMAGE_PIXELS = 64    # skip icons, logos, HR rules
_BLIP_BATCH = 8           # images per BLIP forward pass
_EMBED_BATCH = 64         # texts per SentenceTransformer forward pass
_PAGE_BATCH = 20          # pages processed per streaming chunk to Go

# Set ZENITH_BLIP_CAPTIONS=1 to caption embedded PDF images with BLIP.
# Default (off): surrounding page text is used — much faster and often more
# informative for technical documents.
_USE_BLIP_FOR_PDF = os.environ.get("ZENITH_BLIP_CAPTIONS", "").lower() in ("1", "true", "yes")

# ── Lazy model state ──────────────────────────────────────────────────────────
_model_lock = threading.Lock()
_embed_model = None
_blip_processor = None
_blip_model = None


def _best_onnx_provider() -> str:
    """Return the fastest available ONNX Runtime execution provider.

    Falls back to CPUExecutionProvider when no GPU is detected so the code
    path is identical on CPU-only machines — no config required.
    """
    try:
        import onnxruntime as ort
        available = ort.get_available_providers()
        for provider in (
            "CUDAExecutionProvider",       # NVIDIA GPU
            "DmlExecutionProvider",        # Windows DirectML (AMD/Intel/NVIDIA)
            "CoreMLExecutionProvider",     # Apple Silicon
        ):
            if provider in available:
                log.info("ONNX provider: %s", provider)
                return provider
    except Exception:
        pass
    return "CPUExecutionProvider"


def _ensure_models() -> None:
    """Load the ONNX embedding model (int8 quantized if available, else fp32).

    Deferred import keeps the gRPC server bindable immediately — torch import
    alone can take 30-90 s, causing WaitReady health-checks to time out.
    """
    global _embed_model
    if _embed_model is not None:
        return
    with _model_lock:
        if _embed_model is not None:
            return
        from sentence_transformers import SentenceTransformer

        provider = _best_onnx_provider()
        # Try int8 quantized ONNX first (2× faster, same quality for semantic search).
        for fname in ("model_quantized.onnx", None):
            try:
                kwargs = {"provider": provider}
                if fname:
                    kwargs["file_name"] = fname
                _embed_model = SentenceTransformer("all-MiniLM-L6-v2", backend="onnx", model_kwargs=kwargs)
                log.info("Embedding model ready (file=%s provider=%s)", fname or "default", provider)
                return
            except Exception as exc:
                if fname:
                    log.info("Quantized ONNX not available (%s), trying fp32", exc)
        raise RuntimeError("Failed to load embedding model")


def _ensure_blip() -> None:
    """Load BLIP lazily — only called when actually needed."""
    global _blip_processor, _blip_model
    if _blip_processor is not None:
        return
    with _model_lock:
        if _blip_processor is not None:
            return
        from transformers import BlipForConditionalGeneration, BlipProcessor

        log.info("Loading BLIP-base captioning model...")
        _blip_processor = BlipProcessor.from_pretrained("Salesforce/blip-image-captioning-base")
        _blip_model = BlipForConditionalGeneration.from_pretrained("Salesforce/blip-image-captioning-base")
        log.info("BLIP model ready")


def _split_into_chunks(text: str) -> list[str]:
    words = text.split()
    chunks, i = [], 0
    while i < len(words):
        chunks.append(" ".join(words[i : i + CHUNK_WORDS]))
        i += CHUNK_WORDS - CHUNK_OVERLAP
    return [c for c in chunks if c.strip()]


# ── Per-page parallel extraction helper ──────────────────────────────────────

def _extract_one_page(file_path: str, page_idx: int, use_blip: bool) -> tuple:
    """Open a fresh fitz.Document for one page (thread-safe: each thread owns its handle).

    Returns (page_num, text_items, image_items) where:
      text_items:  list of (chunk_idx, text, page_rect)
      image_items: list of (base_chunk_idx, content, rect, kind)
                   kind = "blip" → content is a PIL image
                   kind = "text" → content is a context string
    """
    import fitz
    from PIL import Image

    doc = fitz.open(file_path)
    page = doc[page_idx]
    page_num = page_idx + 1
    page_rect = page.rect

    raw_text = page.get_text("text") or ""
    text_chunks = _split_into_chunks(raw_text)
    text_items = [(i, t, page_rect) for i, t in enumerate(text_chunks)]

    image_items = []
    images_on_page = 0
    for img_info in page.get_images(full=True):
        if images_on_page >= _MAX_IMAGES_PER_PAGE:
            break
        try:
            xref = img_info[0]
            base_image = doc.extract_image(xref)
            rects = page.get_image_rects(xref)
            rect = rects[0] if rects else fitz.Rect(0, 0, 0, 0)
            with Image.open(io.BytesIO(base_image["image"])) as probe:
                w, h = probe.size
            if w < _MIN_IMAGE_PIXELS or h < _MIN_IMAGE_PIXELS:
                continue
            base_idx = len(text_chunks) + images_on_page
            if use_blip:
                pil = Image.open(io.BytesIO(base_image["image"])).convert("RGB")
                image_items.append((base_idx, pil, rect, "blip"))
            else:
                ctx = raw_text[:200].strip() or f"figure on page {page_num}"
                image_items.append((base_idx, ctx, rect, "text"))
            images_on_page += 1
        except Exception as exc:
            log.warning("image load failed page=%d: %s", page_num, exc)

    doc.close()
    return page_num, text_items, image_items


def _process_page_batch(
    file_path: str,
    page_indices: list[int],
    images_remaining: int,
    document_id: str,
) -> list[nerve_pb2.Chunk]:
    """Extract and encode a batch of pages in parallel, return Chunk objects.

    Pages are extracted concurrently (each thread opens its own fitz.Document),
    then all text is batch-encoded in a single ONNX pass.
    """
    _ensure_models()
    if _USE_BLIP_FOR_PDF:
        _ensure_blip()

    max_workers = min(len(page_indices), os.cpu_count() or 4, 8)
    page_results: dict[int, tuple] = {}

    # Parallel extraction — pure I/O, no model inference
    with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as pool:
        futures = {
            pool.submit(_extract_one_page, file_path, idx, _USE_BLIP_FOR_PDF): idx
            for idx in page_indices
        }
        for fut in concurrent.futures.as_completed(futures):
            page_idx = futures[fut]
            try:
                page_results[page_idx] = fut.result()
            except Exception as exc:
                log.warning("page %d extraction failed (doc=%s): %s", page_idx, document_id, exc)

    # Merge in page order, applying global image cap
    text_rows: list[tuple] = []   # (page_num, chunk_idx, text, rect, source_type)
    blip_rows: list[tuple] = []   # (page_num, chunk_idx, pil, rect)
    images_collected = 0

    for page_idx in page_indices:
        if page_idx not in page_results:
            continue
        page_num, text_items, image_items = page_results[page_idx]
        for chunk_idx, text, rect in text_items:
            text_rows.append((page_num, chunk_idx, text, rect, "text"))
        for base_idx, content, rect, kind in image_items:
            if images_collected >= images_remaining:
                break
            if kind == "blip":
                blip_rows.append((page_num, base_idx, content, rect))
            else:
                text_rows.append((page_num, base_idx, content, rect, "image_context"))
            images_collected += 1

    # Batch-encode all text rows in one ONNX pass
    text_vecs: list[list[float]] = []
    if text_rows:
        texts = [r[2] for r in text_rows]
        text_vecs = _embed_model.encode(
            texts, batch_size=_EMBED_BATCH, show_progress_bar=False, convert_to_numpy=True
        ).tolist()

    # BLIP pass (only when ZENITH_BLIP_CAPTIONS=1)
    blip_chunks: list[nerve_pb2.Chunk] = []
    if _USE_BLIP_FOR_PDF and blip_rows:
        all_pil = [r[2] for r in blip_rows]
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
        if any(c.strip() for c in captions):
            blip_vecs = _embed_model.encode(
                captions, batch_size=_EMBED_BATCH, show_progress_bar=False, convert_to_numpy=True
            ).tolist()
        else:
            blip_vecs = [[0.0] * 384] * len(captions)
        for i, (page_num, chunk_idx, _pil, rect) in enumerate(blip_rows):
            cap = captions[i] if i < len(captions) else ""
            if cap.strip():
                blip_chunks.append(nerve_pb2.Chunk(
                    text=cap, page_number=page_num, chunk_index=chunk_idx,
                    source_type="image_caption", embedding=blip_vecs[i],
                    bbox_x=float(rect.x0), bbox_y=float(rect.y0),
                    bbox_w=float(rect.width), bbox_h=float(rect.height),
                ))

    # Assemble final chunks
    chunks: list[nerve_pb2.Chunk] = []
    for i, (page_num, chunk_idx, text, rect, source_type) in enumerate(text_rows):
        chunks.append(nerve_pb2.Chunk(
            text=text, page_number=page_num, chunk_index=chunk_idx,
            source_type=source_type, embedding=text_vecs[i],
            bbox_x=float(rect.x0), bbox_y=float(rect.y0),
            bbox_w=float(rect.width), bbox_h=float(rect.height),
        ))
    chunks.extend(blip_chunks)
    return chunks


def _extract_image_sync(file_path: str, document_id: str) -> tuple[str, list[float]]:
    """Caption a standalone image file with BLIP. Always uses BLIP since there
    is no surrounding text to fall back on for standalone image files."""
    from PIL import Image

    _ensure_models()
    _ensure_blip()
    img = Image.open(file_path).convert("RGB")
    inputs = _blip_processor(img, return_tensors="pt")
    out = _blip_model.generate(**inputs, max_new_tokens=50)
    caption = _blip_processor.decode(out[0], skip_special_tokens=True)
    embedding = _embed_model.encode(caption).tolist()
    return caption, embedding


# ── gRPC servicer ─────────────────────────────────────────────────────────────

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
        """Server-side streaming: yields chunks in _PAGE_BATCH-sized page batches.

        Go starts indexing received chunks while Python encodes later pages,
        fully overlapping encoding time with Go's indexing work.
        """
        import fitz

        _ensure_models()
        if _USE_BLIP_FOR_PDF:
            _ensure_blip()

        try:
            doc = fitz.open(request.file_path)
            total_pages = len(doc)
            doc.close()
        except FileNotFoundError:
            await context.abort(grpc.StatusCode.NOT_FOUND, f"file not found: {request.file_path}")
            return
        except Exception as exc:
            log.error("ExtractPDF open failed for %s: %s", request.file_path, exc)
            await context.abort(grpc.StatusCode.INTERNAL, str(exc))
            return

        images_total = 0
        loop = asyncio.get_event_loop()

        for batch_start in range(0, total_pages, _PAGE_BATCH):
            batch_end = min(batch_start + _PAGE_BATCH, total_pages)
            images_remaining = _MAX_IMAGES_TOTAL - images_total

            page_indices = list(range(batch_start, batch_end))
            try:
                chunks = await loop.run_in_executor(
                    self._executor,
                    lambda pi=page_indices, ir=images_remaining: _process_page_batch(
                        request.file_path, pi, ir, request.document_id
                    ),
                )
            except Exception as exc:
                log.error("page batch %d-%d failed (doc=%s): %s",
                          batch_start, batch_end, request.document_id, exc)
                continue

            for chunk in chunks:
                if chunk.source_type in ("image_caption", "image_context"):
                    images_total += 1
                yield chunk

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
