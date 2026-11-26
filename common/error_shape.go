package common

import "github.com/gin-gonic/gin"

// ErrorShape is the document family an error response is written in. A route
// group picks the shape before anything can fail, and every writer that answers
// a failed request reads it. The zero value is the OpenAI one, which keeps every
// route that never sets it unchanged.
type ErrorShape string

const (
	// ErrorShapeOpenAI writes {"error":{"message","type","code"}}.
	ErrorShapeOpenAI ErrorShape = ""
	// ErrorShapeTypeSafe writes {"detail": ...}: an array of validation errors
	// for a rejected request body, an object for every other failure.
	ErrorShapeTypeSafe ErrorShape = "typesafe"
)

const keyErrorShape = "key_error_shape"

// SetErrorShape marks the request as answering in the given shape. It runs in a
// middleware, before authentication, so a rejected key is written in the shape
// the caller expects.
func SetErrorShape(c *gin.Context, shape ErrorShape) {
	if c == nil {
		return
	}
	c.Set(keyErrorShape, shape)
}

// ErrorShapeOf answers the shape the request answers in. A request that never
// set one answers in the OpenAI shape.
func ErrorShapeOf(c *gin.Context) ErrorShape {
	if c == nil {
		return ErrorShapeOpenAI
	}
	if value, exists := c.Get(keyErrorShape); exists {
		if shape, ok := value.(ErrorShape); ok {
			return shape
		}
	}
	return ErrorShapeOpenAI
}

// AnswersInTypeSafeShape reports whether the request answers errors in the
// TypeSafe document family.
func AnswersInTypeSafeShape(c *gin.Context) bool {
	return ErrorShapeOf(c) == ErrorShapeTypeSafe
}
