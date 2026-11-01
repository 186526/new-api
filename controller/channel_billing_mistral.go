package controller

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

// updateChannelMistralVibeBalance reports what is left of the Vibe Code
// allowance as the channel balance. The percentage lives in the Vibe web app
// and not behind the API key, so the account is matched by channel name and the
// account entry carries the plan amount that turns the percentage into dollars.
func updateChannelMistralVibeBalance(channel *model.Channel) (float64, error) {
	account, err := findMistralVibeAccount(channel.Name)
	if err != nil {
		return 0, err
	}
	quota, err := service.FetchMistralVibeQuota(context.Background(), account)
	if err != nil {
		return 0, err
	}
	balance := account.MonthlyLimitUSD * (1 - quota.UsagePercentage/100)
	channel.UpdateBalance(balance)
	return balance, nil
}

func findMistralVibeAccount(name string) (service.MistralVibeAccount, error) {
	accounts, err := service.LoadMistralVibeAccounts()
	if err != nil {
		return service.MistralVibeAccount{}, err
	}
	for _, account := range accounts {
		if account.Channel != name {
			continue
		}
		if account.MonthlyLimitUSD <= 0 {
			return service.MistralVibeAccount{}, common.NewMessage(fmt.Sprintf(
				"Mistral Vibe account %q has no monthlyLimitUsd", name))
		}
		return account, nil
	}
	return service.MistralVibeAccount{}, common.NewMessage(fmt.Sprintf(
		"no Mistral Vibe account is configured for channel %q", name))
}
