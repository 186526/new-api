// Package modelname 维护 Antigravity 上游模型名与 reasoning 档位的对应关系。
// 上游把档位编码在模型 id 的后缀里（例如 gemini-4-argon-high），对外只暴露基础名：
// 请求时按客户端的 effort 还原后缀，拉取模型列表时再把后缀折回基础名。
// 表的内容来自 fetchAvailableModels 的返回，逐个模型记录它实际提供的档位。
package modelname

import (
	"fmt"
	"slices"
	"strings"
)

// 上游档位后缀。只有部分模型提供 xhigh，见 models 表。
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
	{Name: "gemini-4-hydrogen", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh, LevelXHigh}},
	{Name: "gemini-4-hydrogen-sonar"},
	{Name: "gemini-4-hydrogen-tiered", DefaultLevel: LevelHigh, Levels: []string{LevelHigh, LevelXHigh}},
	{Name: "gemini-4-neon", DefaultLevel: LevelMedium, Levels: []string{LevelLow, LevelMedium, LevelHigh, LevelXHigh}},
	{Name: "gpt-oss-120b", DefaultLevel: LevelMedium, Levels: []string{LevelMedium}},
}

// effortLevels 把客户端的 effort 换算成档位。xhigh 与 max 都表示最高档：
// 模型声明了 xhigh 就请求 xhigh，否则退回 high。
var effortLevels = map[string]string{
	"none":    LevelLow,
	"minimal": LevelLow,
	"low":     LevelLow,
	"medium":  LevelMedium,
	"high":    LevelHigh,
	"xhigh":   LevelXHigh,
	"max":     LevelXHigh,
}

// xhighFallback 是模型没有声明 xhigh 时用的档位。
const xhighFallback = LevelHigh

func init() {
	for _, model := range models {
		if len(model.Levels) == 0 {
			continue
		}
		if !slices.Contains(model.Levels, model.DefaultLevel) {
			panic(fmt.Sprintf("modelname: %s declares levels %v but defaults to %q", model.Name, model.Levels, model.DefaultLevel))
		}
	}
}

// levelFor 把 effort 换算成该模型声明的档位。
// 认不出的 effort 和模型没声明的档位都用缺省档位（init 保证它在 Levels 里）；
// 只有 xhigh 特殊，没声明时退回 high。
func (model Model) levelFor(effort string) string {
	level, known := effortLevels[strings.ToLower(strings.TrimSpace(effort))]
	if !known {
		return model.DefaultLevel
	}
	if slices.Contains(model.Levels, level) {
		return level
	}
	if level == LevelXHigh && slices.Contains(model.Levels, xhighFallback) {
		return xhighFallback
	}
	return model.DefaultLevel
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
// 返回的档位一定在该模型声明的 Levels 里；不带档位的名字原样返回。
// 不在上表里的名字（包括带后缀的旧名字）原样返回。
func Resolve(name, effort string) string {
	model, ok := lookup(name)
	if !ok || len(model.Levels) == 0 {
		return name
	}
	return name + "-" + model.levelFor(effort)
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
