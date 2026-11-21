package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/service"
)

// mistralChannelIndex answers which Mistral channel carries the name of a
// configured quota account. A refresh reads the channel table once per tick and
// resolves every account against that read, so the accounts of one tick cannot
// disagree about which channels exist.
type mistralChannelIndex struct {
	byName map[string]*model.Channel
	names  []string
	// readErr is the failure to read the channel table. Every account of the
	// tick then stays labelled as configured: an unreadable table says nothing
	// about a channel being gone, and hiding a live channel is worse than
	// showing an orphan until the next tick reads the table again.
	readErr error
}

func loadMistralChannelIndex() mistralChannelIndex {
	index := mistralChannelIndex{byName: make(map[string]*model.Channel)}
	channels, err := model.GetAllChannels(0, 0, true, true)
	if err != nil {
		index.readErr = err
		return index
	}
	for _, channel := range channels {
		if channel.Type != constant.ChannelTypeMistral {
			continue
		}
		index.names = append(index.names, channel.Name)
		if _, exists := index.byName[channel.Name]; !exists {
			index.byName[channel.Name] = channel
		}
	}
	return index
}

// resolve returns the channel that carries the account name together with
// whether the account's allowance has lost its channel. The second value is the
// channel_configured label: false only when the table was read and no channel
// carries the name.
func (i mistralChannelIndex) resolve(name string) (*model.Channel, bool) {
	if i.readErr != nil {
		return nil, false
	}
	channel, ok := i.byName[name]
	return channel, !ok
}

// updateChannelMistralBalance reports what is left of the account's merged
// monthly allowance as the channel balance. Mistral bills Vibe and Studio from
// one allowance, so every channel of the account draws on the same figure and
// the key the channel carries no longer selects a side. The allowance is read
// with the channel's own key, so a key left behind in the accounts file cannot
// decide the balance of a channel that was rotated since.
func updateChannelMistralBalance(channel *model.Channel) (float64, error) {
	account, err := findMistralQuotaAccount(channel.Name)
	if err != nil {
		return 0, err
	}
	account.ChannelKey = mistralChannelKey(channel)
	quota, err := service.FetchMistralQuota(context.Background(), account)
	if err != nil {
		return 0, err
	}
	return applyMistralChannelBalance(channel, quota)
}

// mistralChannelKey returns the key a channel calls the upstream with, or an
// empty string for a channel that carries none. A Mistral channel holds one key,
// so the first key of a multi-line value is the one it uses.
func mistralChannelKey(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return ""
	}
	return strings.TrimSpace(keys[0])
}

// storeMistralChannelBalance stores the allowance of a channel whose quota was
// already fetched by the quota refresh, so the balance does not wait for the
// channel balance sweep, which reads a disabled channel only when it disabled it
// for its balance. A nil channel is an account without a channel: its allowance
// is still published as the upstream quota, but there is no channel balance to
// store.
func storeMistralChannelBalance(channel *model.Channel, quota *service.MistralQuota) error {
	if channel == nil {
		return nil
	}
	_, err := applyMistralChannelBalance(channel, quota)
	return err
}

// applyMistralChannelBalance stores what is left of the merged allowance as the
// channel balance.
func applyMistralChannelBalance(channel *model.Channel, quota *service.MistralQuota) (float64, error) {
	budget := quota.Allowance
	if budget == nil {
		return 0, common.NewMessage(fmt.Sprintf(
			"Mistral quota account %q reported no monthly allowance", channel.Name))
	}
	if budget.LimitUSD <= 0 {
		return 0, common.NewMessage(fmt.Sprintf(
			"Mistral quota account %q has no plan amount for its monthly allowance", channel.Name))
	}
	balance := budget.LimitUSD * (1 - budget.UsagePercentage/100)
	channel.UpdateBalance(balance)
	appmetrics.ObserveChannelBalanceUSD(channel.Name, channel.Id, balance)
	return balance, nil
}

// findMistralQuotaAccount returns the account configured for a channel name.
func findMistralQuotaAccount(name string) (service.MistralQuotaAccount, error) {
	accounts, err := service.LoadMistralQuotaAccounts()
	if err != nil {
		return service.MistralQuotaAccount{}, err
	}
	for _, account := range accounts {
		if account.Channel == name {
			return account, nil
		}
	}
	return service.MistralQuotaAccount{}, common.NewMessage(fmt.Sprintf(
		"no Mistral quota account is configured for channel %q", name))
}
