package image_compress

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
	s "github.com/QuantumNous/new-api/setting/image_compression_setting"
	"github.com/tidwall/gjson"
)

type Download func(context.Context, string, int64) ([]byte, string, error)

type Stats struct {
	Processed int   `json:"processed"`
	Saved     int   `json:"saved_bytes"`
	Elapsed   int64 `json:"elapsed_ms"`
	Skipped   int   `json:"skipped"`
	Failed    int   `json:"failed"`
}

type imageSlot struct {
	value, media, source gjson.Result
	style                string
}

type replacement struct {
	start, end int
	value      []byte
}

type cachedImage struct {
	encoded
	err error
}

type batch struct {
	settings  s.Settings
	fetch     Download
	cache     map[[32]byte]cachedImage
	remaining int64
}

// Rewrite creates a separate JSON body for a non-passthrough attempt. A nil
// body means the original request can be reused. Call at most once per request
// and only after checking channel policy and selecting the non-passthrough path.
func Rewrite(ctx context.Context, settings s.Settings, format types.RelayFormat, raw []byte, fetch Download) ([]byte, Stats, error) {
	if !settings.Enabled {
		return nil, Stats{}, nil
	}
	if err := settings.Validate(); err != nil {
		return nil, Stats{}, err
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(settings.BudgetMS)*time.Millisecond)
	defer cancel()
	if err := workers.Acquire(ctx, 1); err != nil {
		return rewriteFailure(settings, Stats{}, err)
	}
	defer workers.Release(1)
	// Reserve the caller's body, ParseBytes' string copy, and space for JSON
	// unescaping. Staged output and codec work share the rest of the budget.
	if len(raw) > (workBudget-codecOverhead)/3 {
		return rewriteFailure(settings, Stats{}, ErrImageLimit)
	}
	processor := batch{settings: settings, fetch: fetch, cache: make(map[[32]byte]cachedImage), remaining: workBudget - 3*int64(len(raw))}
	stats := Stats{}
	var patches []replacement
	var failure error
	collectSlots(gjson.ParseBytes(raw), format, func(slot imageSlot) bool {
		if slot.remote() && !settings.CompressRemoteURLs {
			return true
		}
		if stats.Processed+stats.Skipped+stats.Failed >= settings.MaxProcessImages {
			stats.Skipped++
			if settings.FailurePolicy == "reject_request" {
				failure = ErrImageLimit
			}
			return false
		}
		result, err := processor.encode(ctx, slot)
		if err != nil {
			stats.Failed++
			if settings.FailurePolicy == "reject_request" {
				failure = err
				return false
			}
			return ctx.Err() == nil
		}
		if len(result.data) == 0 {
			stats.Skipped++
			return true
		}
		// Bound the temporary base64 and JSON copies and reserve the retained patch.
		patchBytes := int64(base64.StdEncoding.EncodedLen(len(result.data))) + 256
		if 6*patchBytes > processor.remaining {
			stats.Failed++
			if settings.FailurePolicy == "reject_request" {
				failure = ErrImageLimit
				return false
			}
			return true
		}
		processor.remaining -= patchBytes
		stats.Processed++
		stats.Saved += result.info.Saved
		patches = slot.replace(patches, result)
		return true
	})
	stats.Elapsed = time.Since(start).Milliseconds()
	if err := ctx.Err(); err != nil {
		return rewriteFailure(settings, stats, err)
	}
	if failure != nil {
		return rewriteFailure(settings, stats, failure)
	}
	if len(patches) == 0 {
		return nil, stats, nil
	}
	out, err := applyPatches(ctx, raw, patches, processor.remaining)
	stats.Elapsed = time.Since(start).Milliseconds()
	if err != nil {
		return rewriteFailure(settings, stats, err)
	}
	return out, stats, nil
}

func rewriteFailure(settings s.Settings, stats Stats, err error) ([]byte, Stats, error) {
	stats.Skipped += stats.Processed
	stats.Processed, stats.Saved = 0, 0
	if stats.Failed == 0 {
		stats.Failed = 1
	}
	if settings.FailurePolicy == "reject_request" {
		return nil, stats, err
	}
	return nil, stats, nil
}

