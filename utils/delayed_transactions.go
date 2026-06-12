package utils

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/handlers"
)

const DelayedTransactionsEpochDelay = 2

func DelayedTransactionsKey(networkId string, absoluteEpochIndex int) string {
	return fmt.Sprintf("%s%s:%d", constants.DBKeyPrefixDelayedTransactions, networkId, absoluteEpochIndex)
}

func DelayedTransactionsTargetEpoch(epochOffset int, localEpochIndex int) int {
	return epochOffset + localEpochIndex + DelayedTransactionsEpochDelay
}

func ActiveDelayedTransactionsEpoch(localEpochIndex int) int {
	return ActiveConsensusEpochOffset() + localEpochIndex
}

func LegacyDelayedTransactionsKey(epochIndex int) string {
	return constants.DBKeyPrefixDelayedTransactions + strconv.Itoa(epochIndex)
}

func DelayedTransactionsSigningPayload(networkId string, epochIndex int, payloadBytes []byte) string {
	return constants.SigningPrefixDelayedOperations + ":" + networkId + ":" + strconv.Itoa(epochIndex) + ":" + Blake3(string(payloadBytes))
}

func ParseDelayedTransactionsKey(key string) (networkId string, epochIndex int, legacy bool, ok bool) {
	raw := strings.TrimPrefix(key, constants.DBKeyPrefixDelayedTransactions)
	if raw == key || raw == "" {
		return "", 0, false, false
	}

	if epoch, err := strconv.Atoi(raw); err == nil {
		return "", epoch, true, true
	}

	networkId, rawEpoch, found := strings.Cut(raw, ":")
	if !found || networkId == "" || rawEpoch == "" {
		return "", 0, false, false
	}

	epoch, err := strconv.Atoi(rawEpoch)
	if err != nil || epoch < 0 {
		return "", 0, false, false
	}

	return networkId, epoch, false, true
}

func ActiveConsensusEpochOffset() int {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if handlers.EXECUTION_THREAD_METADATA.RecoveryPlan != nil {
		return handlers.EXECUTION_THREAD_METADATA.RecoveryPlan.LastEpochIndex + 1
	}

	return handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochOffset
}

func ShouldReadLegacyDelayedTransactions() bool {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	return handlers.EXECUTION_THREAD_METADATA.RecoveryPlan == nil &&
		handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochOffset == 0
}
