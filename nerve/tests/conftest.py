"""
Stubs heavy ML libraries in sys.modules BEFORE main.py is imported.
This prevents BLIP (~900MB) and sentence-transformers from loading during tests.
"""
import sys
from unittest.mock import MagicMock, AsyncMock
import numpy as np

# ── sentence_transformers stub ────────────────────────────────────────────────
_mock_embed_model = MagicMock()

def _encode(text, **kw):
    if isinstance(text, str):
        return np.zeros(384, dtype=np.float32)
    return np.zeros((len(text), 384), dtype=np.float32)

_mock_embed_model.encode.side_effect = _encode

_mock_st_module = MagicMock()
_mock_st_module.SentenceTransformer.return_value = _mock_embed_model
sys.modules["sentence_transformers"] = _mock_st_module

# ── torch stub ────────────────────────────────────────────────────────────────
sys.modules["torch"] = MagicMock()

# ── transformers stub ─────────────────────────────────────────────────────────
_mock_blip_processor = MagicMock()
_mock_blip_processor.decode.return_value = "a bar chart"

_mock_blip_model = MagicMock()
_mock_blip_model.generate.return_value = MagicMock()

_mock_transformers = MagicMock()
_mock_transformers.BlipProcessor.from_pretrained.return_value = _mock_blip_processor
_mock_transformers.BlipForConditionalGeneration.from_pretrained.return_value = _mock_blip_model
sys.modules["transformers"] = _mock_transformers

# ── pdfplumber stub ───────────────────────────────────────────────────────────
sys.modules["pdfplumber"] = MagicMock()

# ── fitz (PyMuPDF) stub ───────────────────────────────────────────────────────
sys.modules["fitz"] = MagicMock()

# ── Pillow stub ───────────────────────────────────────────────────────────────
sys.modules["PIL"] = MagicMock()
sys.modules["PIL.Image"] = MagicMock()

import pytest

@pytest.fixture
def mock_embed_model():
    return _mock_embed_model

@pytest.fixture
def mock_blip_processor():
    return _mock_blip_processor

@pytest.fixture
def mock_blip_model():
    return _mock_blip_model

@pytest.fixture
def mock_grpc_context():
    ctx = MagicMock()
    ctx.abort = AsyncMock()
    return ctx