func applyPatches(ctx context.Context, raw []byte, patches []replacement, remaining int64) ([]byte, error) {
	slices.SortFunc(patches, func(first, second replacement) int { return first.start - second.start })
	size, position := len(raw), 0
	for _, patch := range patches {
		if patch.start < position || patch.end < patch.start || patch.end > len(raw) {
			return nil, ErrImageData
		}
		size += len(patch.value) - (patch.end - patch.start)
		position = patch.end
	}
	if int64(size) > remaining {
		return nil, ErrImageLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]byte, 0, size)
	position = 0
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, raw[position:patch.start]...)
		out = append(out, patch.value...)
		position = patch.end
	}
	out = append(out, raw[position:]...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (slot imageSlot) remote() bool {
	return slot.style == "claude_url" || (slot.style == "url" && !strings.HasPrefix(slot.value.Str, "data:"))
}

func (processor *batch) encode(ctx context.Context, slot imageSlot) (encoded, error) {
	if err := ctx.Err(); err != nil {
		return encoded{}, err
	}
	value, mime := slot.value.Str, slot.media.Str
	if slot.remote() {
		key, err := imageKey(ctx, "url\x00", "", value)
		if err != nil {
			return encoded{}, err
		}
		if result, ok := processor.cache[key]; ok {
			return result.encoded, result.err
		}
		if processor.fetch == nil {
			return encoded{}, ErrRemoteImage
		}
		remoteCtx, cancel := context.WithTimeout(ctx, time.Duration(processor.settings.RemoteTimeoutMS)*time.Millisecond)
		defer cancel()
		limit := min(int64(processor.settings.RemoteMaxMB)<<20, (processor.remaining-codecOverhead)/6)
		if limit <= 0 {
			return encoded{}, ErrImageLimit
		}
		raw, mime, err := processor.fetch(remoteCtx, value, limit)
		if remoteCtx.Err() != nil {
			err = remoteCtx.Err()
		} else if int64(len(raw)) > limit || int64(cap(raw)) > 2*limit+4096 {
			err = ErrImageLimit
		}
		if err != nil {
			processor.cache[key] = cachedImage{err: err}
			return encoded{}, err
		}
		return processor.encodeBinary(ctx, key, raw, mime)
	}
	if slot.style == "url" {
		metadata, payload, ok := strings.Cut(value, ",")
		if !ok || !strings.HasSuffix(metadata, ";base64") {
			return encoded{}, ErrImageData
		}
		mime = strings.TrimSuffix(strings.TrimPrefix(metadata, "data:"), ";base64")
		value = payload
	}
	if len(value) > base64.StdEncoding.EncodedLen(MaxInputBytes) {
		return encoded{}, ErrImageLimit
	}
	// Hash the payload first so repeated inline images need no second decoding.
	key, err := imageKey(ctx, "base64\x00", mime, value)
	if err != nil {
		return encoded{}, err
	}
	if result, ok := processor.cache[key]; ok {
		return result.encoded, result.err
	}
	size := base64.StdEncoding.DecodedLen(len(value))
	if int64(size)+codecOverhead > processor.remaining {
		return encoded{}, ErrImageLimit
	}
	raw := make([]byte, size)
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(value))
	total := 0
	for total < len(raw) {
		if err := ctx.Err(); err != nil {
			return encoded{}, err
		}
		count, err := decoder.Read(raw[total:min(total+32768, len(raw))])
		total += count
		if err == io.EOF {
			break
		}
		if err != nil {
			return encoded{}, ErrImageData
		}
	}
	// Verify the entire input, including a trailing partial quartet.
	var extra [1]byte
	if count, err := decoder.Read(extra[:]); count != 0 || err != io.EOF {
		return encoded{}, ErrImageData
	}
	return processor.encodeBinary(ctx, key, raw[:total], mime)
}

func imageKey(ctx context.Context, domain, mime, value string) ([32]byte, error) {
	hash := sha256.New()
	for _, field := range []string{domain, mime, "\x00", value} {
		for len(field) > 0 {
			if err := ctx.Err(); err != nil {
				return [32]byte{}, err
			}
			block := min(len(field), 32768)
			hash.Write([]byte(field[:block]))
			field = field[block:]
		}
	}
	var key [32]byte
	hash.Sum(key[:0])
	return key, nil
}

func (processor *batch) encodeBinary(ctx context.Context, key [32]byte, raw []byte, mime string) (encoded, error) {
	result, err := compress(ctx, processor.settings, raw, mime, processor.remaining-int64(cap(raw)-len(raw)))
	processor.remaining -= int64(cap(result.data))
	processor.cache[key] = cachedImage{encoded: result, err: err}
	return result, err
}

func (slot imageSlot) replace(patches []replacement, result encoded) []replacement {
	data := base64.StdEncoding.EncodeToString(result.data)
	add := func(target gjson.Result, next any) {
		raw, _ := json.Marshal(next)
		patches = append(patches, replacement{target.Index, target.Index + len(target.Raw), raw})
	}
	switch slot.style {
	case "url":
		add(slot.value, fmt.Sprintf("data:%s;base64,%s", result.mime, data))
	case "claude_url":
		add(slot.source, map[string]string{"type": "base64", "media_type": result.mime, "data": data})
	default:
		add(slot.value, data)
		add(slot.media, result.mime)
	}
	return patches
}
