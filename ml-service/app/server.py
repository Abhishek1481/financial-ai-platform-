"""ml-service gRPC server entrypoint.

Wires config, logging, and every servicer into a single grpc.aio server,
exposes the standard gRPC health-checking and reflection services, and
shuts down gracefully on SIGINT/SIGTERM. This process has no HTTP port —
gateway-go is the only client, and it speaks gRPC exclusively.

Run directly with:  python -m app.server
"""

from __future__ import annotations

import asyncio
import signal
from collections.abc import Callable

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from grpc_reflection.v1alpha import reflection

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.config import Settings, get_settings
from app.embeddings.model import EmbeddingModel
from app.embeddings.vector_store import FaissVectorStore
from app.logging import configure_logging, get_logger
from app.servicers.embeddings import EmbeddingServicer
from app.servicers.evaluation import EvaluationServicer
from app.servicers.ingestion import IngestionServicer
from app.servicers.rag import RAGServicer
from app.servicers.search import SearchServicer
from embeddings.v1 import embeddings_pb2, embeddings_pb2_grpc
from evaluation.v1 import evaluation_pb2, evaluation_pb2_grpc
from ingestion.v1 import ingestion_pb2, ingestion_pb2_grpc
from rag.v1 import rag_pb2, rag_pb2_grpc
from search.v1 import search_pb2, search_pb2_grpc

logger = get_logger(__name__)

# (full service name, add-to-server registration fn, servicer instance).
# Declared as data rather than five repeated add_*_to_server call sites so
# registering a service, tracking its full name for health/reflection, and
# marking it SERVING can't drift out of sync with each other.
_ServiceRegistration = tuple[str, Callable[..., None], object]


def _build_services(settings: Settings) -> tuple[_ServiceRegistration, ...]:
    # EmbeddingModel loads its weights lazily on first use, not here —
    # constructing it is cheap and does not block server startup (see
    # app/embeddings/model.py). FaissVectorStore only needs the
    # dimension, a static config value, not the loaded model.
    embedding_model = EmbeddingModel(settings.embedding_model_name)
    vector_store = FaissVectorStore(
        dimension=settings.embedding_dimension,
        persist_dir=settings.vector_store_dir or None,
    )

    return (
        (
            ingestion_pb2.DESCRIPTOR.services_by_name["IngestionService"].full_name,
            ingestion_pb2_grpc.add_IngestionServiceServicer_to_server,
            IngestionServicer(),
        ),
        (
            embeddings_pb2.DESCRIPTOR.services_by_name["EmbeddingService"].full_name,
            embeddings_pb2_grpc.add_EmbeddingServiceServicer_to_server,
            EmbeddingServicer(embedding_model, vector_store),
        ),
        (
            search_pb2.DESCRIPTOR.services_by_name["SearchService"].full_name,
            search_pb2_grpc.add_SearchServiceServicer_to_server,
            SearchServicer(),
        ),
        (
            rag_pb2.DESCRIPTOR.services_by_name["RAGService"].full_name,
            rag_pb2_grpc.add_RAGServiceServicer_to_server,
            RAGServicer(),
        ),
        (
            evaluation_pb2.DESCRIPTOR.services_by_name["EvaluationService"].full_name,
            evaluation_pb2_grpc.add_EvaluationServiceServicer_to_server,
            EvaluationServicer(),
        ),
    )


async def build_server(settings: Settings) -> tuple[grpc.aio.Server, int]:
    """Constructs and binds (but does not start) the gRPC server.

    Split out from `serve()` so tests can build and start a server without
    also installing OS signal handlers, which only make sense for the real
    process.
    """
    server = grpc.aio.server()

    reflected_service_names = [health.SERVICE_NAME, reflection.SERVICE_NAME]

    health_servicer = health.aio.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    for full_name, add_to_server, servicer in _build_services(settings):
        add_to_server(servicer, server)
        reflected_service_names.append(full_name)
        # Every registered service reports SERVING immediately, even though
        # most of their RPCs currently abort with UNIMPLEMENTED: health
        # tracks "is the process up and able to accept this service's
        # calls," not "is every RPC on it fully built." A client hitting an
        # unbuilt RPC gets UNIMPLEMENTED from the call itself, which is a
        # more precise signal than a failing health check would be.
        await health_servicer.set(full_name, health_pb2.HealthCheckResponse.SERVING)
    await health_servicer.set(health.OVERALL_HEALTH, health_pb2.HealthCheckResponse.SERVING)

    if settings.reflection_enabled:
        reflection.enable_server_reflection(reflected_service_names, server)

    bound_port = server.add_insecure_port(f"{settings.grpc_host}:{settings.grpc_port}")
    return server, bound_port


def _install_signal_handlers(loop: asyncio.AbstractEventLoop, on_signal: Callable[[], None]) -> None:
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, on_signal)
        except NotImplementedError:
            # ProactorEventLoop (default on Windows) doesn't support
            # add_signal_handler. Production runs in a Linux container
            # (Phase 16), where the branch above is used; this fallback
            # only exists so local development on Windows also shuts down
            # cleanly on Ctrl-C.
            signal.signal(sig, lambda *_args: loop.call_soon_threadsafe(on_signal))


async def serve() -> None:
    settings = get_settings()
    configure_logging(settings.log_level)

    server, bound_port = await build_server(settings)
    await server.start()
    logger.info(
        "ml-service listening",
        extra={"port": bound_port, "environment": settings.environment},
    )

    stop_event = asyncio.Event()
    _install_signal_handlers(asyncio.get_running_loop(), stop_event.set)

    await stop_event.wait()
    logger.info("shutdown signal received, draining in-flight RPCs")
    await server.stop(grace=10)
    logger.info("ml-service stopped")


if __name__ == "__main__":
    asyncio.run(serve())
