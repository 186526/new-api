package controller

import (
	"net/http"
	"sort"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetRequestPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"options": model.CurrentRequestPolicy().Options}})
}

func UpdateRequestPolicy(c *gin.Context) {
	var request struct {
		Options map[string]string `json:"options"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	previous := model.CurrentRequestPolicy().Options
	if err := model.UpdateRequestPolicyOptions(request.Options); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	current := model.CurrentRequestPolicy().Options
	var changed []string
	for key := range request.Options {
		if previous[key] != current[key] {
			changed = append(changed, key)
		}
	}
	if len(changed) != 0 {
		sort.Strings(changed)
		recordManageAudit(c, "request_policy.update", map[string]any{"keys": changed})
	}
	GetRequestPolicy(c)
}
