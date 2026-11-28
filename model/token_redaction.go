package model

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrTokenRedactionConflict = errors.New("request redaction policy has changed")

// UpdateRedactionPolicy only writes policy fields; batch edits must not replace
// quota or other metadata with a snapshot read before a concurrent update.
func (token *Token) UpdateRedactionPolicy() error {
	return token.updateRedactionPolicy(false)
}

func (token *Token) updateRedactionPolicy(includeTokenFields bool) error {
	if token.Id <= 0 || token.UserId <= 0 || token.RedactionRevision < 0 {
		return ErrTokenRedactionConflict
	}
	if err := invalidateTokenRedactionCache(token.Key); err != nil {
		return errors.New("could not invalidate token policy cache")
	}
	fields := map[string]any{
		"redaction_enabled":  token.RedactionEnabled,
		"redaction_config":   token.RedactionConfig,
		"redaction_revision": gorm.Expr("redaction_revision + 1"),
	}
	if includeTokenFields {
		fields["name"], fields["status"] = token.Name, token.Status
		fields["expired_time"], fields["remain_quota"] = token.ExpiredTime, token.RemainQuota
		fields["unlimited_quota"] = token.UnlimitedQuota
		fields["model_limits_enabled"], fields["model_limits"] = token.ModelLimitsEnabled, token.ModelLimits
		fields["allow_ips"], fields["group"] = token.AllowIps, token.Group
		fields["cross_group_retry"], fields["auto_groups"] = token.CrossGroupRetry, token.AutoGroups
		fields["allow_user_quota_query"] = token.AllowUserQuotaQuery
	}
	result := DB.Model(&Token{}).Where("id = ? AND user_id = ? AND redaction_revision = ?",
		token.Id, token.UserId, token.RedactionRevision).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTokenRedactionConflict
	}
	token.RedactionRevision++
	return nil
}

func invalidateTokenRedactionCache(key string) error {
	if !common.RedisEnabled || key == "" {
		return nil
	}
	const script = `redis.call('SET', KEYS[2], '1', 'EX', ARGV[1]); return redis.call('HDEL', KEYS[1], 'CacheMAC')`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return common.RDB.Eval(ctx, script, []string{getTokenCacheKey(key), getTokenCacheFenceKey(key)}, strconv.Itoa(tokenCacheFenceSeconds)).Err()
}

// GetTokenRedaction reads only policy metadata and leaves signed quota state untouched.
func GetTokenRedaction(id int) (Token, error) {
	var policy Token
	err := DB.Model(&Token{}).Select("id", "redaction_enabled", "redaction_config").Where("id = ?", id).Take(&policy).Error
	return policy, err
}

func redactionCipher() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(common.GenerateHMAC("token-redaction-config:v1")))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (token Token) redactionAssociatedData() []byte {
	return []byte(common.GenerateHMAC("token-redaction-owner:v1:" + token.Key))
}

func (token Token) RedactionConfigBytes() ([]byte, error) {
	if token.RedactionConfig == "" {
		return nil, nil
	}
	data, err := base64.RawStdEncoding.DecodeString(token.RedactionConfig)
	if err != nil || len(data) > 25<<10 {
		return nil, errors.New("invalid encrypted token policy")
	}
	cipher, err := redactionCipher()
	if err != nil {
		return nil, err
	}
	return cipher.Open(nil, nil, data, token.redactionAssociatedData())
}

func (token *Token) StoreRedactionConfig(data []byte) error {
	cipher, err := redactionCipher()
	if err != nil {
		return err
	}
	token.RedactionConfig = base64.RawStdEncoding.EncodeToString(cipher.Seal(nil, nil, data, token.redactionAssociatedData()))
	return nil
}

func (token Token) RedactionPolicyVersion() [32]byte {
	return sha256.Sum256([]byte(strconv.FormatBool(token.RedactionEnabled) + ":" + token.RedactionConfig))
}

func (token Token) RedactionFingerprintKey() []byte {
	return []byte(common.GenerateHMAC("request-redaction:v1:" + strconv.Itoa(token.Id)))
}
