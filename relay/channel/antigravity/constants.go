package antigravity

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/antigravity/modelname"
)

const ChannelName = "antigravity"

// ModelList 是 Antigravity 账号可用的模型名，即以基础名暴露的列表，加上搜索能力名：
// Antigravity 的搜索由模型驱动，所以这个渠道能服务搜索路由；它没有抓取端点，
// 抓取路由不会分发到这个渠道。
var ModelList = append(modelname.List(), constant.ToolsSearchModel)
