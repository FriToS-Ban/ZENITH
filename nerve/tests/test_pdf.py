import sys
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from unittest.mock import MagicMock, patch, AsyncMock
import grpc
import nerve_pb2
import main as nerve_main


def _make_fake_pdfplumber(pages):
    mock_pdf = MagicMock()
    mock_pdf.__enter__ = MagicMock(return_value=mock_pdf)
    mock_pdf.__exit__ = MagicMock(return_value=False)
    mock_pdf.pages = pages
    return MagicMock(return_value=mock_pdf)


def _make_fake_fitz_doc(pages_images=None):
    if pages_images is None:
        pages_images = [[]]

    # Pre-build page mocks so callers can reference them by index
    fitz_pages = []
    for images in pages_images:
        p = MagicMock()
        p.get_images.return_value = images
        fitz_pages.append(p)

    mock_doc = MagicMock()
    mock_doc.close = MagicMock()
    # __getitem__ on MagicMock is called as method(self, idx), so accept self
    mock_doc.__getitem__ = lambda self_mock, idx: fitz_pages[idx] if idx < len(fitz_pages) else MagicMock()

    mock_fitz = MagicMock()
    mock_fitz.open.return_value = mock_doc
    mock_fitz.Rect.return_value = MagicMock(x0=0, y0=0, width=0, height=0)
    return mock_fitz, mock_doc, fitz_pages


async def test_extract_text_chunks_source_type(mock_grpc_context):
    page = MagicMock()
    page.extract_text.return_value = "hello world this is some text content on the page"
    page.bbox = (0, 0, 612, 792)

    mock_fitz, _, _fitz_pages = _make_fake_fitz_doc([[]])

    with patch("pdfplumber.open", _make_fake_pdfplumber([page])), \
         patch.dict(sys.modules, {"fitz": mock_fitz}):
        servicer = nerve_main.NerveServicer()
        request = nerve_pb2.ExtractPDFRequest(file_path="/fake/file.pdf", document_id="doc1")
        response = await servicer.ExtractPDF(request, mock_grpc_context)

    text_chunks = [c for c in response.chunks if c.source_type == "text"]
    assert len(text_chunks) >= 1
    for chunk in text_chunks:
        assert chunk.source_type == "text"
        assert chunk.page_number == 1


async def test_extract_missing_file_aborts_not_found(mock_grpc_context):
    with patch("pdfplumber.open", side_effect=FileNotFoundError("no such file")), \
         patch.dict(sys.modules, {"fitz": MagicMock()}):
        servicer = nerve_main.NerveServicer()
        request = nerve_pb2.ExtractPDFRequest(file_path="/does/not/exist.pdf", document_id="x")
        await servicer.ExtractPDF(request, mock_grpc_context)

    mock_grpc_context.abort.assert_called_once()
    call_args = mock_grpc_context.abort.call_args[0]
    assert call_args[0] == grpc.StatusCode.NOT_FOUND


async def test_extract_blip_failure_skips_image(mock_grpc_context, mock_blip_model):
    page = MagicMock()
    page.extract_text.return_value = "some real text here for testing"
    page.bbox = (0, 0, 612, 792)

    fake_image_entry = (99,) + (None,) * 7
    mock_fitz, mock_doc, fitz_pages = _make_fake_fitz_doc([[fake_image_entry]])
    fitz_page = fitz_pages[0]
    fitz_page.get_images.return_value = [fake_image_entry]
    fitz_page.get_image_rects.return_value = [MagicMock(x0=0, y0=0, width=100, height=50)]
    mock_doc.extract_image.return_value = {"image": b"\x89PNG"}
    mock_blip_model.generate.side_effect = RuntimeError("BLIP exploded")

    with patch("pdfplumber.open", _make_fake_pdfplumber([page])), \
         patch.dict(sys.modules, {"fitz": mock_fitz}):
        servicer = nerve_main.NerveServicer()
        request = nerve_pb2.ExtractPDFRequest(file_path="/fake/file.pdf", document_id="doc1")
        response = await servicer.ExtractPDF(request, mock_grpc_context)

    text_chunks = [c for c in response.chunks if c.source_type == "text"]
    assert len(text_chunks) >= 1
    caption_chunks = [c for c in response.chunks if c.source_type == "image_caption"]
    assert len(caption_chunks) == 0
    mock_blip_model.generate.side_effect = None


async def test_extract_chunk_splitting(mock_grpc_context):
    long_text = " ".join([f"word{i}" for i in range(700)])
    page = MagicMock()
    page.extract_text.return_value = long_text
    page.bbox = (0, 0, 612, 792)

    mock_fitz, _, _fitz_pages = _make_fake_fitz_doc([[]])

    with patch("pdfplumber.open", _make_fake_pdfplumber([page])), \
         patch.dict(sys.modules, {"fitz": mock_fitz}):
        servicer = nerve_main.NerveServicer()
        request = nerve_pb2.ExtractPDFRequest(file_path="/fake/file.pdf", document_id="doc1")
        response = await servicer.ExtractPDF(request, mock_grpc_context)

    text_chunks = [c for c in response.chunks if c.source_type == "text"]
    assert len(text_chunks) > 1, f"expected multiple chunks for 700-word text, got {len(text_chunks)}"


async def test_extract_total_pages(mock_grpc_context):
    page = MagicMock()
    page.extract_text.return_value = "page one content"
    page.bbox = (0, 0, 612, 792)

    mock_fitz, _, _fitz_pages = _make_fake_fitz_doc([[]])

    with patch("pdfplumber.open", _make_fake_pdfplumber([page])), \
         patch.dict(sys.modules, {"fitz": mock_fitz}):
        servicer = nerve_main.NerveServicer()
        request = nerve_pb2.ExtractPDFRequest(file_path="/fake/file.pdf", document_id="doc1")
        response = await servicer.ExtractPDF(request, mock_grpc_context)

    assert response.total_pages == 1
