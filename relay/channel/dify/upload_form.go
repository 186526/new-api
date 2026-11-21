package dify

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
)

// uploadForm is the multipart envelope written around one image. The boundary
// is chosen first and the prologue and epilogue are built from it, so the bytes
// that do not depend on the payload are known before the body is written and
// the upload can declare an exact Content-Length the way a buffered body did,
// while the image itself is still streamed.
type uploadForm struct {
	boundary string
	prologue []byte
	epilogue []byte
}

func newUploadForm(user, mimeType string) (*uploadForm, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	form := &uploadForm{boundary: hex.EncodeToString(raw[:])}

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.SetBoundary(form.boundary); err != nil {
		return nil, err
	}
	if err := writer.WriteField("user", user); err != nil {
		return nil, err
	}
	if _, err := writer.CreateFormFile("file", fmt.Sprintf("image.%s", strings.TrimPrefix(mimeType, "image/"))); err != nil {
		return nil, err
	}
	form.prologue = buffer.Bytes()
	form.epilogue = []byte("\r\n--" + form.boundary + "--\r\n")
	return form, nil
}

// contentLength is the number of bytes stream writes for a payload that decodes
// to decodedBytes. Both numbers come from the same decoder, so the declared
// length matches what the pipe yields even when the payload carries line breaks.
func (f *uploadForm) contentLength(decodedBytes int64) int64 {
	return int64(len(f.prologue)) + decodedBytes + int64(len(f.epilogue))
}

func (f *uploadForm) contentType() string {
	return fmt.Sprintf("multipart/form-data; boundary=%s", f.boundary)
}

// stream writes the prologue, the image decoded incrementally from its base64
// payload, and the epilogue. The decoder reads incrementally and the writer
// forwards each chunk as it is produced, so neither the decoded image nor the
// form body is ever held whole.
func (f *uploadForm) stream(w io.Writer, encoded string) error {
	if _, err := w.Write(f.prologue); err != nil {
		return err
	}
	if _, err := io.Copy(w, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))); err != nil {
		return err
	}
	_, err := w.Write(f.epilogue)
	return err
}
