package model

import (
	"errors"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRegistrationQuotaTransaction(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		mode            string
		failAfterInsert bool
		wantError       error
	}{
		{name: "commit", mode: QuotaWriteSQL},
		{name: "legacy insert", mode: QuotaWriteLegacy},
		{name: "OAuth binding failure", mode: QuotaWriteSQL, failAfterInsert: true},
		{name: "draining rejects registration", mode: QuotaWriteDraining, wantError: ErrQuotaWriteProtocol},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, db.AutoMigrate(&User{}, &QuotaWriteProtocol{}, &NonRequestQuotaReceipt{}, &QuotaBalancePublication{}))
			require.NoError(t, db.Create(&QuotaWriteProtocol{ID: quotaWriteProtocolID, Version: quotaWriteProtocolVersion, Mode: scenario.mode, DrainFrom: QuotaWriteSQL}).Error)

			newUserQuota, inviteeQuota, inviterQuota := common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter
			payment := operation_setting.GetPaymentSetting()
			previousPayment := *payment
			t.Cleanup(func() {
				common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = newUserQuota, inviteeQuota, inviterQuota
				*payment = previousPayment
			})
			common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = 100, 20, 30
			payment.ComplianceConfirmed = true
			payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion

			inviter := User{Username: "inviter", AffCode: "seed", AffQuota: 5, AffHistoryQuota: 10, AffCount: 1}
			require.NoError(t, db.Create(&inviter).Error)
			user := User{Username: "new-user", InviterId: inviter.Id}
			wantError := scenario.wantError
			if scenario.failAfterInsert {
				wantError = errors.New("OAuth binding failed")
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				if scenario.mode == QuotaWriteLegacy {
					return user.InsertWithTx(tx, inviter.Id)
				}
				if err := user.insertWithTx(tx, inviter.Id, true); err != nil {
					return err
				}
				if scenario.failAfterInsert {
					return wantError
				}
				return nil
			})
			var storedInviter User
			require.NoError(t, db.First(&storedInviter, inviter.Id).Error)
			var receipts []NonRequestQuotaReceipt
			require.NoError(t, db.Order("kind").Find(&receipts).Error)
			var publications []QuotaBalancePublication
			require.NoError(t, db.Find(&publications).Error)
			var storedUser User
			readUser := db.Where("username = ?", user.Username).Take(&storedUser)
			if wantError != nil {
				require.ErrorIs(t, err, wantError)
				require.ErrorIs(t, readUser.Error, gorm.ErrRecordNotFound)
				require.Empty(t, receipts)
				require.Empty(t, publications)
				require.Equal(t, 5, storedInviter.AffQuota)
				require.Equal(t, 10, storedInviter.AffHistoryQuota)
				require.Equal(t, 1, storedInviter.AffCount)
				return
			}
			require.NoError(t, err)
			require.NoError(t, readUser.Error)
			if scenario.mode == QuotaWriteLegacy {
				require.Equal(t, 100, storedUser.Quota)
				require.Equal(t, 5, storedInviter.AffQuota)
				require.Empty(t, receipts)
				require.Empty(t, publications)
				return
			}
			require.Equal(t, 120, storedUser.Quota)
			require.Equal(t, 120, user.Quota)
			require.Equal(t, 35, storedInviter.AffQuota)
			require.Equal(t, 40, storedInviter.AffHistoryQuota)
			require.Equal(t, 2, storedInviter.AffCount)
			require.Len(t, receipts, 3)
			for index := range receipts {
				require.Positive(t, receipts[index].CreatedAt)
				receipts[index].CreatedAt = 0
			}
			sourceID := strconv.Itoa(user.Id)
			require.Equal(t, []NonRequestQuotaReceipt{
				{Kind: "invitee", SourceID: sourceID, UserID: user.Id, ActorID: inviter.Id, Mode: "add", Amount: 20, Before: 100, After: 120},
				{Kind: "inviter", SourceID: sourceID, UserID: inviter.Id, ActorID: user.Id, Mode: "add", Amount: 30, Before: 5, After: 35},
				{Kind: "registration", SourceID: sourceID, UserID: user.Id, Mode: "add", Amount: 100, After: 100},
			}, receipts)
			require.Equal(t, []QuotaBalancePublication{{Kind: RefundPartWallet, SubjectID: user.Id, Version: 2, Balance: 120, Pending: true}}, publications)
		})
	}
}
