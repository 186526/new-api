package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/image_compress"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func DownloadCompressionImage(ctx context.Context, target string, limit int64) ([]byte, string, error) {
	settings := system_setting.GetFetchSetting()
	if !settings.EnableSSRFProtection || !settings.DomainFilterMode || len(settings.DomainList) == 0 {
		return nil, "", image_compress.ErrRemoteImage
	}
	protection, err := common.NewSSRFProtectionFromFetchSetting(false, true, settings.IpFilterMode, settings.DomainList, settings.IpList, settings.AllowedPorts, true)
	if err != nil {
		return nil, "", image_compress.ErrRemoteImage
	}
	check := func(target *url.URL) error {
		if target.Scheme != "https" || target.User != nil || target.Hostname() == "" || net.ParseIP(target.Hostname()) != nil {
			return image_compress.ErrRemoteImage
		}
		port := 443
		if target.Port() != "" {
			port, _ = strconv.Atoi(target.Port())
		}
		return protection.ValidateNetworkTarget(target.Hostname(), port)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil || check(req.URL) != nil {
		return nil, "", image_compress.ErrRemoteImage
	}
	dialer := protectedFetchDialer{
		resolver:      net.DefaultResolver,
		dialContext:   (&net.Dialer{}).DialContext,
		getProtection: func() (*common.SSRFProtection, bool, error) { return protection, true, nil },
	}
	transport := &http.Transport{DialContext: dialer.DialContext, DisableCompression: true, DisableKeepAlives: true}
	client := &http.Client{Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return image_compress.ErrRemoteImage
		}
		next.Header.Del("Referer")
		return check(next.URL)
	}}
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, "", err
		}
		return nil, "", image_compress.ErrRemoteImage
	}
	defer resp.Body.Close()
	mime := common.MediaTypeOf(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !strings.HasPrefix(mime, "image/") {
		return nil, "", image_compress.ErrRemoteImage
	}
	if limit <= 0 || limit > image_compress.MaxInputBytes || resp.ContentLength > limit {
		return nil, "", image_compress.ErrImageLimit
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", image_compress.ErrRemoteImage
	}
	if int64(len(raw)) > limit {
		return nil, "", image_compress.ErrImageLimit
	}
	return raw, mime, nil
}
