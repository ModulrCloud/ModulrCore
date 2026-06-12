package tests

import (
	"testing"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func TestDelayedTransactionsUseAbsoluteTargetEpochWithNetworkNamespace(t *testing.T) {
	if got := utils.DelayedTransactionsTargetEpoch(51, 0); got != 53 {
		t.Fatalf("expected target epoch offset+local+2, got %d", got)
	}

	key := utils.DelayedTransactionsKey("network-a", 53)
	networkId, epoch, legacy, ok := utils.ParseDelayedTransactionsKey(key)
	if !ok || legacy || networkId != "network-a" || epoch != 53 {
		t.Fatalf("unexpected parsed key: networkId=%q epoch=%d legacy=%v ok=%v", networkId, epoch, legacy, ok)
	}

	networkId, epoch, legacy, ok = utils.ParseDelayedTransactionsKey(utils.LegacyDelayedTransactionsKey(2))
	if !ok || !legacy || networkId != "" || epoch != 2 {
		t.Fatalf("unexpected parsed legacy key: networkId=%q epoch=%d legacy=%v ok=%v", networkId, epoch, legacy, ok)
	}
}

func TestActiveDelayedTransactionsEpochUsesRecoveryPlanOffset(t *testing.T) {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
	handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochOffset = 0
	handlers.EXECUTION_THREAD_METADATA.RecoveryPlan = &structures.RecoveryData{LastEpochIndex: 50}
	handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	t.Cleanup(func() {
		handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
		handlers.EXECUTION_THREAD_METADATA.RecoveryPlan = nil
		handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochOffset = 0
		handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
	})

	if got := utils.ActiveDelayedTransactionsEpoch(0); got != 51 {
		t.Fatalf("expected recovered consensus epoch 0 to map to absolute epoch 51, got %d", got)
	}
}
