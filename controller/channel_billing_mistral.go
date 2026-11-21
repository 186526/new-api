package controller

import (
	"context"
	"fmt"

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

// updateChannelMistralBalance reports what is left of the allowance the channel
// consumes as the channel balance. The percentages live in the admin console and
// not behind the API key, so the account is matched by channel name; when the
// account carries both keys, the channel key picks the allowance, because a
// Studio key draws from the API budget and a Vibe plan key from the Vibe budget.
func updateChannelMistralBalance(channel *model.Channel) (float64, error) {
	account, err := findMistralQuotaAccount(channel.Name)
	if err != nil {
		return 0, err
	}
	quota, err := service.FetchMistralQuota(context.Background(), account)
	if err != nil {
		return 0, err
	}
	return applyMistralChannelBalance(channel, account, quota)
}

// storeMistralChannelBalance stores the allowance of a channel whose quota was
// already fetched by the quota refresh, so the balance does not depend on the
// channel balance sweep, which only visits enabled channels. A nil channel is an
// account without a channel: its allowance is still published as the upstream
// quota, but there is no channel balance to store.
func storeMistralChannelBalance(channel *model.Channel, account service.MistralQuotaAccount, quota *service.MistralQuota) error {
	if channel == nil {
		return nil
	}
	_, err := applyMistralChannelBalance(channel, account, quota)
	return err
}

// applyMistralChannelBalance picks the allowance the channel key consumes and
// stores the remaining dollars as the channel balance.
func applyMistralChannelBalance(channel *model.Channel, account service.MistralQuotaAccount, quota *service.MistralQuota) (float64, error) {
	budget := quota.Budget(account.BudgetScopeForKey(channel.Key))
	if budget == nil {
		// An unlisted key keeps reporting the Vibe allowance, which is what a
		// plan key consumed before the account carried both keys.
		budget = quota.Vibe
	}
	if budget == nil {
		budget = quota.API
	}
	if budget == nil {
		return 0, common.NewMessage(fmt.Sprintf(
			"Mistral quota account %q reported no monthly allowance", channel.Name))
	}
	if budget.LimitUSD <= 0 {
		return 0, common.NewMessage(fmt.Sprintf(
			"Mistral quota account %q has no plan amount for the %s allowance", channel.Name, budget.Scope))
	}
	balance := budget.LimitUSD * (1 - budget.UsagePercentage/100)
	channel.UpdateBalance(balance)
	appmetrics.ObserveChannelBalanceUSD(channel.Name, channel.Id, balance)
	return balance, nil
}

// findMistralQuotaAccount returns the account configured for a channel name. The
// plan amount is left as configured, so a caller that only needs the percentage
// still gets the account when the limits are missing.
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
