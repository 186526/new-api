package common

import (
	"mime"
	"net/http"
	"strings"
)

// MediaTypeOf answers the media type of a Content-Type value with the case and
// the parameters removed, so "APPLICATION/JSON; charset=utf-8" reads as
// "application/json". RFC 7231 makes the type and the subtype case-insensitive,
// and every parameter is optional, so a caller that only compares media types
// must not read the header as a case-sensitive prefix. A value the parser cannot
// read falls back to the substring before the first ";", trimmed and lowered,
// which is what a malformed header still gets right.
func MediaTypeOf(contentType string) string {
	if contentType == "" {
		return ""
	}
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		return mediaType
	}
	cut := contentType
	if index := strings.IndexByte(cut, ';'); index >= 0 {
		cut = cut[:index]
	}
	return strings.ToLower(strings.TrimSpace(cut))
}

// IsJSONMediaType reports whether the media type is JSON. The suffix form,
// "application/problem+json" and the like, counts as JSON too.
func IsJSONMediaType(mediaType string) bool {
	return mediaType == "application/json" || mediaType == "text/json" || strings.HasSuffix(mediaType, "+json")
}

// IsJSONContentType reports whether a Content-Type value names a JSON body.
func IsJSONContentType(contentType string) bool {
	return IsJSONMediaType(MediaTypeOf(contentType))
}

// IsMultipartFormContentType reports whether a Content-Type value names a
// multipart form body.
func IsMultipartFormContentType(contentType string) bool {
	return MediaTypeOf(contentType) == "multipart/form-data"
}

// RequestIsJSON reports whether the request carries a JSON body.
func RequestIsJSON(request *http.Request) bool {
	if request == nil {
		return false
	}
	return IsJSONContentType(request.Header.Get("Content-Type"))
}
