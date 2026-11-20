// Package modelname 维护 Antigravity 上游模型名与 reasoning 档位的对应关系。
// 上游把档位编码在模型 id 的后缀里（例如 gemini-4-argon-high），对外只暴露基础名：
// 请求时按客户端的 effort 还原后缀，拉取模型列表时再把后缀折回基础名。
// 表的内容来自 fetchAvailableModels 的返回，逐个模型记录它实际提供的档位。
package modelname

import (
	"slices"
	"strings"
)

// 上游档位后缀。LevelXHigh 只出现在部分模型的上游 id 里，effortLevels 不产出它：
// Resolve 不会请求 xhigh，Collapse 把带该后缀的 id 折回基础名。
const (
	LevelLow    = "low"
	LevelMedium = "medium"
	LevelHigh   = "high"
	LevelXHigh  = "xhigh"
)

// Model 描述一个上游模型。
// Levels 是上游提供的档位，Resolve 与 Collapse 都只认这些后缀；
// Levels 为空表示该模型名不按档位区分。
type Model struct {
	Name         string
	DefaultLevel string
	Levels       []string
}

var models = []Model{
	{Name: "claude-opus-5-5", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh}},
	{Name: "claude-sonnet-5-5", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh}},
	{Name: "gemini-2.5-flash"},
	{Name: "gemini-2.5-flash-lite"},
	{Name: "gemini-3-flash"},
	{Name: "gemini-3.1-flash-image"},
	{Name: "gemini-3.1-flash-lite"},
	{Name: "gemini-3.5-flash-lite"},
	{Name: "gemini-3.8-flash", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh}},
	{Name: "gemini-3.8-flash-tiered"},
	{Name: "gemini-4-argon", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh}},
	{Name: "gemini-4-hydrogen", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh}},
	{Name: "gemini-4-hydrogen-sonar"},
	{Name: "gemini-4-hydrogen-tiered", DefaultLevel: LevelHigh, Levels: []string{LevelHigh}},
	{Name: "gemini-4-neon", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh}},
	{Name: "gpt-oss-120b", DefaultLevel: LevelMedium, Levels: []string{LevelMedium}},
}

// effortLevels 把客户端的 effort 收敛到上游的档位。
// 模型没有收敛后的这一档时不再降级：Resolve 会改用该模型的缺省档位。
var effortLevels = map[string]string{
	"none":    LevelLow,
	"minimal": LevelLow,
	"low":     LevelLow,
	"medium":  LevelMedium,
	"high":    LevelHigh,
	"xhigh":   LevelHigh,
	"max":     LevelHigh,
}

// List 返回对外暴露的模型名，即上表中的基础名。
func List() []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Name)
	}
	return names
}

// Resolve 把基础名与客户端 effort 还原成上游模型 id。
// effort 不在 effortLevels 里、或该模型没有这一档时，用该模型的缺省档位。
// 不在上表里的名字（包括带后缀的旧名字）原样返回。
func Resolve(name, effort string) string {
	model, ok := lookup(name)
	if !ok || len(model.Levels) == 0 {
		return name
	}
	level, known := effortLevels[strings.ToLower(strings.TrimSpace(effort))]
	if !known || !slices.Contains(model.Levels, level) {
		level = model.DefaultLevel
	}
	return name + "-" + level
}

// Collapse 把上游带档位后缀的模型名折回基础名，只折叠上表该模型声明过的档位；
// 其他名字原样返回。
func Collapse(name string) string {
	for _, model := range models {
		for _, level := range model.Levels {
			if name == model.Name+"-"+level {
				return model.Name
			}
		}
	}
	return name
}

func lookup(name string) (Model, bool) {
	for _, model := range models {
		if model.Name == name {
			return model, true
		}
	}
	return Model{}, false
}
