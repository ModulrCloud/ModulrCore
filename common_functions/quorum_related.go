package common_functions

import (
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
)

type ValidatorData struct {
	ValidatorPubKey string
	TotalStake      uint64
}

func GetFromApprovementThreadState(poolId string) *structures.PoolStorage {

	if val, ok := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.Cache[poolId]; ok {
		return val
	}

	data, err := globals.APPROVEMENT_THREAD_METADATA.Get([]byte(poolId), nil)

	if err != nil {
		return nil
	}

	var pool structures.PoolStorage

	err = json.Unmarshal(data, &pool)

	if err != nil {
		return nil
	}

	globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.Cache[poolId] = &pool

	return &pool

}

func SetLeadersSequence(epochHandler *structures.EpochDataHandler, epochSeed string) {

	epochHandler.LeadersSequence = []string{} // [pool0, pool1,...poolN]

	// Hash of metadata from the old epoch

	hashOfMetadataFromOldEpoch := utils.Blake3(epochSeed)

	// Change order of validators pseudo-randomly

	validatorsExtendedData := make(map[string]ValidatorData)

	var totalStakeSum uint64 = 0

	// Populate validator data and calculate total stake sum

	for _, validatorPubKey := range epochHandler.PoolsRegistry {

		validatorData := GetFromApprovementThreadState(validatorPubKey + "(POOL)_STORAGE_POOL")

		// Calculate total stake

		totalStakeByThisValidator := validatorData.TotalStaked

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData[validatorPubKey] = ValidatorData{validatorPubKey, totalStakeByThisValidator}

	}

	// Iterate over the poolsRegistry and pseudo-randomly choose leaders

	for i := range len(epochHandler.PoolsRegistry) {

		cumulativeSum := uint64(0)

		// Generate deterministic random value using the hash of metadata
		hashInput := hashOfMetadataFromOldEpoch + "_" + strconv.Itoa(i)

		// Generate deterministic random value from Blake3 hash
		hashHex := utils.Blake3(hashInput)

		// Take the first 16 hex chars (= 8 bytes) and convert to uint64
		b, _ := hex.DecodeString(hashHex[:16])

		var deterministicRandomValue uint64

		for _, by := range b {
			deterministicRandomValue = (deterministicRandomValue << 8) | uint64(by)
		}

		// Reduce into range [0, totalStakeSum-1]
		if totalStakeSum > 0 {
			deterministicRandomValue = deterministicRandomValue % totalStakeSum
		} else {
			deterministicRandomValue = 0
		}

		// Find the validator based on the random value
		for validatorPubKey, validator := range validatorsExtendedData {

			cumulativeSum += validator.TotalStake

			if deterministicRandomValue < cumulativeSum {
				// Add the chosen validator to the leaders sequence
				epochHandler.LeadersSequence = append(epochHandler.LeadersSequence, validatorPubKey)

				// Update totalStakeSum and remove the chosen validator from the map
				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					// should not happen if invariants hold
					totalStakeSum = 0
				}

				delete(validatorsExtendedData, validatorPubKey)
				break
			}

		}

	}

}

func GetQuorumMajority(epochHandler *structures.EpochDataHandler) int {

	quorumSize := len(epochHandler.Quorum)

	majority := (2 * quorumSize) / 3

	majority += 1

	if majority > quorumSize {
		return quorumSize
	}

	return majority
}

func GetQuorumUrlsAndPubkeys(epochHandler *structures.EpochDataHandler) []structures.QuorumMemberData {

	var toReturn []structures.QuorumMemberData

	for _, pubKey := range epochHandler.Quorum {

		poolStorage := GetFromApprovementThreadState(pubKey + "(POOL)_STORAGE_POOL")

		toReturn = append(toReturn, structures.QuorumMemberData{PubKey: pubKey, Url: poolStorage.PoolUrl})

	}

	return toReturn

}

func GetCurrentEpochQuorum(epochHandler *structures.EpochDataHandler, quorumSize int, newEpochSeed string) []string {

	totalNumberOfValidators := len(epochHandler.PoolsRegistry)

	if totalNumberOfValidators <= quorumSize {

		futureQuorum := make([]string, len(epochHandler.PoolsRegistry))

		copy(futureQuorum, epochHandler.PoolsRegistry)

		return futureQuorum
	}

	quorum := []string{}

	// Blake3 hash of epoch metadata (hex string)
	hashOfMetadataFromEpoch := utils.Blake3(newEpochSeed)

	// Collect validator data and total stake (uint64)
	validatorsExtendedData := make(map[string]ValidatorData)

	var totalStakeSum uint64 = 0

	for _, validatorPubKey := range epochHandler.PoolsRegistry {

		validatorData := GetFromApprovementThreadState(validatorPubKey + "(POOL)_STORAGE_POOL")

		totalStakeByThisValidator := validatorData.TotalStaked // uint64

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData[validatorPubKey] = ValidatorData{
			ValidatorPubKey: validatorPubKey,
			TotalStake:      totalStakeByThisValidator,
		}
	}

	// If total stake is zero, no weighted choice is possible

	if totalStakeSum == 0 {
		return quorum
	}

	// Draw 'quorumSize' validators without replacement
	for i := 0; i < quorumSize; i++ {
		// Deterministic "random": Blake3(hash || "_" || i) -> uint64
		hashInput := hashOfMetadataFromEpoch + "_" + strconv.Itoa(i)
		hashHex := utils.Blake3(hashInput) // hex string

		// Take the first 8 bytes (16 hex chars) -> uint64 BigEndian
		var r uint64 = 0
		if len(hashHex) >= 16 {
			if b, err := hex.DecodeString(hashHex[:16]); err == nil {
				for _, by := range b {
					r = (r << 8) | uint64(by)
				}
			}
		}
		// Reduce into [0, totalStakeSum-1]
		if totalStakeSum > 0 {
			r = r % totalStakeSum
		} else {
			r = 0
		}

		// Iterate over current validators and pick the one that hits the interval
		var cumulativeSum uint64 = 0
		for validatorPubKey, validator := range validatorsExtendedData {
			cumulativeSum += validator.TotalStake

			// Preserve original logic: choose when r <= cumulativeSum
			if r <= cumulativeSum {
				// Add chosen validator
				quorum = append(quorum, validatorPubKey)

				// Update total stake and remove chosen one (draw without replacement)
				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					totalStakeSum = 0
				}
				delete(validatorsExtendedData, validatorPubKey)
				break
			}
		}

		// If total stake became zero, no further weighted draws are possible
		if totalStakeSum == 0 || len(validatorsExtendedData) == 0 {
			break
		}
	}

	return quorum
}
