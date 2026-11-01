package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"

	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

// Per-region quota leases.
//
// A relay instance never touches the central balance directly. The admin cuts
// a lease out of a subject's central balance and hands it to one region; the
// region spends only from that lease and reports the net spend back. The admin
// guarantees that the sum of all outstanding leases never exceeds the central
// balance, so regions cannot jointly overdraw an account.
//
// Region-side state lives in one hash per subject:
//
//	Available    quota the region may still spend
//	TotalSpent   monotonic net spend (pre-consume adds, refunds subtract)
//	LastReported TotalSpent at the last successful report
//	Granted      lease total the region has been granted
//	Epoch        lease generation, bumped when the admin claws the lease back
//
// The hash must not expire while TotalSpent differs from LastReported, or the
// unreported spend would be lost with the key.

const (
	RegionSubjectUser  = "user"
	RegionSubjectToken = "token"
)

var ErrRegionLeaseUnavailable = errors.New("region quota lease unavailable")

// regionLeaseRequest pulls a fresh lease from the admin. It is installed by
// the service layer, which owns the HTTP client, to keep model free of relay
// dependencies.
var regionLeaseRequest func(subjectType string, subjectID string) error

// SetRegionLeaseRequest installs the on-demand lease fetcher.
func SetRegionLeaseRequest(fn func(subjectType string, subjectID string) error) {
	regionLeaseRequest = fn
}

// regionLeaseRequestLocks coalesces concurrent top-up requests per subject.
// Callers that lose the race wait for the in-flight request instead of
// failing, because the winner's grant is exactly the quota they need.
var regionLeaseRequestLocks sync.Map

func regionSubjectKey(region string, subjectType string, subjectID string) string {
	return fmt.Sprintf("region:%s:%s:%s", region, subjectType, subjectID)
}

func regionSubjectSeqKey(region string, subjectType string, subjectID string) string {
	return regionSubjectKey(region, subjectType, subjectID) + ":seq"
}

func poolRegionKey(region string, subjectType string, subjectID string) string {
	return fmt.Sprintf("pool:region:%s:%s:%s", region, subjectType, subjectID)
}

func poolOutstandingKey(subjectType string, subjectID string) string {
	return fmt.Sprintf("pool:%s:%s:outstanding", subjectType, subjectID)
}

// RegionConfigured reports whether this process serves the region endpoints.
// The admin answers lease requests and folds reported spend back; a relay
// instance spends from its lease.
func RegionConfigured() bool {
	return common.RegionLeaseEnabled && common.Region != "" && common.RedisEnabled
}

// RegionEnabled reports whether this process spends from region leases, which
// only a relay instance does. The admin must keep changing the central balance
// directly: a top-up there is not a regional spend.
func RegionEnabled() bool {
	return common.RunsRelay() && RegionConfigured()
}

func regionTokenSubjectID(key string) string {
	return common.GenerateHMAC(key)
}

const regionReserveScript = `
if redis.call('HEXISTS', KEYS[1], 'Available') == 0 then
  return -1
end
local available = tonumber(redis.call('HGET', KEYS[1], 'Available'))
if available == nil or available < tonumber(ARGV[1]) then
  return 0
end
redis.call('HINCRBY', KEYS[1], 'Available', -tonumber(ARGV[1]))
redis.call('HINCRBY', KEYS[1], 'TotalSpent', tonumber(ARGV[1]))
redis.call('SADD', KEYS[2], ARGV[2])
return 1`

// regionSpendScript applies a net spend delta. A positive delta is a charge
// that may not exceed the remaining lease; a negative delta returns quota to
// the lease and is always allowed.
const regionSpendScript = `
if redis.call('HEXISTS', KEYS[1], 'Available') == 0 then
  return -1
end
local delta = tonumber(ARGV[1])
if delta > 0 then
  local available = tonumber(redis.call('HGET', KEYS[1], 'Available'))
  if available == nil or available < delta then
    return 0
  end
  redis.call('HINCRBY', KEYS[1], 'Available', -delta)
end
redis.call('HINCRBY', KEYS[1], 'TotalSpent', delta)
redis.call('SADD', KEYS[2], ARGV[2])
redis.call('SADD', KEYS[3], ARGV[2])
return 1`

