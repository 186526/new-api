package systemone

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngBytes is the eight-byte PNG signature, which is all the format probe reads.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func multipartBody(t *testing.T, document string, images map[string][]byte) (string, []byte) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)

	requestPart, err := writer.CreateFormField(MultipartRequestPartName)
	require.NoError(t, err)
	_, err = requestPart.Write([]byte(document))
	require.NoError(t, err)

	for name, content := range images {
		part, createErr := writer.CreatePart(textprotoHeader(name, "image/png"))
		require.NoError(t, createErr)
		_, err = part.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return writer.FormDataContentType(), buffer.Bytes()
}

func TestMultipartBodyFoldsImagesIntoAssets(t *testing.T) {
	document := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"y"}},` +
		`"assets":{"photo":{"type":"image","part":"photo"}}}`
	contentType, body := multipartBody(t, document, map[string][]byte{"photo": pngBytes})

	joined, problems := MultipartBody(contentType, body)
	require.Empty(t, problems)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(joined, &decoded))
	asset := decoded["assets"].(map[string]any)["photo"].(map[string]any)
	assert.Equal(t, "png", asset["format"])
	assert.NotEmpty(t, asset["data_base64"])
	assert.NotContains(t, asset, "part")
}

func TestMultipartBodyRejectsAnUnusedPart(t *testing.T) {
	document := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"y"}},` +
		`"assets":{"photo":{"type":"image","part":"photo"}}}`
	contentType, body := multipartBody(t, document, map[string][]byte{
		"photo":    pngBytes,
		"surprise": pngBytes,
	})

	_, problems := MultipartBody(contentType, body)
	require.Len(t, problems, 1)
	assert.Equal(t, []any{"body", "assets"}, problems[0].Loc)
}

func TestMultipartBodyRequiresTheRequestPart(t *testing.T) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreatePart(textprotoHeader("photo", "image/png"))
	require.NoError(t, err)
	_, err = part.Write(pngBytes)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	_, problems := MultipartBody(writer.FormDataContentType(), buffer.Bytes())
	require.Len(t, problems, 1)
	assert.Equal(t, "missing", problems[0].Type)
}

func textprotoHeader(fieldName, contentType string) textproto.MIMEHeader {
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="`+fieldName+`"`)
	header.Set("Content-Type", contentType)
	return header
}
