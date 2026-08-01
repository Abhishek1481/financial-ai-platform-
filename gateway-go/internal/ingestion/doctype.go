package ingestion

import (
	"errors"
	"path/filepath"
	"strings"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
)

var ErrUnsupportedFileType = errors.New("ingestion: unsupported file extension")

// extensionDocType maps a lowercase file extension to the proto
// DocumentType it corresponds to. SEC_FILING isn't derivable from an
// extension — a caller opts into it explicitly (see Category in
// Service.Upload) since EDGAR filings share their byte format with
// ordinary HTML/TXT documents; the difference is provenance, not bytes.
var extensionDocType = map[string]commonv1.DocumentType{
	".pdf":  commonv1.DocumentType_DOCUMENT_TYPE_PDF,
	".docx": commonv1.DocumentType_DOCUMENT_TYPE_DOCX,
	".html": commonv1.DocumentType_DOCUMENT_TYPE_HTML,
	".htm":  commonv1.DocumentType_DOCUMENT_TYPE_HTML,
	".txt":  commonv1.DocumentType_DOCUMENT_TYPE_TXT,
}

func docTypeForFilename(filename string) (commonv1.DocumentType, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	dt, ok := extensionDocType[ext]
	if !ok {
		return commonv1.DocumentType_DOCUMENT_TYPE_UNSPECIFIED, ErrUnsupportedFileType
	}
	return dt, nil
}