// regionReportScript snapshots the unreported spend under a fresh sequence
// number. The sequence lives in its own key so that rebuilding the balance
// hash cannot rewind it.
const regionReportScript = `
if redis.call('HEXISTS', KEYS[1], 'TotalSpent') == 0 then
  return {-1, 0, 0}
end
local total = tonumber(redis.call('HGET', KEYS[1], 'TotalSpent'))
local reported = tonumber(redis.call('HGET', KEYS[1], 'LastReported') or '0')
local delta = total - reported
if delta == 0 then
  return {0, 0, 0}
end
local seq = redis.call('INCR', KEYS[2])
redis.call('HSET', KEYS[1], 'LastReported', total)
return {1, delta, seq}`

const regionAckScript = `
if redis.call('HEXISTS', KEYS[1], 'TotalSpent') == 0 then
  return 0
end
local total = tonumber(redis.call('HGET', KEYS[1], 'TotalSpent'))
local reported = tonumber(redis.call('HGET', KEYS[1], 'LastReported') or '0')
if total == reported then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return 1`

// regionHydrateScript installs a freshly computed lease only when the hash is
// absent: a live hash already accounts for spend made after the snapshot was
// taken. Unreported spend keeps the hash alive without a TTL.
const regionHydrateScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then
  return 0
end
redis.call('HSET', KEYS[1], 'Available', ARGV[1], 'TotalSpent', 0, 'LastReported', 0,
  'Granted', ARGV[2], 'Epoch', ARGV[3])
if ARGV[4] ~= '0' then
  redis.call('EXPIRE', KEYS[1], ARGV[4])
end
return 1`

const regionRevokeScript = `
local epoch = tonumber(redis.call('HGET', KEYS[1], 'Epoch') or '0')
if epoch >= tonumber(ARGV[1]) then
  return 0
