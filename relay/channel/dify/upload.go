package dify

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
)

// uploadRejection marks a request new-api refuses before it dials the upload
// URL. ConvertOpenAIRequest turns it into a 400 carrying ErrorCodeInvalidRequest
// and skip-retry, so a client with a base64 image past the size limit or a
// channel base URL that is not HTTP(S) reads its own request as wrong instead
// of a channel failure.
type uploadRejection struct{ reason string }

func (e *uploadRejection) Error() string { return e.reason }

func rejectUpload(format string, args ...any) error {
	return &uploadRejection{reason: fmt.Sprintf(format, args...)}
}

// asUploadRejectionError keeps every other error as it is, so an upstream
// failure keeps the surface it had before this file existed.
func asUploadRejectionError(err error) error {
	var rejection *uploadRejection
	if !errors.As(err, &rejection) {
		return err
	}
	return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// maxUploadImageBytes is the decoded size one uploaded image may hold. It is the
// same MAX_FILE_DOWNLOAD_MB ceiling service/file_service.go applies to every
// file the relay downloads, read at call time so a test can lower it.
func maxUploadImageBytes() int64 {
	limitMB := constant.MaxFileDownloadMB
	if limitMB <= 0 {
		limitMB = 64
	}
	return int64(limitMB) << 20
}

// validateUploadURL checks the string handed to fmt.Sprintf before any request
// is made: the scheme must be http or https, the host must be the host of the
// configured base URL, and neither may carry a query, fragment or userinfo that
// would move the request somewhere the channel did not configure.
func validateUploadURL(uploadURL, baseURL string) error {
	parsedUpload, err := url.Parse(uploadURL)
	if err != nil {
		return rejectUpload("upload url is not a valid url: %s", err.Error())
	}
	if parsedUpload.Scheme != "http" && parsedUpload.Scheme != "https" {
		return rejectUpload("upload url must be http or https, got scheme %q", parsedUpload.Scheme)
	}
	if parsedUpload.User != nil {
		return rejectUpload("upload url must not carry credentials")
	}
	if parsedUpload.RawQuery != "" || parsedUpload.Fragment != "" {
		return rejectUpload("upload url must not carry a query or fragment")
	}

	parsedBase, err := url.Parse(baseURL)
	if err != nil || parsedBase.Host == "" {
		return rejectUpload("channel base url is not a valid absolute url")
	}
	if !strings.EqualFold(parsedUpload.Host, parsedBase.Host) {
		return rejectUpload("upload url host %q is not the channel base url host %q", parsedUpload.Host, parsedBase.Host)
	}
	return nil
}

// imagePayload returns the bytes of a data: URL after its prefix. A URL without
// a prefix is returned whole, which is how the relay has always read it.
func imagePayload(imageURL string) string {
	if idx := strings.Index(imageURL, ","); idx != -1 {
		return imageURL[idx+1:]
	}
	return imageURL
}

// imageMimeType is the type the form part and its filename are built from.
func imageMimeType(image *dto.MessageImageUrl) string {
	if image.MimeType == "" {
		return "image/jpeg"
	}
	return image.MimeType
}

// validBase64 reports whether encoded decodes without error. The decoder fails
// only once the body is being streamed upstream, so the payload is decoded into
// io.Discard here instead: a malformed image is dropped before the upload URL is
// dialed, which is what the decode-then-buffer uploader did. Nothing is held.
func validBase64(encoded string) bool {
	_, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
	return err == nil
}

// uploadDifyImage posts one image as multipart/form-data and returns the file
// id. The base64 payload is decoded straight into the multipart writer through
// io.Pipe, so the decoded image is never held as a byte slice and the multipart
// body is never buffered: peak live memory is the request body plus the pipe
// buffer, instead of the request body plus the decoded image plus a body buffer
// that doubles while it grows.
//
// Upstream failures keep the surface they had: logged, and the image is dropped
// from the request. Only a request new-api itself refuses returns an error.
func uploadDifyImage(info *relaycommon.RelayInfo, user string, image *dto.MessageImageUrl) (*DifyFile, error) {
	uploadURL := fmt.Sprintf("%s/v1/files/upload", info.ChannelBaseUrl)
	if err := validateUploadURL(uploadURL, info.ChannelBaseUrl); err != nil {
		return nil, err
	}

	encoded := imagePayload(image.Url)
	limit := maxUploadImageBytes()
	if decoded := base64.StdEncoding.DecodedLen(len(encoded)); int64(decoded) > limit {
		return nil, rejectUpload("image decodes to %d bytes, past the %d MB upload limit", decoded, limit>>20)
	}
	if !validBase64(encoded) {
		common.SysLog(common.LogText("failed to decode base64 image"))
		return nil, nil
	}

	mimeType := imageMimeType(image)
	bodyReader, bodyWriter := io.Pipe()
	writer := multipart.NewWriter(bodyWriter)
	go func() {
		bodyWriter.CloseWithError(writeUploadForm(writer, user, mimeType, encoded))
	}()

	req, err := http.NewRequest(http.MethodPost, uploadURL, bodyReader)
	if err != nil {
		bodyReader.CloseWithError(err)
		common.SysLog(common.LogText("failed to create request: %s", err.Error()))
		return nil, nil
	}
	// Closing the read side unblocks the form writer when the request is over,
	// including when it failed before reading the whole body.
	defer bodyReader.Close()

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))

	resp, err := service.GetHttpClient().Do(req)
	if err != nil {
		common.SysLog(common.LogText("failed to send request: %s", err.Error()))
		return nil, nil
	}
	defer resp.Body.Close()

	var result struct {
		Id string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		common.SysLog(common.LogText("failed to decode response: %s", err.Error()))
		return nil, nil
	}

	return &DifyFile{
		UploadFileId: result.Id,
		Type:         "image",
		TransferMode: "local_file",
	}, nil
}

// writeUploadForm writes the multipart body into the pipe. The decoder reads the
// base64 payload incrementally and the multipart writer forwards each chunk as
// it is produced, so neither the decoded image nor the form body is ever held
// whole.
func writeUploadForm(writer *multipart.Writer, user, mimeType, encoded string) error {
	if err := writer.WriteField("user", user); err != nil {
		return err
	}
	part, err := writer.CreateFormFile("file", fmt.Sprintf("image.%s", strings.TrimPrefix(mimeType, "image/")))
	if err != nil {
		return err
	}
	decoded := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	if _, err := io.Copy(part, decoded); err != nil {
		return err
	}
	return writer.Close()
}
