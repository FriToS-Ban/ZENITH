import sys
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import nerve_pb2
import main as nerve_main


async def test_embed_returns_384_dims(mock_grpc_context):
    servicer = nerve_main.NerveServicer()
    request = nerve_pb2.EmbedRequest(text="hello world")
    response = await servicer.Embed(request, mock_grpc_context)
    assert len(response.embedding) == 384


async def test_embed_calls_encode_once(mock_embed_model, mock_grpc_context):
    mock_embed_model.encode.reset_mock()
    servicer = nerve_main.NerveServicer()
    request = nerve_pb2.EmbedRequest(text="some text")
    await servicer.Embed(request, mock_grpc_context)
    mock_embed_model.encode.assert_called_once()


async def test_embed_batch_correct_count(mock_grpc_context):
    servicer = nerve_main.NerveServicer()
    request = nerve_pb2.BatchEmbedRequest(texts=["a", "b", "c"])
    response = await servicer.EmbedBatch(request, mock_grpc_context)
    assert len(response.embeddings) == 3
    for ev in response.embeddings:
        assert len(ev.elements) == 384


async def test_embed_batch_empty(mock_grpc_context):
    servicer = nerve_main.NerveServicer()
    request = nerve_pb2.BatchEmbedRequest(texts=[])
    response = await servicer.EmbedBatch(request, mock_grpc_context)
    assert len(response.embeddings) == 0


async def test_embed_batch_single(mock_grpc_context):
    servicer = nerve_main.NerveServicer()
    request = nerve_pb2.BatchEmbedRequest(texts=["only one"])
    response = await servicer.EmbedBatch(request, mock_grpc_context)
    assert len(response.embeddings) == 1
    assert len(response.embeddings[0].elements) == 384
