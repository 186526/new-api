package model

import "strings"

// GetModelDescriptions answers the catalog description of each named model.
// A model with no catalog row is absent from the answer rather than empty, so a
// caller can tell a missing row from an empty description. Only exact-name rows
// are read: a rule-form row describes a set of names, not one.
func GetModelDescriptions(names []string) (map[string]string, error) {
	descriptions := make(map[string]string, len(names))
	if len(names) == 0 {
		return descriptions, nil
	}
	var records []Model
	if err := DB.Select("model_name", "description").
		Where("model_name IN ? AND name_rule = ?", names, NameRuleExact).
		Find(&records).Error; err != nil {
		return nil, err
	}
	for _, record := range records {
		if strings.TrimSpace(record.Description) == "" {
			continue
		}
		descriptions[record.ModelName] = record.Description
	}
	return descriptions, nil
}
