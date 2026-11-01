package controller

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// RegionLeaseRequest asks the admin for more quota for one subject.
type RegionLeaseRequest struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
}

// RegionReportRequest reports one batch of spend a region owes the admin.
type RegionReportRequest struct {
	Region      string `json:"region"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Seq         int64  `json:"seq"`
	Delta       int64  `json:"delta"`
}

// RegionInternalAuth guards the endpoints relay instances call. They are not
// reachable through the public ingress, and the shared token is required even
// inside the cluster.
func RegionInternalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := service.RegionInternalToken()
		provided := c.GetHeader("X-Region-Token")
		if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func validRegionSubject(subjectType string, subjectID string) bool {
	if strings.TrimSpace(subjectID) == "" {
		return false
	}
	return subjectType == model.RegionSubjectUser
}

// RequestRegionLease grants a region a chunk of a subject's central balance.
func RequestRegionLease(c *gin.Context) {
	var request RegionLeaseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validRegionSubject(request.SubjectType, request.SubjectID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subject"})
		return
	}
	region := strings.TrimSpace(os.Getenv("REGION"))
	if region == "" {
		region = c.GetHeader("X-Region")
	}
	if region == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "region is required"})
		return
	}
	granted, err := service.GrantRegionLeaseTopUp(region, request.SubjectType, request.SubjectID)
	if err != nil {
		common.SysLog(common.LogText("failed to grant region lease: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to grant lease"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"granted": granted})
}

// ReportRegionSpend records spend a region reports and moves its lease.
func ReportRegionSpend(c *gin.Context) {
	var request RegionReportRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validRegionSubject(request.SubjectType, request.SubjectID) || request.Region == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subject"})
		return
	}
	if err := model.ReportRegionBatch(request.Region, request.SubjectType, request.SubjectID, request.Seq, request.Delta); err != nil {
		common.SysLog(common.LogText("failed to record region report: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record report"})
		return
	}
	c.Status(http.StatusNoContent)
}
