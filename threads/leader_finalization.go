package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/gorilla/websocket"
)

type LeaderFinalizationCache struct {
	SkipData structures.VotingStat
	Proofs   map[string]string
}

type EpochRotationState struct {
	caches  map[string]*LeaderFinalizationCache
	wsConns map[string]*websocket.Conn
	waiter  *utils.QuorumWaiter
}

var (
	leaderFinalizationMutex  = sync.Mutex{}
	leaderFinalizationStates = make(map[int]*EpochRotationState)
	processingEpochId        = -1
	finalizationProgressKey  = []byte("ALFP_PROGRESS")
)

func LeadersFinalizationThread() {

	for {

		if processingEpochId == -1 {
			processingEpochId = loadFinalizationProgress()
		}

		processingHandler := getOrLoadEpochHandler(processingEpochId)
		networkParams := getNetworkParameters()

		if processingHandler == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		state := ensureLeaderFinalizationState(processingHandler)
		majority := utils.GetQuorumMajority(processingHandler)

		for _, leaderIndex := range leadersReadyForAlfp(processingHandler, &networkParams) {
			tryCollectLeaderFinalizationProofs(processingHandler, leaderIndex, majority, state)
		}

		if allLeaderFinalizationProofsCollected(processingHandler) {
			processingEpochId++
			persistFinalizationProgress(processingEpochId)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func ensureLeaderFinalizationState(epochHandler *structures.EpochDataHandler) *EpochRotationState {

	leaderFinalizationMutex.Lock()
	defer leaderFinalizationMutex.Unlock()

	if state, ok := leaderFinalizationStates[epochHandler.Id]; ok {
		return state
	}

	state := &EpochRotationState{
		caches:  make(map[string]*LeaderFinalizationCache),
		wsConns: make(map[string]*websocket.Conn),
		waiter:  utils.NewQuorumWaiter(len(epochHandler.Quorum)),
	}

	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, state.wsConns)
	leaderFinalizationStates[epochHandler.Id] = state

	return state
}

func getOrLoadEpochHandler(epochId int) *structures.EpochDataHandler {

	handlers.FINALIZATION_THREAD_METADATA.RWMutex.RLock()
	handler, ok := handlers.FINALIZATION_THREAD_METADATA.EpochHandlers[epochId]
	handlers.FINALIZATION_THREAD_METADATA.RWMutex.RUnlock()

	if ok {
		h := handler
		return &h
	}

	key := []byte("EPOCH_HANDLER:" + strconv.Itoa(epochId))
	raw, err := databases.EPOCH_DATA.Get(key, nil)
	if err != nil {
		return nil
	}

	var loaded structures.EpochDataHandler
	if json.Unmarshal(raw, &loaded) != nil {
		return nil
	}

	handlers.FINALIZATION_THREAD_METADATA.RWMutex.Lock()
	handlers.FINALIZATION_THREAD_METADATA.EpochHandlers[epochId] = loaded
	handlers.FINALIZATION_THREAD_METADATA.RWMutex.Unlock()

	return &loaded
}

func loadFinalizationProgress() int {

	if raw, err := databases.FINALIZATION_VOTING_STATS.Get(finalizationProgressKey, nil); err == nil {
		if idx, convErr := strconv.Atoi(string(raw)); convErr == nil {
			return idx
		}
	}

	return 0
}

func persistFinalizationProgress(epochId int) {

	_ = databases.FINALIZATION_VOTING_STATS.Put(finalizationProgressKey, []byte(strconv.Itoa(epochId)), nil)
}

func getNetworkParameters() structures.NetworkParameters {

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	params := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	return params
}

func leadersReadyForAlfp(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters) []int {

	ready := make([]int, 0)

	for idx := range epochHandler.LeadersSequence {

		if leaderHasAlfp(epochHandler.Id, epochHandler.LeadersSequence[idx]) {
			continue
		}

		leaderFinished := idx < epochHandler.CurrentLeaderIndex ||
			(idx == epochHandler.CurrentLeaderIndex && leaderTimeIsOut(epochHandler, networkParams, idx))

		if leaderFinished {
			ready = append(ready, idx)
		}
	}

	return ready
}

func leaderHasAlfp(epochId int, leader string) bool {

	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leader))
	_, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	return err == nil
}

func allLeaderFinalizationProofsCollected(epochHandler *structures.EpochDataHandler) bool {

	for _, leader := range epochHandler.LeadersSequence {
		if !leaderHasAlfp(epochHandler.Id, leader) {
			return false
		}
	}

	return true
}

func leaderTimeIsOut(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters, leaderIndex int) bool {

	leadershipTimeframe := networkParams.LeadershipDuration
	return utils.GetUTCTimestampInMilliSeconds() >= int64(epochHandler.StartTimestamp)+(int64(leaderIndex)+1)*leadershipTimeframe
}