end
local granted = tonumber(redis.call('HGET', KEYS[1], 'Granted') or '0')
local reported = tonumber(redis.call('HGET', KEYS[1], 'LastReported') or '0')
local returned = granted - reported
redis.call('HSET', KEYS[1], 'Available', 0, 'Granted', reported, 'Epoch', ARGV[1])
return returned`

func cacheRegionReserve(subjectKey string, amount int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), regionReserveScript, []string{subjectKey}, amount).Int()
	return quotaResultFromLua(result, err)
}

func cacheRegionSpend(subjectKey string, delta int64, member string) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), regionSpendScript,
		[]string{subjectKey, regionDirtyKey(common.Region), regionActiveKey(common.Region)}, delta, member).Int()
	return quotaResultFromLua(result, err)
}

func regionDirtyKey(region string) string {
	return fmt.Sprintf("region:%s:dirty", region)
}

func regionActiveKey(region string) string {
	return fmt.Sprintf("region:%s:active", region)
}

func regionPendingKey(region string) string {
	return fmt.Sprintf("region:%s:pending", region)
}

func regionMember(subjectType string, subjectID string) string {
	return subjectType + ":" + subjectID
}

// RegionSubjects returns the subjects that spent quota in this region, so the
// region can report what it owes and the dashboard can aggregate its leases.
func RegionSubjects(region string) ([][2]string, error) {
	return regionSubjectSet(regionActiveKey(region))
}

// RegionDirtySubjects returns the subjects whose lease needs a top-up. A grant
// writes the member here; spending alone does not.
func RegionDirtySubjects(region string) ([][2]string, error) {
	return regionSubjectSet(regionDirtyKey(region))
}

func regionSubjectSet(key string) ([][2]string, error) {
	members, err := common.RDB.SMembers(context.Background(), key).Result()
	if err != nil {
		return nil, err
	}
	return splitRegionMembers(members), nil
}

// regionPoolSubjects unions the active and dirty sets: a subject that only
// holds a lease has no spend to report but still counts towards the region's
// available and outstanding totals.
func regionPoolSubjects(region string) ([][2]string, error) {
	ctx := context.Background()
	active, err := common.RDB.SMembers(ctx, regionActiveKey(region)).Result()
	if err != nil {
		return nil, err
	}
	dirty, err := common.RDB.SMembers(ctx, regionDirtyKey(region)).Result()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(active)+len(dirty))
	members := make([]string, 0, len(active)+len(dirty))
	for _, set := range [][]string{active, dirty} {
		for _, member := range set {
			if _, ok := seen[member]; ok {
				continue
			}
			seen[member] = struct{}{}
			members = append(members, member)
		}
	}
	return splitRegionMembers(members), nil
}

func splitRegionMembers(members []string) [][2]string {
	subjects := make([][2]string, 0, len(members))
	for _, member := range members {
		subjectType, subjectID, ok := strings.Cut(member, ":")
		if !ok {
			continue
		}
		subjects = append(subjects, [2]string{subjectType, subjectID})
	}
	return subjects
}

// regionLeaseAvailable returns the quota a region may still spend for a
// subject, hydrating the region hash from its lease when it is absent.
func regionLeaseAvailable(subjectType string, subjectID string) (int, error) {
	subjectKey := regionSubjectKey(common.Region, subjectType, subjectID)
	available, err := common.RDB.HGet(context.Background(), subjectKey, "Available").Int()
	if err == nil {
		return available, nil
	}
	if !errors.Is(err, redis.Nil) {
		return 0, err
	}
	granted, reported, epoch, err := readRegionLease(subjectType, subjectID)
	if err != nil {
		return 0, err
	}
	available = max(0, granted-reported)
	ttl := strconv.Itoa(userCacheTTLSeconds())
	if _, err := common.RDB.Eval(context.Background(), regionHydrateScript, []string{subjectKey},
		available, granted, epoch, ttl).Int(); err != nil {
		return 0, err
	}
	return available, nil
}

// readRegionLease reads the lease the admin holds for this region.
func readRegionLease(subjectType string, subjectID string) (granted int, reported int, epoch int64, err error) {
	values, err := common.RDB.HGetAll(context.Background(), poolRegionKey(common.Region, subjectType, subjectID)).Result()
	if err != nil {
		return 0, 0, 0, err
	}
	granted, _ = strconv.Atoi(values["Granted"])
	reported, _ = strconv.Atoi(values["Reported"])
	epoch, _ = strconv.ParseInt(values["Epoch"], 10, 64)
	return granted, reported, epoch, nil
}

// spendRegionLease charges the region lease. A positive delta needs enough
// remaining lease; a negative delta returns quota.
func spendRegionLease(subjectType string, subjectID string, delta int64) error {
	if delta == 0 {
		return nil
	}
	subjectKey := regionSubjectKey(common.Region, subjectType, subjectID)
	member := regionMember(subjectType, subjectID)
	result, err := cacheRegionSpend(subjectKey, delta, member)
	if err != nil {
		return err
	}
	if result == cacheQuotaMiss {
		if _, err := regionLeaseAvailable(subjectType, subjectID); err != nil {
			return err
		}
		if result, err = cacheRegionSpend(subjectKey, delta, member); err != nil {
			return err
		}
	}
	if result == cacheQuotaInsufficient && delta > 0 {
		appmetrics.ObserveRegionLeaseExhausted(common.Region, subjectType)
		if err := requestRegionLease(subjectType, subjectID); err != nil {
			return err
		}
		result, err = cacheRegionSpend(subjectKey, delta, member)
		if err != nil {
			return err
		}
	}
	if result != cacheQuotaOK {
		return ErrRegionLeaseUnavailable
	}
	return nil
}

// requestRegionLease asks the admin for more quota. Concurrent requests for the
// same subject share one round trip.
func requestRegionLease(subjectType string, subjectID string) error {
	if regionLeaseRequest == nil {
		return ErrRegionLeaseUnavailable
	}
	key := subjectType + ":" + subjectID
	done := make(chan struct{})
	if actual, loaded := regionLeaseRequestLocks.LoadOrStore(key, done); loaded {
		<-actual.(chan struct{})
		return nil
	}
	defer func() {
		regionLeaseRequestLocks.Delete(key)
		close(done)
	}()
	return regionLeaseRequest(subjectType, subjectID)
}

// RegionReport is one batch of spend a region owes the admin.
type RegionReport struct {
	Region      string
	SubjectType string
	SubjectID   string
	Seq         int64
	Delta       int64
}

func NextRegionReport(region string, subjectType string, subjectID string) (*RegionReport, error) {
	values, err := common.RDB.Eval(context.Background(), regionReportScript,
		[]string{regionSubjectKey(region, subjectType, subjectID), regionSubjectSeqKey(region, subjectType, subjectID)}).Slice()
	if err != nil {
		return nil, err
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("unexpected region report reply: %v", values)
	}
	status, _ := values[0].(int64)
	if status != 1 {
		return nil, nil
	}
	delta, _ := values[1].(int64)
	seq, _ := values[2].(int64)
	return &RegionReport{Region: region, SubjectType: subjectType, SubjectID: subjectID, Seq: seq, Delta: delta}, nil
}

// ackRegionReport restores the idle TTL once every delta has been reported.
func ackRegionReport(region string, subjectType string, subjectID string) {
	if _, err := common.RDB.Eval(context.Background(), regionAckScript,
		[]string{regionSubjectKey(region, subjectType, subjectID)}, userCacheTTLSeconds()).Int(); err != nil {
		common.SysLog(common.LogText("failed to refresh region lease TTL: %s", err.Error()))
	}
}

// RegionQuotaEvent is the durable record of one reported batch. The unique key
// makes a retried report idempotent.
type RegionQuotaEvent struct {
	Id          int    `json:"id" gorm:"primaryKey"`
	Region      string `json:"region" gorm:"size:64;uniqueIndex:idx_region_event,priority:1"`
	SubjectType string `json:"subject_type" gorm:"size:16;uniqueIndex:idx_region_event,priority:2"`
	SubjectID   string `json:"subject_id" gorm:"size:128;uniqueIndex:idx_region_event,priority:3"`
	Seq         int64  `json:"seq" gorm:"uniqueIndex:idx_region_event,priority:4"`
	Delta       int64  `json:"delta"`
	CreatedAt   int64  `json:"created_at" gorm:"index"`
}

func (RegionQuotaEvent) TableName() string {
	return "region_quota_events"
}

// ReportRegionBatch reports one batch to the admin: the event lands in the
// database first, then the central lease's reported total moves. A crash
// between the two only makes the next grant read a stale reported total, which
// the periodic reconciliation repairs; a retried batch is ignored.
func ReportRegionBatch(region string, subjectType string, subjectID string, seq int64, delta int64) error {
	report := &RegionReport{Region: region, SubjectType: subjectType, SubjectID: subjectID, Seq: seq, Delta: delta}
	if err := persistRegionReport(report); err != nil {
		return err
	}
	appmetrics.ObserveRegionReport(region, subjectType, delta)
	return common.RDB.HIncrBy(context.Background(),
		poolRegionKey(region, subjectType, subjectID), "Reported", delta).Err()
}

// RecordRegionReport stores a reported batch and moves the central lease's
// reported total. A duplicate batch is ignored.
func RecordRegionReport(report *RegionReport) error {
	err := DB.Create(&RegionQuotaEvent{
		Region:      report.Region,
		SubjectType: report.SubjectType,
		SubjectID:   report.SubjectID,
		Seq:         report.Seq,
		Delta:       report.Delta,
		CreatedAt:   common.GetTimestamp(),
	}).Error
	if err != nil && !isDuplicateKey(err) {
		return err
	}
	return nil
}

func persistRegionReport(report *RegionReport) error {
	err := DB.Create(&RegionQuotaEvent{
		Region:      report.Region,
		SubjectType: report.SubjectType,
		SubjectID:   report.SubjectID,
		Seq:         report.Seq,
		Delta:       report.Delta,
		CreatedAt:   common.GetTimestamp(),
	}).Error
	if err != nil && !isDuplicateKey(err) {
		return err
	}
	return nil
}

// isDuplicateKey reports whether a unique index rejected the insert. GORM is
// opened without TranslateError, so the driver error is matched by text.
func isDuplicateKey(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "unique failed")
}

// ApplyRegionEvents folds unreported events into the central balance. Events
// are deleted once applied, so the ledger and the balance move together.
func ApplyRegionEvents(limit int) (int, error) {
	var events []RegionQuotaEvent
	if err := DB.Order("id asc").Limit(limit).Find(&events).Error; err != nil {
		return 0, err
	}
	applied := 0
	for _, event := range events {
		if err := applyRegionEvent(event); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

func applyRegionEvent(event RegionQuotaEvent) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&RegionQuotaEvent{}, event.Id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if event.Delta == 0 {
			return nil
		}
		switch event.SubjectType {
		case RegionSubjectUser:
			id, err := strconv.Atoi(event.SubjectID)
			if err != nil {
				return err
			}
			return tx.Model(&User{}).Where("id = ?", id).
				Update("quota", gorm.Expr("quota - ?", event.Delta)).Error
		}
		return nil
	})
}

// revokeRegionLease zeroes a region's remaining lease and returns the unused
// part so the admin can hand it to another region or back to the balance.
func revokeRegionLease(region string, subjectType string, subjectID string, epoch int64) (int64, error) {
	returned, err := common.RDB.Eval(context.Background(), regionRevokeScript,
		[]string{regionSubjectKey(region, subjectType, subjectID)}, epoch).Int64()
	if err != nil {
		return 0, err
	}
	if returned == 0 {
		return 0, nil
	}
	if err := common.RDB.HIncrBy(context.Background(), poolOutstandingKey(subjectType, subjectID), "Total", -returned).Err(); err != nil {
		return 0, err
	}
	return returned, nil
}

// grantLeaseScript raises the subject's outstanding total only when the new
// lease still fits under the central balance. Doing the check and the
// increment in one script keeps two admins from granting the same free quota.
const grantLeaseScript = `
local outstanding = tonumber(redis.call('HGET', KEYS[1], 'Total') or '0')
local balance = tonumber(ARGV[1])
local amount = tonumber(ARGV[2])
local free = balance - outstanding
if free <= 0 then
  return 0
