package system_contracts

import (
	"slices"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
)

type DelayedTxExecutorFunction = func(map[string]string, string) bool

var DELAYED_TRANSACTIONS_MAP = map[string]DelayedTxExecutorFunction{
	"createValidator": CreateValidator,
	"updateValidator": UpdateValidator,
	"stake":           Stake,
	"unstake":         Unstake,
}

func removeFromSlice[T comparable](s []T, v T) []T {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func CreateValidator(delayedTransaction map[string]string, context string) bool {

	validatorPubkey := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	validatorURL := delayedTransaction["validatorURL"]
	wssValidatorURL := delayedTransaction["wssValidatorURL"]

	if validatorURL != "" && wssValidatorURL != "" && percentage <= 100 {

		validatorStorageKey := validatorPubkey + "_VALIDATOR_STORAGE"

		if context == "AT" {

			if _, existsInCache := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey]; existsInCache {

				return false

			}

			_, existErr := globals.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorStorageKey), nil)

			// Activate this branch only in case we still don't have this validator in db

			if existErr != nil {

				globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey] = &structures.ValidatorStorage{
					Pubkey:      validatorPubkey,
					Percentage:  percentage,
					TotalStaked: 0,
					Stakers: map[string]structures.Staker{
						validatorPubkey: {
							Stake: 0,
						},
					},
					ValidatorUrl:    validatorURL,
					WssValidatorUrl: wssValidatorURL,
				}

				return true

			}

			return false

		} else {

			if _, existsInCache := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey]; existsInCache {

				return false

			}

			_, existErr := globals.STATE.Get([]byte(validatorStorageKey), nil)

			if existErr != nil {

				globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey] = &structures.ValidatorStorage{
					Pubkey:      validatorPubkey,
					Percentage:  percentage,
					TotalStaked: 0,
					Stakers: map[string]structures.Staker{
						validatorPubkey: {
							Stake: 0,
						},
					},
					ValidatorUrl:    validatorURL,
					WssValidatorUrl: wssValidatorURL,
				}

				return true

			}

			return false

		}

	}

	return false
}

func UpdateValidator(delayedTransaction map[string]string, context string) bool {

	validatorPubkey := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	validatorURL := delayedTransaction["validatorURL"]
	wssValidatorURL := delayedTransaction["wssValidatorURL"]

	if percentage > 100 || validatorURL == "" || wssValidatorURL == "" {

		return false

	}

	validatorStorageId := validatorPubkey + "_VALIDATOR_STORAGE"

	if context == "AT" {

		validatorStorage := utils.GetValidatorFromApprovementThreadState(validatorPubkey)

		if validatorStorage != nil {

			validatorStorage.Percentage = percentage

			validatorStorage.ValidatorUrl = validatorURL

			validatorStorage.WssValidatorUrl = wssValidatorURL

			globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageId] = validatorStorage

			return true

		}

		return false

	} else {

		validatorStorage := utils.GetValidatorFromExecThreadState(validatorPubkey)

		if validatorStorage != nil {

			validatorStorage.Percentage = percentage

			validatorStorage.ValidatorUrl = validatorURL

			validatorStorage.WssValidatorUrl = wssValidatorURL

			globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageId] = validatorStorage

			return true

		}

		return false

	}

}

func Stake(delayedTransaction map[string]string, context string) bool {

	staker := delayedTransaction["staker"]
	validatorPubkey := delayedTransaction["validatorPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {

		return false

	}

	if context == "AT" {

		validatorStorage := utils.GetValidatorFromApprovementThreadState(validatorPubkey)

		if validatorStorage != nil {

			minStake := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.MinimalStakePerStaker

			if amount < minStake {

				return false

			}

			if _, exists := validatorStorage.Stakers[staker]; !exists {

				validatorStorage.Stakers[staker] = structures.Staker{

					Stake: 0,
				}

			}

			stakerData := validatorStorage.Stakers[staker]

			stakerData.Stake += amount

			validatorStorage.TotalStaked += amount

			validatorStorage.Stakers[staker] = stakerData

			requiredStake := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked >= requiredStake {

				if !slices.Contains(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey) {

					globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry = append(
						globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey,
					)

				}

			}

			return true

		}

		return false

	} else {

		validatorStorage := utils.GetValidatorFromExecThreadState(validatorPubkey)

		if validatorStorage != nil {

			minStake := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.NetworkParameters.MinimalStakePerStaker

			if amount < minStake {

				return false

			}

			if _, exists := validatorStorage.Stakers[staker]; !exists {

				validatorStorage.Stakers[staker] = structures.Staker{

					Stake: 0,
				}

			}

			stakerData := validatorStorage.Stakers[staker]

			stakerData.Stake += amount

			validatorStorage.TotalStaked += amount

			validatorStorage.Stakers[staker] = stakerData

			requiredStake := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked >= requiredStake {

				if !slices.Contains(globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey) {

					globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry = append(
						globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey,
					)

				}

			}

			return true

		}

		return false

	}

}

func Unstake(delayedTransaction map[string]string, context string) bool {

	unstaker := delayedTransaction["unstaker"]
	validatorPubkey := delayedTransaction["validatorPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {

		return false

	}

	if context == "AT" {

		validatorStorage := utils.GetValidatorFromApprovementThreadState(validatorPubkey)

		if validatorStorage != nil {

			stakerData, exists := validatorStorage.Stakers[unstaker]

			if !exists {

				return false

			}

			if stakerData.Stake < amount {

				return false

			}

			stakerData.Stake -= amount

			validatorStorage.TotalStaked -= amount

			if stakerData.Stake == 0 {

				delete(validatorStorage.Stakers, unstaker) // no sense to store staker with 0 balance in stakers list

			} else {

				validatorStorage.Stakers[unstaker] = stakerData

			}

			requiredStake := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked < requiredStake {

				reg := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry

				globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry = removeFromSlice(reg, validatorPubkey)

			}

			return true

		}

		return false

	} else {

		validatorStorage := utils.GetValidatorFromExecThreadState(validatorPubkey)

		if validatorStorage != nil {

			stakerData, exists := validatorStorage.Stakers[unstaker]

			if !exists {

				return false

			}

			if stakerData.Stake < amount {

				return false

			}

			stakerData.Stake -= amount

			validatorStorage.TotalStaked -= amount

			if stakerData.Stake == 0 {

				delete(validatorStorage.Stakers, unstaker) // no sense to store staker with 0 balance in stakers list

			} else {

				validatorStorage.Stakers[unstaker] = stakerData

			}

			requiredStake := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked < requiredStake {

				reg := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry

				globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.ValidatorsRegistry = removeFromSlice(reg, validatorPubkey)

			}

			return true

		}

		return false

	}

}
