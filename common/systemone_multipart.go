package common

import (
	"github.com/QuantumNous/new-api/relaykit/systemone"
	"github.com/gin-gonic/gin"
)

const keySystemOneMultipart = "key_systemone_multipart"

type SystemOneMultipartRequest struct {
	Model string `json:"model"`
	Body  []byte `json:"-"`
}

func ReadSystemOneMultipart(ctx *gin.Context) (SystemOneMultipartRequest, []systemone.ValidationError, error) {
	if cached, ok := ctx.Get(keySystemOneMultipart); ok {
		return cached.(SystemOneMultipartRequest), nil, nil
	}
	storage, err := GetBodyStorage(ctx)
	if err != nil {
		return SystemOneMultipartRequest{}, nil, err
	}
	if storage.Size() > systemone.MaxRequestBodyBytes {
		return SystemOneMultipartRequest{}, nil, ErrRequestBodyTooLarge
	}
	raw, err := storage.Bytes()
	if err != nil {
		return SystemOneMultipartRequest{}, nil, err
	}
	body, problems := systemone.MultipartBody(ctx.GetHeader("Content-Type"), raw)
	if len(problems) > 0 {
		return SystemOneMultipartRequest{}, problems, nil
	}
	if len(body) > systemone.MaxRequestBodyBytes {
		return SystemOneMultipartRequest{}, nil, ErrRequestBodyTooLarge
	}

	request := SystemOneMultipartRequest{Body: body}
	if err := Unmarshal(body, &request); err != nil {
		return SystemOneMultipartRequest{}, []systemone.ValidationError{
			systemone.FieldValidationError([]any{"model"}, "Input should be a valid string", "string_type"),
		}, nil
	}
	if request.Model == "" {
		return SystemOneMultipartRequest{}, []systemone.ValidationError{
			systemone.FieldValidationError([]any{"model"}, "Model is required", "missing"),
		}, nil
	}
	ctx.Set(keySystemOneMultipart, request)
	return request, nil, nil
}
