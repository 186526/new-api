package service

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const RegionReportV2Path = "/api/internal/region/report/v2"
const RegionReportV2TokenHeader = "X-Region-V2-Token"
const regionReceiptMaxBytes = 4096

var regionReportClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func ValidRegionReportV2Token(token string) bool {
	return len(token) >= 32 && !strings.ContainsAny(token, " \t\r\n") &&
		subtle.ConstantTimeCompare([]byte(token), []byte(RegionInternalToken())) != 1
}

type RegionReportSenderV2 struct {
	BaseURL string
	Region  string
	Token   string
}

func (sender RegionReportSenderV2) endpoint() (string, error) {
	address, err := url.Parse(sender.BaseURL)
	if err != nil || address.Scheme != "https" || address.Host == "" || address.User != nil || address.RawQuery != "" || address.Fragment != "" ||
		(address.Path != "" && address.Path != "/") || !ValidRegionReportV2Token(sender.Token) {
		return "", errors.New("invalid regional V2 report sender configuration")
	}
	return address.ResolveReference(&url.URL{Path: RegionReportV2Path}).String(), nil
}

func (sender RegionReportSenderV2) deliver(ctx context.Context, endpoint string, report model.RegionReportV2) (model.RegionReportReceipt, error) {
	if err := report.Validate(); err != nil {
		return model.RegionReportReceipt{}, err
	}
	if report.Region != sender.Region {
		return model.RegionReportReceipt{}, model.ErrInvalidRegionReport
	}
	body, err := json.Marshal(report)
	if err != nil {
		return model.RegionReportReceipt{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return model.RegionReportReceipt{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(RegionReportV2TokenHeader, sender.Token)
	response, err := regionReportClient.Do(request)
	if err != nil {
		return model.RegionReportReceipt{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return model.RegionReportReceipt{}, fmt.Errorf("regional report admin replied %d", response.StatusCode)
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, regionReceiptMaxBytes+1))
	if err != nil {
		return model.RegionReportReceipt{}, err
	}
	if len(reply) > regionReceiptMaxBytes {
		return model.RegionReportReceipt{}, errors.New("regional report receipt exceeds size limit")
	}
	return decodeRegionReportReceipt(reply, report)
}

func (sender RegionReportSenderV2) deliverPending(ctx context.Context, db *gorm.DB, endpoint string) error {
	pending, err := model.ClaimRegionReportsV2(ctx, db, sender.Region, 8)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range pending {
		if err := ctx.Err(); err != nil {
			return errors.Join(errors.Join(failures...), err)
		}
		receipt, err := sender.deliver(ctx, endpoint, entry.RegionReportV2)
		if err == nil {
			err = model.AcknowledgeRegionReportV2(ctx, db, receipt)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("deliver regional report row %d: %w", entry.ID, err))
		}
	}
	return errors.Join(failures...)
}

// Run returns when canceled. Start it only after mixed-version cutover and join it before closing the database.
func (sender RegionReportSenderV2) Run(ctx context.Context, db *gorm.DB) error {
	endpoint, err := sender.endpoint()
	if err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := sender.deliverPending(ctx, db, endpoint); err != nil && ctx.Err() == nil {
			common.SysError(fmt.Sprintf("regional V2 outbox: %v", err))
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return ctx.Err()
}

func decodeRegionReportReceipt(response []byte, report model.RegionReportV2) (model.RegionReportReceipt, error) {
	var receipt model.RegionReportReceipt
	if err := json.Unmarshal(response, &receipt); err != nil {
		return model.RegionReportReceipt{}, fmt.Errorf("decode regional report receipt: %w", err)
	}
	if receipt.RegionReportV2 != report || receipt.AppliedAt <= 0 {
		return model.RegionReportReceipt{}, errors.New("admin did not return a matching applied regional report receipt")
	}
	return receipt, nil
}
