package utils

import (
	"encoding/hex"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
)

type CurrentLeaderData struct {
	IsMeLeader bool
	Url        string
}

func GetCurrentLeader() CurrentLeaderData {

	globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RLock()

	defer globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

	currentLeaderIndex := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.CurrentLeaderIndex

	currentLeaderPubKey := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.LeadersSequence[currentLeaderIndex]

	if currentLeaderPubKey != globals.CONFIGURATION.PublicKey {

		poolStorage := GetPoolFromApprovementThreadState(currentLeaderPubKey)

		if poolStorage != nil {

			return CurrentLeaderData{IsMeLeader: false, Url: poolStorage.PoolUrl}

		}

	}

	return CurrentLeaderData{IsMeLeader: true, Url: ""}

}

func SetLeadersSequence(epochHandler *structures.EpochDataHandler, epochSeed string) {

	epochHandler.LeadersSequence = []string{} // [pool0, pool1,...poolN]

	// Hash of metadata from the old epoch
	hashOfMetadataFromOldEpoch := Blake3(epochSeed)

	// Change order of validators pseudo-randomly
	validatorsExtendedData := make([]ValidatorData, 0, len(epochHandler.PoolsRegistry))

	var totalStakeSum uint64 = 0

	// Populate validator data and calculate total stake sum
	for _, validatorPubKey := range epochHandler.PoolsRegistry {

		validatorData := GetPoolFromApprovementThreadState(validatorPubKey)

		// Calculate total stake
		totalStakeByThisValidator := validatorData.TotalStaked

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData = append(validatorsExtendedData, ValidatorData{
			ValidatorPubKey: validatorPubKey,
			TotalStake:      totalStakeByThisValidator,
		})
	}

	// Iterate over the poolsRegistry and pseudo-randomly choose leaders
	for i := 0; i < len(epochHandler.PoolsRegistry); i++ {

		cumulativeSum := uint64(0)

		// Generate deterministic random value using the hash of metadata
		hashInput := hashOfMetadataFromOldEpoch + "_" + strconv.Itoa(i)
		hashHex := Blake3(hashInput)
		var deterministicRandomValue uint64
		if len(hashHex) >= 16 {
			if b, err := hex.DecodeString(hashHex[:16]); err == nil {
				for _, by := range b {
					deterministicRandomValue = (deterministicRandomValue << 8) | uint64(by)
				}
			}
		}
		if totalStakeSum > 0 {
			deterministicRandomValue = deterministicRandomValue % totalStakeSum
		}

		// Find the validator based on the random value
		for idx, validator := range validatorsExtendedData {

			cumulativeSum += validator.TotalStake

			if deterministicRandomValue < cumulativeSum {

				// Add the chosen validator to the leaders sequence
				epochHandler.LeadersSequence = append(epochHandler.LeadersSequence, validator.ValidatorPubKey)

				// Update totalStakeSum and remove the chosen validator from the list

				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					totalStakeSum = 0
				}

				validatorsExtendedData = append(validatorsExtendedData[:idx], validatorsExtendedData[idx+1:]...)

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

		poolStorage := GetPoolFromApprovementThreadState(pubKey)

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
	hashOfMetadataFromEpoch := Blake3(newEpochSeed)

	// Collect validator data and total stake (uint64)
	validatorsExtendedData := make([]ValidatorData, 0, len(epochHandler.PoolsRegistry))

	var totalStakeSum uint64 = 0

	for _, validatorPubKey := range epochHandler.PoolsRegistry {

		validatorData := GetPoolFromApprovementThreadState(validatorPubKey)

		totalStakeByThisValidator := validatorData.TotalStaked // uint64

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData = append(validatorsExtendedData, ValidatorData{
			ValidatorPubKey: validatorPubKey,
			TotalStake:      totalStakeByThisValidator,
		})
	}

	// If total stake is zero, no weighted choice is possible

	if totalStakeSum == 0 {
		return quorum
	}

	// Draw 'quorumSize' validators without replacement
	for i := 0; i < quorumSize && len(validatorsExtendedData) > 0; i++ {

		// Deterministic "random": Blake3(hash || "_" || i) -> uint64
		hashInput := hashOfMetadataFromEpoch + "_" + strconv.Itoa(i)
		hashHex := Blake3(hashInput) // hex string

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

		for idx, validator := range validatorsExtendedData {

			cumulativeSum += validator.TotalStake

			// Preserve original logic: choose when r <= cumulativeSum
			if r < cumulativeSum {
				// Add chosen validator
				quorum = append(quorum, validator.ValidatorPubKey)

				// Update total stake and remove chosen one (draw without replacement)
				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					totalStakeSum = 0
				}
				validatorsExtendedData = append(validatorsExtendedData[:idx], validatorsExtendedData[idx+1:]...)
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
