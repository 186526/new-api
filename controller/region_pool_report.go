package controller

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// NewRegionReportV2Handler binds each V2 credential to a single region. Do not register it until the durable writers and mixed-version cutover are in use.
func NewRegionReportV2Handler(tokensByRegion map[string]string) (gin.HandlerFunc, error) {
	tokens := make(map[string]string, len(tokensByRegion))
	seen := make(map[string]bool, len(tokensByRegion))
	for region, token := range tokensByRegion {
		if region == "" || !service.ValidRegionReportV2Token(token) || seen[token] {
			return nil, errors.New("invalid regional V2 report credentials")
		}
		tokens[region], seen[token] = token, true
	}
	if len(tokens) == 0 {
		return nil, errors.New("missing regional V2 report credentials")
	}
	return func(c *gin.Context) {
		provided := c.Request.Header.Values(service.RegionReportV2TokenHeader)
		if len(provided) != 1 || !service.ValidRegionReportV2Token(provided[0]) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		report, err := decodeRegionReportV2(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid regional report"})
			return
		}
		expected := tokens[report.Region]
		if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided[0])) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		reportRegionSpendV2(c, report)
	}, nil
}

func decodeRegionReportV2(c *gin.Context) (model.RegionReportV2, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var report model.RegionReportV2
	if err := decoder.Decode(&report); err != nil {
		return report, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return report, model.ErrInvalidRegionReport
	}
	return report, report.Validate()
}

func reportRegionSpendV2(c *gin.Context, report model.RegionReportV2) {
	receipt, err := model.ApplyRegionReport(c.Request.Context(), report)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, receipt)
	case errors.Is(err, model.ErrInvalidRegionReport):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid regional report"})
	case errors.Is(err, model.ErrRegionReportConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "regional report payload conflict"})
	case errors.Is(err, model.ErrRegionReportNotReady):
		c.JSON(http.StatusConflict, gin.H{"error": "regional report not ready"})
	default:
		common.SysLog(common.LogText("failed to apply regional report: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to apply regional report"})
	}
}
