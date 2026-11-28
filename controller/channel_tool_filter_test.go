package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSearchChannelsMatchesWholeToolModel(t *testing.T) {
	db := modelManagementDB(t, "sqlite", "")
	require.NoError(t, db.Create(&[]model.Channel{
		{Id: 1, Name: "both", Models: "chat, tools-search ,tools-fetch", Type: 25, Tag: new("shared")},
		{Id: 2, Name: "suffix", Models: "tools-search-v2,tools-fetch-v2", Type: 27, Tag: new("shared")},
		{Id: 3, Name: "search", Models: "tools-search", Type: 27, Tag: new("search")},
		{Id: 4, Name: "case", Models: "TOOLS-SEARCH", Type: 27, Tag: new("shared")},
	}).Error)

	for _, tc := range []struct {
		query string
		ids   []int
		types map[string]int
	}{
		{"model=tools-search", []int{1, 3}, map[string]int{"25": 1, "27": 1}},
		{"model=tools-search&tag_mode=true", []int{1, 3}, map[string]int{"25": 1, "27": 1}},
		{"model=tools-fetch", []int{1}, map[string]int{"25": 1}},
		{"model=tools-fetch&tag_mode=true", []int{1}, map[string]int{"25": 1}},
		{"model=search-v2", []int{2}, map[string]int{"27": 1}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					Items      []model.Channel `json:"items"`
					Total      int             `json:"total"`
					TypeCounts map[string]int  `json:"type_counts"`
				} `json:"data"`
			}
			recorder := modelManagementRequest(t, SearchChannels, http.MethodGet, "/api/channel/search?"+tc.query, nil, &response)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.True(t, response.Success)
			ids := make([]int, 0, len(response.Data.Items))
			for _, channel := range response.Data.Items {
				ids = append(ids, channel.Id)
			}
			require.ElementsMatch(t, tc.ids, ids)
			require.Equal(t, len(tc.ids), response.Data.Total)
			require.Equal(t, tc.types, response.Data.TypeCounts)
		})
	}
}
