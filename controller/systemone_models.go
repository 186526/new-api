package controller

import (
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/systemone"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// ListSystemOneModels answers GET /systemone/v1/models with the document the
// upstream declares: {"models":[{name,description,release_date}]}, naming only
// enabled decision models in the caller's groups that the token permits. This
// gateway writes an empty release date because it keeps no release dates.
func ListSystemOneModels(c *gin.Context) {
	groups, err := getModelListGroups(c)
	if err != nil {
		common.WriteTypeSafeError(c, http.StatusInternalServerError, "failed to load models")
		return
	}
	names := FilterSystemOneModels(service.GetGroupsEnabledModels(groups.ownerGroups))
	if limit, enabled := tokenModelLimitForList(c); enabled {
		names = slices.DeleteFunc(names, func(name string) bool {
			return !middleware.TokenModelLimitAllows(limit, name)
		})
	}

	// A catalog the description read cannot answer leaves the entries without a
	// description; failing the whole listing would hide the models a caller can
	// actually reach.
	descriptions, err := model.GetModelDescriptions(names)
	if err != nil {
		descriptions = map[string]string{}
	}
	models := make([]systemone.ModelMetadata, 0, len(names))
	for _, name := range names {
		models = append(models, systemone.ModelMetadata{
			Name:        name,
			Description: descriptions[name],
		})
	}
	c.JSON(http.StatusOK, systemone.NewModelMetadataList(models))
}

// FilterSystemOneModels keeps the decision models of a model list, in the order
// the list already has, so the answer is stable across calls.
func FilterSystemOneModels(names []string) []string {
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, constant.SystemOneModelPrefix) {
			kept = append(kept, name)
		}
	}
	return kept
}

// SystemOneModelNotFound answers GET /systemone/v1/models/:name. The upstream
// publishes no single-model endpoint, so the answer is the contract-shaped 404 a
// generated client already handles, not a list entry.
func SystemOneModelNotFound(c *gin.Context) {
	common.WriteTypeSafeError(c, http.StatusNotFound, "Not Found")
}
