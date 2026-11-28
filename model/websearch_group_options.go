package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrWebSearchGroupPolicyConflict = errors.New("web search group policies changed; refresh before saving")

type WebSearchGroupOptions struct {
	Value   string `json:"value"`
	Version string `json:"version"`
}

func webSearchGroupOptions(raw string, exists bool) WebSearchGroupOptions {
	if !exists {
		return WebSearchGroupOptions{Value: "{}", Version: "absent"}
	}
	hash := sha256.Sum256([]byte(raw))
	return WebSearchGroupOptions{Value: raw, Version: hex.EncodeToString(hash[:])}
}

func readWebSearchGroupOptions(db *gorm.DB) (WebSearchGroupOptions, error) {
	var row Option
	err := db.Take(&row, clause.Eq{Column: "key", Value: websearch_setting.GroupPoliciesOptionKey}).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return webSearchGroupOptions("", false), nil
	}
	if err != nil {
		return WebSearchGroupOptions{}, err
	}
	return webSearchGroupOptions(row.Value, true), nil
}

func ReadWebSearchGroupOptions(ctx context.Context) (WebSearchGroupOptions, error) {
	return readWebSearchGroupOptions(DB.WithContext(ctx))
}

func CompareAndSwapWebSearchGroupOptions(ctx context.Context, expected, value string) (WebSearchGroupOptions, error) {
	if err := websearch_setting.ValidateGroupPolicies(value); err != nil {
		return WebSearchGroupOptions{}, err
	}
	requestPolicyOptionMutex.Lock()
	defer requestPolicyOptionMutex.Unlock()
	db := DB.WithContext(ctx)
	current, err := readWebSearchGroupOptions(db)
	if err != nil {
		return WebSearchGroupOptions{}, err
	}
	if expected != current.Version {
		return current, ErrWebSearchGroupPolicyConflict
	}
	key := websearch_setting.GroupPoliciesOptionKey
	var result *gorm.DB
	if expected == "absent" {
		result = db.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: key, Value: value})
	} else if current.Value != value {
		result = db.Model(&Option{}).Where(clause.Eq{Column: "key", Value: key}).
			Where(clause.Eq{Column: "value", Value: current.Value}).UpdateColumn("value", value)
	}
	if result != nil {
		if result.Error != nil {
			return WebSearchGroupOptions{}, result.Error
		}
		if result.RowsAffected != 1 {
			latest, err := readWebSearchGroupOptions(db)
			if err != nil {
				return WebSearchGroupOptions{}, err
			}
			return latest, ErrWebSearchGroupPolicyConflict
		}
	}
	if err := applyWebSearchOptions(map[string]string{key: value}); err != nil {
		return WebSearchGroupOptions{}, err
	}
	return webSearchGroupOptions(value, true), nil
}