end
if amount > free then
  amount = free
end
redis.call('HINCRBY', KEYS[1], 'Total', amount)
return amount`

// GrantRegionLease moves quota from a subject's central balance into a
// region's lease. It refuses to grant more than the central balance minus
// every outstanding lease, so regions cannot jointly overdraw the account.
func GrantRegionLease(region string, subjectType string, subjectID string, amount int) (int, error) {
	if amount <= 0 {
		return 0, nil
	}
	balance, err := regionSubjectCentralBalance(subjectType, subjectID)
	if err != nil {
		return 0, err
	}
	ctx := context.Background()
	outstandingKey := poolOutstandingKey(subjectType, subjectID)
	granted, err := common.RDB.Eval(ctx, grantLeaseScript, []string{outstandingKey}, balance, amount).Int()
	if err != nil {
		return 0, err
	}
	if granted <= 0 {
		return 0, nil
	}
	appmetrics.ObserveRegionLeaseGrant(region, subjectType, granted)
	// Track the subject and the region so the admin can top the lease up again.
	if err := common.RDB.SAdd(ctx, regionDirtyKey(region), regionMember(subjectType, subjectID)).Err(); err != nil {
		common.RDB.HIncrBy(ctx, outstandingKey, "Total", -int64(granted))
		return 0, err
	}
	if err := RegisterRegion(region); err != nil {
		common.RDB.HIncrBy(ctx, outstandingKey, "Total", -int64(granted))
		return 0, err
	}
	leaseKey := poolRegionKey(region, subjectType, subjectID)
	if err := common.RDB.HIncrBy(ctx, leaseKey, "Granted", int64(granted)).Err(); err != nil {
		common.RDB.HIncrBy(ctx, outstandingKey, "Total", -int64(granted))
		return 0, err
	}
	if err := common.RDB.HIncrBy(ctx, regionSubjectKey(region, subjectType, subjectID), "Available", int64(granted)).Err(); err != nil {
		common.RDB.HIncrBy(ctx, outstandingKey, "Total", -int64(granted))
		common.RDB.HIncrBy(ctx, leaseKey, "Granted", -int64(granted))
		return 0, err
	}
	return granted, nil
}

func regionSubjectCentralBalance(subjectType string, subjectID string) (int, error) {
	switch subjectType {
	case RegionSubjectUser:
		id, err := strconv.Atoi(subjectID)
		if err != nil {
			return 0, err
		}
		var quota int
		err = DB.Model(&User{}).Where("id = ?", id).Select("quota").Scan(&quota).Error
		return quota, err
	}
	return 0, fmt.Errorf("unknown region subject type %q", subjectType)
}

// SavePendingRegionReport stores a batch whose delivery is not acknowledged
// yet, so a restart cannot lose it.
func SavePendingRegionReport(report *RegionReport) error {
	value := fmt.Sprintf("%d|%d", report.Seq, report.Delta)
	return common.RDB.HSet(context.Background(), regionPendingKey(report.Region),
		regionMember(report.SubjectType, report.SubjectID), value).Err()
}

// PendingRegionReports lists batches still owed to the admin.
func PendingRegionReports(region string) ([]RegionReport, error) {
	entries, err := common.RDB.HGetAll(context.Background(), regionPendingKey(region)).Result()
	if err != nil {
		return nil, err
	}
	reports := make([]RegionReport, 0, len(entries))
	for member, value := range entries {
		subjectType, subjectID, ok := strings.Cut(member, ":")
		if !ok {
			continue
		}
		seqText, deltaText, ok := strings.Cut(value, "|")
		if !ok {
			continue
		}
		seq, seqErr := strconv.ParseInt(seqText, 10, 64)
		delta, deltaErr := strconv.ParseInt(deltaText, 10, 64)
		if seqErr != nil || deltaErr != nil {
			continue
		}
		reports = append(reports, RegionReport{
			Region: region, SubjectType: subjectType, SubjectID: subjectID, Seq: seq, Delta: delta,
		})
	}
	return reports, nil
}

// ClearPendingRegionReport drops a batch the admin has accepted.
func ClearPendingRegionReport(region string, subjectType string, subjectID string) error {
	return common.RDB.HDel(context.Background(), regionPendingKey(region), regionMember(subjectType, subjectID)).Err()
}

// RegionAvailable reports the remaining lease a region holds for a subject.
func RegionAvailable(region string, subjectType string, subjectID string) (int, error) {
	available, err := common.RDB.HGet(context.Background(), regionSubjectKey(region, subjectType, subjectID), "Available").Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return available, err
}

// RegisterRegion records a region so the admin keeps its leases topped up.
func RegisterRegion(region string) error {
	return common.RDB.SAdd(context.Background(), "pool:regions", region).Err()
}

// KnownRegions lists the regions the admin has handed leases to.
func KnownRegions() ([]string, error) {
	return common.RDB.SMembers(context.Background(), "pool:regions").Result()
}

// RegionPoolSnapshot summarises one region's leases for the metrics endpoint.
type RegionPoolSnapshot struct {
	Available   int
	Outstanding int
	Unreported  int
}

func RegionPoolStats(region string) (map[string]RegionPoolSnapshot, error) {
	stats := map[string]RegionPoolSnapshot{
		RegionSubjectUser: {},
	}
	subjects, err := regionPoolSubjects(region)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	for _, subject := range subjects {
		subjectType, subjectID := subject[0], subject[1]
		values, err := common.RDB.HGetAll(ctx, regionSubjectKey(region, subjectType, subjectID)).Result()
		if err != nil {
			return nil, err
		}
		available, _ := strconv.Atoi(values["Available"])
		total, _ := strconv.Atoi(values["TotalSpent"])
		reported, _ := strconv.Atoi(values["LastReported"])
		entry := stats[subjectType]
		entry.Available += available
		entry.Unreported += total - reported
		lease, err := common.RDB.HGetAll(ctx, poolRegionKey(region, subjectType, subjectID)).Result()
		if err != nil {
			return nil, err
		}
		granted, _ := strconv.Atoi(lease["Granted"])
		leaseReported, _ := strconv.Atoi(lease["Reported"])
		entry.Outstanding += granted - leaseReported
		stats[subjectType] = entry
	}
	return stats, nil
}
