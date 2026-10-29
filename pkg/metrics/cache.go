package metrics

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	diskCacheFilesDesc = prometheus.NewDesc(
		"newapi_cache_disk_files", "Active disk cache files.", nil, nil)
	diskCacheBytesDesc = prometheus.NewDesc(
		"newapi_cache_disk_bytes", "Current disk cache size in bytes.", nil, nil)
	memoryCacheBuffersDesc = prometheus.NewDesc(
		"newapi_cache_memory_buffers", "Active in-memory request buffers.", nil, nil)
	memoryCacheBytesDesc = prometheus.NewDesc(
		"newapi_cache_memory_bytes", "Current in-memory request buffer size in bytes.", nil, nil)
	diskCacheHitsDesc = prometheus.NewDesc(
		"newapi_cache_disk_hits_total", "Disk cache hits since start.", nil, nil)
	memoryCacheHitsDesc = prometheus.NewDesc(
		"newapi_cache_memory_hits_total", "Memory cache hits since start.", nil, nil)
	diskCacheLimitDesc = prometheus.NewDesc(
		"newapi_cache_disk_limit_bytes", "Configured disk cache size limit in bytes.", nil, nil)
	diskCacheThresholdDesc = prometheus.NewDesc(
		"newapi_cache_threshold_bytes", "Request body size in bytes above which the disk cache is used.", nil, nil)

	redisUpDesc = prometheus.NewDesc(
		"newapi_redis_up", "1 when the Redis info query succeeded.", nil, nil)
	redisHitsDesc = prometheus.NewDesc(
		"newapi_redis_keyspace_hits_total", "Redis keyspace hits since start.", nil, nil)
	redisMissesDesc = prometheus.NewDesc(
		"newapi_redis_keyspace_misses_total", "Redis keyspace misses since start.", nil, nil)
	redisMemoryDesc = prometheus.NewDesc(
		"newapi_redis_used_memory_bytes", "Redis used memory in bytes.", nil, nil)
	redisKeysDesc = prometheus.NewDesc(
		"newapi_redis_keys", "Keys in the current Redis database.", nil, nil)
	redisEvictedDesc = prometheus.NewDesc(
		"newapi_redis_evicted_keys_total", "Keys evicted by Redis since start.", nil, nil)
	redisExpiredDesc = prometheus.NewDesc(
		"newapi_redis_expired_keys_total", "Keys expired in Redis since start.", nil, nil)
)

// cacheCollector reports the in-process request body cache.
type cacheCollector struct{}

func newCacheCollector() *cacheCollector {
	return &cacheCollector{}
}

func (*cacheCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- diskCacheFilesDesc
	ch <- diskCacheBytesDesc
	ch <- memoryCacheBuffersDesc
	ch <- memoryCacheBytesDesc
	ch <- diskCacheHitsDesc
	ch <- memoryCacheHitsDesc
	ch <- diskCacheLimitDesc
	ch <- diskCacheThresholdDesc
}

func (*cacheCollector) Collect(ch chan<- prometheus.Metric) {
	stats := common.GetDiskCacheStats()
	ch <- prometheus.MustNewConstMetric(diskCacheFilesDesc, prometheus.GaugeValue, float64(stats.ActiveDiskFiles))
	ch <- prometheus.MustNewConstMetric(diskCacheBytesDesc, prometheus.GaugeValue, float64(stats.CurrentDiskUsageBytes))
	ch <- prometheus.MustNewConstMetric(memoryCacheBuffersDesc, prometheus.GaugeValue, float64(stats.ActiveMemoryBuffers))
	ch <- prometheus.MustNewConstMetric(memoryCacheBytesDesc, prometheus.GaugeValue, float64(stats.CurrentMemoryUsageBytes))
	ch <- prometheus.MustNewConstMetric(diskCacheHitsDesc, prometheus.CounterValue, float64(stats.DiskCacheHits))
	ch <- prometheus.MustNewConstMetric(memoryCacheHitsDesc, prometheus.CounterValue, float64(stats.MemoryCacheHits))
	ch <- prometheus.MustNewConstMetric(diskCacheLimitDesc, prometheus.GaugeValue, float64(stats.DiskCacheMaxBytes))
	ch <- prometheus.MustNewConstMetric(diskCacheThresholdDesc, prometheus.GaugeValue, float64(stats.DiskCacheThresholdBytes))
}

// redisCacheCollector reports Redis cache counters. Every query is bounded by
// redisScrapeTimeout so a stalled Redis cannot hold the scrape open.
type redisCacheCollector struct{}

const redisScrapeTimeout = 2 * time.Second

func (redisCacheCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- redisUpDesc
	ch <- redisHitsDesc
	ch <- redisMissesDesc
	ch <- redisMemoryDesc
	ch <- redisKeysDesc
	ch <- redisEvictedDesc
	ch <- redisExpiredDesc
}

func (redisCacheCollector) Collect(ch chan<- prometheus.Metric) {
	up := 0.0
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), redisScrapeTimeout)
		defer cancel()
		if info, err := common.RDB.Info(ctx, "stats").Result(); err == nil {
			up = 1
			values := parseRedisInfo(info)
			ch <- prometheus.MustNewConstMetric(redisHitsDesc, prometheus.CounterValue, float64(values["keyspace_hits"]))
			ch <- prometheus.MustNewConstMetric(redisMissesDesc, prometheus.CounterValue, float64(values["keyspace_misses"]))
			ch <- prometheus.MustNewConstMetric(redisMemoryDesc, prometheus.GaugeValue, float64(values["used_memory"]))
			ch <- prometheus.MustNewConstMetric(redisEvictedDesc, prometheus.CounterValue, float64(values["evicted_keys"]))
			ch <- prometheus.MustNewConstMetric(redisExpiredDesc, prometheus.CounterValue, float64(values["expired_keys"]))
		}
		if size, err := common.RDB.DBSize(ctx).Result(); err == nil {
			ch <- prometheus.MustNewConstMetric(redisKeysDesc, prometheus.GaugeValue, float64(size))
		}
	}
	ch <- prometheus.MustNewConstMetric(redisUpDesc, prometheus.GaugeValue, up)
}

// parseRedisInfo reads the "field:value" lines of one INFO section and ignores
// the comment lines and non-numeric fields.
func parseRedisInfo(info string) map[string]int64 {
	values := make(map[string]int64)
	for line := range strings.SplitSeq(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
			values[key] = value
		}
	}
	return values
}
