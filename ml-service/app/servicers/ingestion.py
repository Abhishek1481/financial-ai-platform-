"""IngestionService: reads a document from object storage and extracts
text, tables, and best-effort metadata from it.

Called by a gateway-go worker (see gateway-go/internal/worker), never
directly by an HTTP request — extraction can take real time on a large
filing and has no business sitting on a request a client is blocked on
(see proto/README.md's "Why these RPC shapes" section).
"""

from __future__ import annotations

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.extraction.base import ExtractionError
from app.extraction.factory import get_extractor
from app.extraction.sec_metadata import infer_sec_metadata
from app.logging import get_logger
from app.storage import ObjectNotFoundError, get_reader
from common.v1 import common_pb2
from ingestion.v1 import ingestion_pb2, ingestion_pb2_grpc

logger = get_logger(__name__)


class IngestionServicer(ingestion_pb2_grpc.IngestionServiceServicer):
    async def ExtractDocument(
        self,
        request: ingestion_pb2.ExtractDocumentRequest,
        context: grpc.aio.ServicerContext,
    ) -> ingestion_pb2.ExtractDocumentResponse:
        try:
            reader = get_reader(request.s3_uri)
        except NotImplementedError as exc:
            await context.abort(grpc.StatusCode.UNIMPLEMENTED, str(exc))
            return

        try:
            raw_bytes = reader.read(request.s3_uri)
        except ObjectNotFoundError as exc:
            await context.abort(grpc.StatusCode.NOT_FOUND, str(exc))
            return

        try:
            extractor = get_extractor(request.doc_type)
        except ValueError as exc:
            await context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
            return

        try:
            result = extractor.extract(raw_bytes)
        except ExtractionError as exc:
            await context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
            return
        except Exception:
            logger.exception(
                "unexpected extraction failure",
                extra={"document_id": request.document_id, "doc_type": request.doc_type},
            )
            await context.abort(grpc.StatusCode.INTERNAL, "extraction failed")
            return

        metadata = result.metadata
        if request.doc_type == common_pb2.DOCUMENT_TYPE_SEC_FILING:
            metadata = infer_sec_metadata(result.raw_text)

        logger.info(
            "document extracted",
            extra={
                "document_id": request.document_id,
                "doc_type": request.doc_type,
                "text_length": len(result.raw_text),
                "table_count": len(result.tables),
                "page_count": result.page_count,
            },
        )

        return ingestion_pb2.ExtractDocumentResponse(
            raw_text=result.raw_text,
            tables=[_to_proto_table(t) for t in result.tables],
            inferred_metadata=common_pb2.FinancialMetadata(
                ticker=metadata.ticker,
                company_name=metadata.company_name,
                filing_type=metadata.filing_type,
                fiscal_period=metadata.fiscal_period,
            ),
            page_count=result.page_count,
        )


def _to_proto_table(table) -> ingestion_pb2.ExtractedTable:
    return ingestion_pb2.ExtractedTable(
        headers=table.headers,
        rows=[
            ingestion_pb2.ExtractedTableRow(
                cells=[
                    ingestion_pb2.ExtractedTableCell(value=c.value, is_numeric=c.is_numeric)
                    for c in row
                ]
            )
            for row in table.rows
        ],
        caption=table.caption,
        page_number=table.page_number,
    )
