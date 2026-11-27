package antigravity

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/antigravity/modelname"
)

const ChannelName = "antigravity"

// ModelList 是 Antigravity 账号可用的模型名，即以基础名暴露的列表，加上搜索与抓取两个能力名。
// 搜索由 Antigravity 的模型驱动；抓取它没有上游端点，但管道里 local 抓取永远是一个候选，
// 不需要渠道凭证，所以这个渠道同样能承载抓取路由。两个名字都要出现在这里，
// 否则运营在后台的模型选择器里勾不到，能力就无从开启。
var ModelList = append(modelname.List(), constant.ToolsSearchModel, constant.ToolsFetchModel)
