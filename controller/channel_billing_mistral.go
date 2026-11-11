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

// refreshMistralChannelBalance stores the allowance of a channel whose quota was
// already fetched by the quota refresh, so the balance does not depend on the
// channel balance sweep, which only visits enabled channels.
func refreshMistralChannelBalance(account service.MistralQuotaAccount, quota *service.MistralQuota) error {
	channels, err := model.GetAllChannels(0, 0, true, true)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if channel.Type != constant.ChannelTypeMistral || channel.Name != account.Channel {
			continue
		}
		_, err = applyMistralChannelBalance(channel, account, quota)
		return err
	}
	return common.NewMessage(fmt.Sprintf("no Mistral channel is named %q", account.Channel))
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
