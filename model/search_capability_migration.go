package model

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Legacy routing names, replaced by the capability names in constant. A search
// or fetch route now assigns one name whatever the protocol, so a channel that
// listed the old per-protocol name would no longer receive the request.
var legacyCapabilityModelNames = map[string]string{
	"deepseek-search": "tools-search",
	"moonshot-search": "tools-search",
	"deepseek-fetch":  "tools-fetch",
	"moonshot-fetch":  "tools-fetch",
}

// migrateSearchCapabilityModelNames renames the stored routing names a channel
// lists and the token model limits that name them, then rebuilds the abilities
// of every channel it touched. It is safe to run more than once: a name that is
// already current matches nothing.
func migrateSearchCapabilityModelNames(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate search capability model names: database is nil")
	}
	if !db.Migrator().HasTable(&Channel{}) ||
		!db.Migrator().HasTable(&Token{}) ||
		!db.Migrator().HasTable(&Ability{}) {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var channels []*Channel
		if err := tx.Find(&channels).Error; err != nil {
			return fmt.Errorf("list channels for the capability rename: %w", err)
		}

		for _, channel := range channels {
			renamed, changed := renameCapabilityModels(channel.GetModels())
			if !changed {
				continue
			}
			models := strings.Join(renamed, ",")
			if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Update("models", models).Error; err != nil {
				return fmt.Errorf("rename the models of channel %d: %w", channel.Id, err)
			}
			channel.Models = models
			if err := channel.UpdateAbilities(tx); err != nil {
				return fmt.Errorf("rebuild the abilities of channel %d: %w", channel.Id, err)
			}
		}

		var tokens []*Token
		if err := tx.Select("id", "model_limits").Find(&tokens).Error; err != nil {
			return fmt.Errorf("list tokens for the capability rename: %w", err)
		}
		for _, token := range tokens {
			renamed, changed := renameCapabilityModels(token.GetModelLimits())
			if !changed {
				continue
			}
			if err := tx.Model(&Token{}).Where("id = ?", token.Id).
				Update("model_limits", strings.Join(renamed, ",")).Error; err != nil {
				return fmt.Errorf("rename the model limits of token %d: %w", token.Id, err)
			}
		}
		return nil
	})
}

// renameCapabilityModels replaces every legacy routing name and drops the
// duplicates the replacement created, keeping the original order.
func renameCapabilityModels(names []string) ([]string, bool) {
	renamed := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	changed := false
	for _, name := range names {
		if current, legacy := legacyCapabilityModelNames[name]; legacy {
			name = current
			changed = true
		}
		if _, duplicate := seen[name]; duplicate {
			changed = true
			continue
		}
		seen[name] = struct{}{}
		renamed = append(renamed, name)
	}
	return renamed, changed
}
