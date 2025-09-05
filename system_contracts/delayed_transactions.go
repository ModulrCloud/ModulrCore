package system_contracts

import (
	"slices"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/common_functions"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
)

type DelayedTxExecutorFunction = func(map[string]string, string) bool

var DELAYED_TRANSACTIONS_MAP = map[string]DelayedTxExecutorFunction{
	"createStakingPool": CreateStakingPool,
	"updateStakingPool": UpdateStakingPool,
	"stake":             Stake,
	"unstake":           Unstake,
}

func removeFromSlice[T comparable](s []T, v T) []T {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func CreateStakingPool(delayedTransaction map[string]string, context string) bool {

	creator := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	poolURL := delayedTransaction["poolURL"]
	wssPoolURL := delayedTransaction["wssPoolURL"]

	if poolURL != "" && wssPoolURL != "" && percentage <= 100 {

		storageKey := creator + "(POOL)_STORAGE_POOL"

		if _, exists := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.Cache[storageKey]; exists {

			return false

		}

		globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.Cache[storageKey] = &structures.PoolStorage{
			Percentage:  percentage,
			TotalStaked: 0,
			Stakers: map[string]structures.Staker{
				creator: {
					Stake: 0,
				},
			},
			PoolUrl:    poolURL,
			WssPoolUrl: wssPoolURL,
		}

		return true
	}

	return false
}

func UpdateStakingPool(delayedTransaction map[string]string, context string) bool {

	creator := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	poolURL := delayedTransaction["poolURL"]
	wssPoolURL := delayedTransaction["wssPoolURL"]

	if percentage > 100 || poolURL == "" || wssPoolURL == "" {

		return false

	}

	poolStorage := common_functions.GetFromApprovementThreadState(creator + "(POOL)_STORAGE_POOL")

	if poolStorage != nil {

		poolStorage.Percentage = percentage

		poolStorage.PoolUrl = poolURL

		poolStorage.WssPoolUrl = wssPoolURL

		requiredStake := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.ValidatorStake

		if poolStorage.TotalStaked >= requiredStake {

			if !slices.Contains(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry, creator) {

				globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry = append(

					globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry, creator,
				)
			}

		} else {

			removeFromSlice(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry, creator)

		}

		globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.Cache[creator+"(POOL)_STORAGE_POOL"] = poolStorage

		return true

	}

	return false

}

func Stake(delayedTransaction map[string]string, context string) bool {

	staker := delayedTransaction["staker"]
	poolPubKey := delayedTransaction["poolPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {

		return false

	}

	poolStorage := common_functions.GetFromApprovementThreadState(poolPubKey + "(POOL)_STORAGE_POOL")

	if poolStorage != nil {

		minStake := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.MinimalStakePerEntity

		if amount < minStake {

			return false

		}

		if _, exists := poolStorage.Stakers[staker]; !exists {

			poolStorage.Stakers[staker] = structures.Staker{

				Stake: 0,
			}

		}

		stakerData := poolStorage.Stakers[staker]

		stakerData.Stake += amount

		poolStorage.TotalStaked += amount

		poolStorage.Stakers[staker] = stakerData

		requiredStake := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.ValidatorStake

		if poolStorage.TotalStaked >= requiredStake {

			if !slices.Contains(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry, poolPubKey) {

				globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry = append(
					globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry, poolPubKey,
				)

			}

		}

		return true

	}

	return false

}

func Unstake(delayedTransaction map[string]string, context string) bool {

	unstaker := delayedTransaction["unstaker"]
	poolPubKey := delayedTransaction["poolPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {

		return false

	}

	poolStorage := common_functions.GetFromApprovementThreadState(poolPubKey + "(POOL)_STORAGE_POOL")

	if poolStorage != nil {

		stakerData, exists := poolStorage.Stakers[unstaker]

		if !exists {

			return false

		}

		if stakerData.Stake < amount {

			return false

		}

		stakerData.Stake -= amount

		poolStorage.TotalStaked -= amount

		if stakerData.Stake == 0 {

			delete(poolStorage.Stakers, unstaker) // no sense to store staker with 0 balance in stakers list

		} else {

			poolStorage.Stakers[unstaker] = stakerData

		}

		requiredStake := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.ValidatorStake

		if poolStorage.TotalStaked < requiredStake {

			removeFromSlice(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry, poolPubKey)

		}

		return true

	}

	return false

}
