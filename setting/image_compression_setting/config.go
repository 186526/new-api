package image_compression_setting

import (
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/setting/config"
)

const Prefix = "image_compression_setting."

type Settings struct {
	Enabled              bool   `json:"enabled"`
	MinBytesKB           int    `json:"min_bytes_kb"`
	MinDimension         int    `json:"min_dimension"`
	MaxDimension         int    `json:"max_dimension"`
	TargetFormat         string `json:"target_format"`
	JPEGQuality          int    `json:"jpeg_quality"`
	Resampler            string `json:"resampler"`
	SkipAnimatedAndSVG   bool   `json:"skip_animated_and_svg"`
	CompressRemoteURLs   bool   `json:"compress_remote_urls"`
	RemoteTimeoutMS      int    `json:"remote_timeout_ms"`
	RemoteMaxMB          int    `json:"remote_max_download_mb"`
	MaxProcessImages     int    `json:"max_process_images"`
	BudgetMS             int    `json:"per_request_budget_ms"`
	FailurePolicy        string `json:"failure_policy"`
	ChannelIDs           string `json:"channel_ids"`
	ModelPatterns        string `json:"model_patterns"`
	ExcludeChannelIDs    string `json:"exclude_channel_ids"`
	ExcludeModelPatterns string `json:"exclude_model_patterns"`
	ThresholdEnabled     bool   `json:"threshold_enabled"`
}

func Defaults() Settings {
	return Settings{
		ThresholdEnabled: true, MinBytesKB: 512, MinDimension: 2048, MaxDimension: 2048,
		TargetFormat: "auto_keep_alpha", JPEGQuality: 82, Resampler: "catmull_rom",
		SkipAnimatedAndSVG: true, RemoteTimeoutMS: 3000, RemoteMaxMB: 15,
		MaxProcessImages: 12, BudgetMS: 4000, FailurePolicy: "keep_original",
	}
}

var registered = Defaults()
var snapshot atomic.Pointer[Settings]

func init() {
	config.GlobalConfig.Register(strings.TrimSuffix(Prefix, "."), &registered)
	snapshot.Store(new(Defaults()))
}

func Get() Settings { return *snapshot.Load() }

// DecodeOptions validates a complete option snapshot, overlaying only keys in
// this namespace on the defaults. Use it before committing an Option mutation.
func DecodeOptions(options map[string]string) (Settings, error) {
	settings := Defaults()
	fields := reflect.ValueOf(&settings).Elem()
	typ := fields.Type()
	for index := range fields.NumField() {
		field := fields.Field(index)
		name := typ.Field(index).Tag.Get("json")
		value, exists := options[Prefix+name]
		if !exists {
			continue
		}
		switch field.Kind() {
		case reflect.String:
			field.SetString(value)
		case reflect.Int:
			number, err := strconv.Atoi(value)
			if err != nil {
				return Settings{}, fmt.Errorf("%s must be an integer", name)
			}
			field.SetInt(int64(number))
		case reflect.Bool:
			if value != "true" && value != "false" {
				return Settings{}, fmt.Errorf("%s must be true or false", name)
			}
			field.SetBool(value == "true")
		}
	}
	return settings, settings.Validate()
}

func Publish(settings Settings) { snapshot.Store(&settings) }

func DecodeDraft(raw []byte) (Settings, error) {
	settings := Defaults()
	if err := json.Unmarshal(raw, &settings); err != nil {
		return Settings{}, fmt.Errorf("invalid image compression settings")
	}
	return settings, settings.Validate()
}

func (settings Settings) Validate() error {
	for _, limit := range []struct {
		name            string
		value, min, max int
	}{
		{"min_bytes_kb", settings.MinBytesKB, 16, 51200},
		{"min_dimension", settings.MinDimension, 256, 16384},
		{"max_dimension", settings.MaxDimension, 256, 8192},
		{"jpeg_quality", settings.JPEGQuality, 30, 95},
		{"remote_timeout_ms", settings.RemoteTimeoutMS, 500, 15000},
		{"remote_max_download_mb", settings.RemoteMaxMB, 1, 50},
		{"max_process_images", settings.MaxProcessImages, 1, 64},
		{"per_request_budget_ms", settings.BudgetMS, 500, 20000},
	} {
		if limit.value < limit.min || limit.value > limit.max {
			return fmt.Errorf("%s must be between %d and %d", limit.name, limit.min, limit.max)
		}
	}
	if settings.ThresholdEnabled && settings.MaxDimension > settings.MinDimension {
		return fmt.Errorf("max_dimension cannot exceed min_dimension")
	}
	switch settings.TargetFormat {
	case "auto_keep_alpha", "jpeg", "png", "keep_original":
	default:
		return fmt.Errorf("invalid target_format")
	}
	if settings.Resampler != "catmull_rom" && settings.Resampler != "bilinear" {
		return fmt.Errorf("invalid resampler")
	}
	if settings.FailurePolicy != "keep_original" && settings.FailurePolicy != "reject_request" {
		return fmt.Errorf("invalid failure_policy")
	}
	return settings.validateScope()
}

func (settings Settings) validateScope() error {
	for _, scope := range []struct{ name, value string }{
		{"channel_ids", settings.ChannelIDs}, {"exclude_channel_ids", settings.ExcludeChannelIDs},
	} {
		if len(scope.value) > 2048 {
			return fmt.Errorf("%s is too long", scope.name)
		}
		for _, channel := range list(scope.value) {
			id, err := strconv.Atoi(channel)
			if err != nil || id < 1 {
				return fmt.Errorf("%s must contain positive integers", scope.name)
			}
		}
	}
	for _, scope := range []struct{ name, value string }{
		{"model_patterns", settings.ModelPatterns}, {"exclude_model_patterns", settings.ExcludeModelPatterns},
	} {
		if len(scope.value) > 4096 {
			return fmt.Errorf("%s is too long", scope.name)
		}
		for _, pattern := range list(scope.value) {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("invalid %s glob", scope.name)
			}
		}
	}
	return nil
}

func (settings Settings) Applies(channelID int, models ...string) bool {
	if !settings.Enabled || matchesChannel(list(settings.ExcludeChannelIDs), channelID) || matchesModel(list(settings.ExcludeModelPatterns), models) {
		return false
	}
	if settings.ChannelIDs != "" && !matchesChannel(list(settings.ChannelIDs), channelID) {
		return false
	}
	patterns := list(settings.ModelPatterns)
	return len(patterns) == 0 || matchesModel(patterns, models)
}

func matchesChannel(channels []string, channelID int) bool {
	for _, channel := range channels {
		if parsed, _ := strconv.Atoi(channel); parsed == channelID {
			return true
		}
	}
	return false
}

func matchesModel(patterns, models []string) bool {
	for _, model := range models {
		for _, pattern := range patterns {
			if matched, _ := path.Match(pattern, model); matched {
				return true
			}
		}
	}
	return false
}

func list(value string) []string {
	return strings.FieldsFunc(value, func(char rune) bool {
		return char == ',' || char == '\r' || char == '\n' || char == ' '
	})
}
