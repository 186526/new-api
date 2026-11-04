package deepseek

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

const (
	reasoningHistoryKeyPrefix = "deepseek:reasoning:"
	reasoningHistoryTTL       = 24 * time.Hour
	// reasoningHistoryMaxEntries caps one stored history so a long agent
	// session cannot grow the cache without bound. Older turns fall back to the
	// empty value, which DeepSeek accepts.
	reasoningHistoryMaxEntries = 400
)

// DeepSeek's thinking mode rejects a continuation whose assistant history lost
// reasoning_content, and OpenAI-compatible clients do not replay that field.
// The relay stores each reply's reasoning under the conversation key and
// writes it back while the client's message history keeps the same prefix.
type reasoningHistory struct {
	// Entries holds the reasoning of assistant turns in the order they were
	// answered, so entry i belongs to the i-th assistant turn of the history.
	Entries []string `json:"entries"`
}

// conversationKey identifies one client conversation without storing its
// content: the client's prompt cache key when it sends one, otherwise the
// calling token.
func conversationKey(c *gin.Context, modelName string, promptCacheKey string) string {
	if c == nil {
		return ""
	}
	seed := strings.TrimSpace(promptCacheKey)
	if seed == "" {
		seed = strings.TrimSpace(c.GetHeader("X-Session-Id"))
	}
	if seed == "" {
		seed = common.GetContextKeyString(c, constant.ContextKeyTokenId)
	}
	if seed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(modelName + "\x00" + seed))
	return hex.EncodeToString(sum[:16])
}

func loadReasoningHistory(key string) *reasoningHistory {
	if key == "" || !common.RedisEnabled {
		return nil
	}
	raw, err := common.RedisGet(reasoningHistoryKeyPrefix + key)
	if err != nil || raw == "" {
		return nil
	}
	var history reasoningHistory
	if err := common.Unmarshal([]byte(raw), &history); err != nil {
		return nil
	}
	return &history
}

func saveReasoningHistory(key string, history *reasoningHistory) {
	if key == "" || history == nil || !common.RedisEnabled {
		return
	}
	if len(history.Entries) > reasoningHistoryMaxEntries {
		history.Entries = history.Entries[len(history.Entries)-reasoningHistoryMaxEntries:]
	}
	encoded, err := common.Marshal(history)
	if err != nil {
		return
	}
	_ = common.RedisSet(reasoningHistoryKeyPrefix+key, string(encoded), reasoningHistoryTTL)
}

// applyReasoningHistory writes stored reasoning into the assistant turns the
// client replayed. A turn the history does not cover keeps the empty value that
// makes DeepSeek accept the request.
func applyReasoningHistory(messages []dto.Message, history *reasoningHistory) {
	turn := 0
	for i := range messages {
		if messages[i].Role != "assistant" {
			continue
		}
		if history != nil && turn < len(history.Entries) && history.Entries[turn] != "" {
			reasoning := history.Entries[turn]
			messages[i].ReasoningContent = &reasoning
		} else if messages[i].ReasoningContent == nil {
			empty := ""
			messages[i].ReasoningContent = &empty
		}
		turn++
	}
}

// applyReasoningHistory stores the reasoning of the reply that is about to be
// sent, so the next request of the conversation can replay it. A reply without
// reasoning leaves the stored history unchanged.
func storeReasoningTurn(c *gin.Context, info *relaycommon.RelayInfo, reasoning string) {
	if c == nil || info == nil || info.DeepSeekReasoningKey == "" || reasoning == "" {
		return
	}
	history := &reasoningHistory{}
	if info.DeepSeekReasoningHistory != nil {
		history.Entries = append(history.Entries, info.DeepSeekReasoningHistory.Entries...)
	}
	history.Entries = append(history.Entries, reasoning)
	saveReasoningHistory(info.DeepSeekReasoningKey, history)
}
