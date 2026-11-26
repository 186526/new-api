package systemone

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"sort"
	"strings"
)

// MultipartRequestPartName is the part that holds the JSON document. The
// upstream requires it by this name; every other part is an image an asset
// refers to by the name of that part.
const MultipartRequestPartName = "request"

// MultipartBody folds a multipart System One request into the JSON body the
// upstream also accepts: the part named "request" is the document, and every
// other part is an image that one asset names through its "part" field.
//
// A caller that sends multipart has usually copied the SDK, which sends images
// as separate parts instead of base64 inside the JSON. Folding here means the
// rest of the relay path reads one shape.
func MultipartBody(contentType string, body []byte) ([]byte, []ValidationError) {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, []ValidationError{
			FieldValidationError(nil, "multipart Content-Type is not readable", "value_error"),
		}
	}
	boundary := parameters["boundary"]
	if boundary == "" {
		return nil, []ValidationError{
			FieldValidationError(nil, "multipart Content-Type names no boundary", "value_error"),
		}
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := map[string]multipartPart{}
	// The document is read before the images are joined to it, but a part that
	// follows it is still collected, and a second document replaces the first.
	var document map[string]any
	documentSeen := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, []ValidationError{
				FieldValidationError(nil, "multipart body is not readable", "value_error"),
			}
		}
		name := part.FormName()
		if name == "" {
			_ = part.Close()
			return nil, []ValidationError{
				FieldValidationError(nil, "every multipart part needs a name", "value_error"),
			}
		}
		content, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			return nil, []ValidationError{
				FieldValidationError([]any{name}, "multipart part is not readable", "value_error"),
			}
		}
		if name == MultipartRequestPartName {
			var decoded map[string]any
			decoder := json.NewDecoder(bytes.NewReader(content))
			decoder.UseNumber()
			if decodeErr := decoder.Decode(&decoded); decodeErr != nil {
				return nil, []ValidationError{
					FieldValidationError([]any{name}, "The request part is not a JSON object", "json_invalid"),
				}
			}
			document = decoded
			documentSeen = true
			continue
		}
		if _, duplicate := parts[name]; duplicate {
			return nil, []ValidationError{
				FieldValidationError([]any{name}, "Two parts share the name of one image", "value_error"),
			}
		}
		parts[name] = multipartPart{
			contentType: part.Header.Get("Content-Type"),
			content:     content,
		}
	}

	if !documentSeen {
		return nil, []ValidationError{
			FieldValidationError([]any{MultipartRequestPartName}, "Field required", "missing"),
		}
	}

	problems, joined := joinMultipartAssets(document, parts)
	if len(problems) > 0 {
		return nil, problems
	}

	encoded, err := json.Marshal(joined)
	if err != nil {
		return nil, []ValidationError{
			FieldValidationError(nil, "The request could not be rebuilt", "value_error"),
		}
	}
	return encoded, nil
}

type multipartPart struct {
	contentType string
	content     []byte
}

// joinMultipartAssets writes every image part into the asset that names it, as
// data_base64 plus the format the bytes are in. An asset whose part names no
// part is reported, and a part no asset names is reported too: the upstream
// requires every part to be used by exactly one asset.
func joinMultipartAssets(document map[string]any, parts map[string]multipartPart) ([]ValidationError, map[string]any) {
	rawAssets, hasAssets := document["assets"]
	assets, isObject := rawAssets.(map[string]any)
	if hasAssets && !isObject {
		return []ValidationError{
			FieldValidationError([]any{"assets"}, "Input should be a valid object", "dict_type"),
		}, nil
	}

	used := map[string]struct{}{}
	var problems []ValidationError
	for _, name := range sortedKeys(assets) {
		asset, ok := assets[name].(map[string]any)
		if !ok {
			// The asset shape is the schema validator's business; there is
			// nothing to join here.
			continue
		}
		partName, hasPart := asset["part"].(string)
		if !hasPart || partName == "" {
			continue
		}
		part, exists := parts[partName]
		if !exists {
			problems = append(problems, FieldValidationError(
				[]any{"assets", name, "part"},
				fmt.Sprintf("No multipart part is named %q", partName),
				"value_error",
			))
			continue
		}
		format, ok := imageFormat(part.contentType, part.content)
		if !ok {
			problems = append(problems, FieldValidationError(
				[]any{"assets", name, "data_base64"},
				"The part is not a PNG or JPEG image",
				"value_error",
			))
			continue
		}
		asset["data_base64"] = base64.StdEncoding.EncodeToString(part.content)
		asset["format"] = format
		delete(asset, "part")
		used[partName] = struct{}{}
	}

	orphans := make([]string, 0, len(parts))
	for name := range parts {
		if _, seen := used[name]; !seen {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		problems = append(problems, FieldValidationError(
			[]any{"assets"},
			fmt.Sprintf("The part %q is not used by any asset", name),
			"value_error",
		))
	}

	if !hasAssets && len(parts) > 0 {
		return []ValidationError{
			FieldValidationError([]any{"assets"}, "Images were sent but the request declares no assets", "missing"),
		}, nil
	}
	return problems, document
}

// imageFormat answers the format the asset schema accepts for these bytes,
// detected from the leading marker rather than trusted from the part's own
// Content-Type, which a client may leave out.
func imageFormat(contentType string, content []byte) (string, bool) {
	switch {
	case len(content) >= 8 && bytes.Equal(content[:8], []byte("\x89PNG\r\n\x1a\n")):
		return "png", true
	case len(content) >= 3 && content[0] == 0xFF && content[1] == 0xD8 && content[2] == 0xFF:
		return "jpg", true
	}
	// The bytes did not identify the image, so the declared type is the last
	// word: refusing here says which part it was, where an upstream that cannot
	// decode it would answer with a location of its own.
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(contentType))
	}
	switch mediaType {
	case "image/png":
		return "png", true
	case "image/jpeg", "image/jpg":
		return "jpg", true
	}
	return "", false
}

func sortedKeys(entries map[string]any) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
