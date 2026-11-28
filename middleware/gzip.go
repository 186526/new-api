package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

type readCloser struct {
	io.Reader
	closeFn func() error
}

func (rc *readCloser) Close() error {
	if rc.closeFn != nil {
		return rc.closeFn()
	}
	return nil
}

// normalizeContentCoding lower-cases the Content-Encoding value and drops the
// whitespace around each coding, so a client that writes "X-GZip" or
// " gzip " is read the same as "gzip". RFC 7231 makes the coding token
// case-insensitive, and the upstream that the relay forwards to accepts the
// x-gzip spelling as another name for gzip.
func normalizeContentCoding(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	codings := make([]string, 0, len(parts))
	for _, part := range parts {
		coding := strings.ToLower(strings.TrimSpace(part))
		if coding == "" {
			continue
		}
		codings = append(codings, coding)
	}
	return codings
}

// contentCodingName folds the spellings one coding is written in onto one name.
func contentCodingName(coding string) string {
	switch coding {
	case "x-gzip":
		return "gzip"
	default:
		return coding
	}
}

func DecompressRequestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		maxMB := constant.MaxRequestBodyMB
		if maxMB <= 0 {
			maxMB = 32
		}
		maxBytes := int64(maxMB) << 20

		origBody := c.Request.Body
		wrapMaxBytes := func(body io.ReadCloser) io.ReadCloser {
			return http.MaxBytesReader(c.Writer, body, maxBytes)
		}

		codings := normalizeContentCoding(strings.Join(c.Request.Header.Values("Content-Encoding"), ","))
		// "identity" names no compression, so a client that spells it out reads
		// as one that sent no header.
		if len(codings) == 1 && codings[0] == "identity" {
			codings = nil
		}
		// One compression layer is what the relay unpacks. A stacked body would
		// need every layer unwound in order, and no client of this gateway sends
		// one, so it is refused rather than misread as the outermost coding.
		if len(codings) > 1 {
			abortDecompressionWithStatus(c, http.StatusUnsupportedMediaType, "Stacked Content-Encoding values are not supported")
			return
		}

		decompressed := false
		switch firstCoding(codings) {
		case "":
			// Even for uncompressed bodies, enforce a max size to avoid huge request allocations.
			c.Request.Body = wrapMaxBytes(origBody)
		case "gzip":
			gzipReader, err := gzip.NewReader(origBody)
			if err != nil {
				abortDecompressionWithStatus(c, http.StatusBadRequest, "Invalid gzip request body")
				return
			}
			// Replace the request body with the decompressed data, and enforce a max size (post-decompression).
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: gzipReader,
				closeFn: func() error {
					_ = gzipReader.Close()
					return origBody.Close()
				},
			})
			decompressed = true
		case "br":
			reader := brotli.NewReader(origBody)
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: reader,
				closeFn: func() error {
					return origBody.Close()
				},
			})
			decompressed = true
		case "zstd":
			reader, err := zstd.NewReader(origBody)
			if err != nil {
				abortDecompressionWithStatus(c, http.StatusBadRequest, "Invalid zstd request body")
				return
			}
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: reader,
				closeFn: func() error {
					reader.Close()
					return origBody.Close()
				},
			})
			decompressed = true
		default:
			_ = origBody.Close()
			if isSystemOneDecisionRoute(c) {
				common.WriteTypeSafeError(c, http.StatusUnsupportedMediaType, "Unsupported Content-Encoding")
				c.Abort()
				return
			}
			abortWithOpenAiMessage(c, http.StatusUnsupportedMediaType, common.LogText("unsupported Content-Encoding: %s", c.GetHeader("Content-Encoding")))
			return
		}

		if decompressed {
			c.Request.Header.Del("Content-Encoding")
		}

		// Continue processing the request
		c.Next()
	}
}

func isSystemOneDecisionRoute(c *gin.Context) bool {
	if c.Request.Method != http.MethodPost {
		return false
	}
	switch c.FullPath() {
	case relayconstant.SystemOneContractPathPrefix + "/systemone", relayconstant.SystemOneUpstreamPath:
		return true
	default:
		return false
	}
}

func abortDecompressionWithStatus(c *gin.Context, status int, message string) {
	_ = c.Request.Body.Close()
	if isSystemOneDecisionRoute(c) {
		common.WriteTypeSafeError(c, status, message)
		c.Abort()
		return
	}
	c.AbortWithStatus(status)
}

func firstCoding(codings []string) string {
	if len(codings) == 0 {
		return ""
	}
	return contentCodingName(codings[0])
}