func tryCollectLeaderFinalizationProofs(epochHandler *structures.EpochDataHandler, leaderIndex, majority int, state *EpochRotationState) {

	leaderPubKey := epochHandler.LeadersSequence[leaderIndex]
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	if leaderHasAlfp(epochHandler.Id, leaderPubKey) || state.waiter == nil {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	request := websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   "get_leader_finalization_proof",
		IndexOfLeaderToFinalize: leaderIndex,
		SkipData:                cache.SkipData,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	responses, ok := state.waiter.SendAndWait(ctx, message, epochHandler.Quorum, state.wsConns, majority)
	if !ok {
		return
	}

	for _, raw := range responses {
		handleLeaderFinalizationResponse(raw, epochHandler, leaderPubKey, epochFullID, state)
	}

	leaderFinalizationMutex.Lock()
	proofsCount := len(cache.Proofs)
	leaderFinalizationMutex.Unlock()

	if proofsCount >= majority {
		persistAggregatedLeaderFinalizationProof(cache, epochHandler.Id, leaderPubKey)
	}
}

func ensureLeaderFinalizationCache(state *EpochRotationState, epochId int, leaderPubKey string) *LeaderFinalizationCache {

	key := fmt.Sprintf("%d:%s", epochId, leaderPubKey)

	leaderFinalizationMutex.Lock()
	defer leaderFinalizationMutex.Unlock()

	if cache, ok := state.caches[key]; ok {
		return cache
	}

	cache := &LeaderFinalizationCache{
		SkipData: loadLeaderSkipData(epochId, leaderPubKey),
		Proofs:   make(map[string]string),
	}

	state.caches[key] = cache

	return cache
}

func loadLeaderSkipData(epochId int, leaderPubKey string) structures.VotingStat {

	skipData := structures.NewLeaderVotingStatTemplate()
	key := []byte(fmt.Sprintf("%d:%s", epochId, leaderPubKey))

	if raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil); err == nil {
		_ = json.Unmarshal(raw, &skipData)
	}

	return skipData
}

func handleLeaderFinalizationResponse(raw []byte, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *EpochRotationState) {

	var statusHolder map[string]any

	if err := json.Unmarshal(raw, &statusHolder); err != nil {
		return
	}

	status, ok := statusHolder["status"].(string)
	if !ok {
		return
	}

	switch status {
	case "OK":
		var response websocket_pack.WsLeaderFinalizationProofResponseOk
		if json.Unmarshal(raw, &response) == nil {
			handleLeaderFinalizationOk(response, epochHandler, leaderPubKey, epochFullID, state)
		}
	case "UPGRADE":
		var response websocket_pack.WsLeaderFinalizationProofResponseUpgrade
		if json.Unmarshal(raw, &response) == nil {
			handleLeaderFinalizationUpgrade(response, epochHandler, leaderPubKey, state)
		}
	}
}

func handleLeaderFinalizationOk(response websocket_pack.WsLeaderFinalizationProofResponseOk, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *EpochRotationState) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	dataToVerify := strings.Join([]string{"LEADER_FINALIZATION_PROOF", leaderPubKey, strconv.Itoa(cache.SkipData.Index), cache.SkipData.Hash, epochFullID}, ":")

	quorumMap := make(map[string]bool)
	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	if cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig) {
		lowered := strings.ToLower(response.Voter)
		leaderFinalizationMutex.Lock()
		if quorumMap[lowered] {
			cache.Proofs[response.Voter] = response.Sig
		}
		leaderFinalizationMutex.Unlock()
	}
}

func handleLeaderFinalizationUpgrade(response websocket_pack.WsLeaderFinalizationProofResponseUpgrade, epochHandler *structures.EpochDataHandler, leaderPubKey string, state *EpochRotationState) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	if !validateUpgradePayload(response, epochHandler, leaderPubKey) {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	leaderFinalizationMutex.Lock()
	cache.SkipData = response.SkipData
	cache.Proofs = make(map[string]string)
	leaderFinalizationMutex.Unlock()
}

func validateUpgradePayload(response websocket_pack.WsLeaderFinalizationProofResponseUpgrade, epochHandler *structures.EpochDataHandler, leaderPubKey string) bool {

	if response.SkipData.Index >= 0 {

		parts := strings.Split(response.SkipData.Afp.BlockId, ":")

		if len(parts) != 3 {
			return false
		}

		indexFromId, err := strconv.Atoi(parts[2])
		if err != nil || indexFromId != response.SkipData.Index || parts[0] != strconv.Itoa(epochHandler.Id) || parts[1] != leaderPubKey {
			return false
		}

		if response.SkipData.Hash != response.SkipData.Afp.BlockHash {
			return false
		}

		if !utils.VerifyAggregatedFinalizationProof(&response.SkipData.Afp, epochHandler) {
			return false
		}
	}

	return true
}

func persistAggregatedLeaderFinalizationProof(cache *LeaderFinalizationCache, epochId int, leaderPubKey string) {

	leaderFinalizationMutex.Lock()

	aggregated := structures.AggregatedLeaderFinalizationProof{
		EpochIndex: epochId,
		Leader:     leaderPubKey,
		VotingStat: structures.VotingStat{
			Index: cache.SkipData.Index,
			Hash:  cache.SkipData.Hash,
			Afp:   cache.SkipData.Afp,
		},
		Signatures: cache.Proofs,
	}

	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leaderPubKey))
	if value, err := json.Marshal(aggregated); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}

	leaderFinalizationMutex.Unlock()

}
