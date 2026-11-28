package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetQuotaReconciliation(c *gin.Context) {
	var identity model.QuotaReviewIdentity
	if err := c.ShouldBindQuery(&identity); err != nil {
		quotaReviewError(c, model.ErrQuotaReviewInvalid)
		return
	}
	review, err := model.GetQuotaReview(c.Request.Context(), identity)
	if err != nil {
		quotaReviewError(c, err)
		return
	}
	common.ApiSuccess(c, review)
}

func ConfirmQuotaReconciliation(c *gin.Context) {
	var submission model.QuotaReviewSubmission
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	if err := c.ShouldBindJSON(&submission); err != nil {
		quotaReviewError(c, model.ErrQuotaReviewInvalid)
		return
	}
	if err := model.ConfirmQuotaReview(c.Request.Context(), submission, c.GetInt("id")); err != nil {
		quotaReviewError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func quotaReviewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrQuotaReviewInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
	case errors.Is(err, model.ErrQuotaReviewPending), errors.Is(err, model.ErrQuotaReviewRecorded):
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
	default:
		common.SysError("quota review: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Quota review unavailable"})
	}
}
