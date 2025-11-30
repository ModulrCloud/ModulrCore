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

type leaderRotationCache struct {
	AfpForFirstBlock structures.AggregatedFinalizationProof
	SkipData         structures.LeaderVotingStat
	Proofs           map[string]string
}

type epochRotationState struct {
	caches  map[string]*leaderRotationCache
	wsConns map[string]*websocket.Conn
	waiter  *utils.QuorumWaiter
}

var (
	leaderRotationMutex     = sync.Mutex{}
	leaderRotationStates    = make(map[int]*epochRotationState)
	processingEpochId       = -1
	defaultHash             = structures.NewLeaderVotingStatTemplate().Hash
	finalizationProgressKey = []byte("ALRP_PROGRESS")
)

func FinalizationThread() {

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

		state := ensureLeaderRotationState(processingHandler)
		majority := utils.GetQuorumMajority(processingHandler)

		for _, leaderIndex := range leadersReadyForAlrp(processingHandler, &networkParams) {
			tryCollectLeaderRotationProofs(processingHandler, leaderIndex, majority, state)
		}

		if allLeaderRotationProofsCollected(processingHandler) {
			processingEpochId++
			persistFinalizationProgress(processingEpochId)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func ensureLeaderRotationState(epochHandler *structures.EpochDataHandler) *epochRotationState {

	leaderRotationMutex.Lock()
	defer leaderRotationMutex.Unlock()

	if state, ok := leaderRotationStates[epochHandler.Id]; ok {
		return state
	}

	state := &epochRotationState{
		caches:  make(map[string]*leaderRotationCache),
		wsConns: make(map[string]*websocket.Conn),
		waiter:  utils.NewQuorumWaiter(len(epochHandler.Quorum)),
	}

	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, state.wsConns)
	leaderRotationStates[epochHandler.Id] = state

	return state
}

func getOrLoadEpochHandler(epochId int) *structures.EpochDataHandler {

	handlers.FINALIZATION_THREAD_CACHE.RWMutex.RLock()
	handler, ok := handlers.FINALIZATION_THREAD_CACHE.EpochHandlers[epochId]
	handlers.FINALIZATION_THREAD_CACHE.RWMutex.RUnlock()

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

	handlers.FINALIZATION_THREAD_CACHE.RWMutex.Lock()
	handlers.FINALIZATION_THREAD_CACHE.EpochHandlers[epochId] = loaded
	handlers.FINALIZATION_THREAD_CACHE.RWMutex.Unlock()

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

func leadersReadyForAlrp(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters) []int {

	ready := make([]int, 0)

	for idx := range epochHandler.LeadersSequence {

		if leaderHasAlrp(epochHandler.Id, epochHandler.LeadersSequence[idx]) {
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

func leaderHasAlrp(epochId int, leader string) bool {

	key := []byte(fmt.Sprintf("ALRP:%d:%s", epochId, leader))
	_, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	return err == nil
}

func allLeaderRotationProofsCollected(epochHandler *structures.EpochDataHandler) bool {

	for _, leader := range epochHandler.LeadersSequence {
		if !leaderHasAlrp(epochHandler.Id, leader) {
			return false
		}
	}

	return true
}

func leaderTimeIsOut(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters, leaderIndex int) bool {

	leadershipTimeframe := networkParams.LeadershipDuration
	return utils.GetUTCTimestampInMilliSeconds() >= int64(epochHandler.StartTimestamp)+(int64(leaderIndex)+1)*leadershipTimeframe
}

func tryCollectLeaderRotationProofs(epochHandler *structures.EpochDataHandler, leaderIndex, majority int, state *epochRotationState) {

	leaderPubKey := epochHandler.LeadersSequence[leaderIndex]
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	if leaderHasAlrp(epochHandler.Id, leaderPubKey) || state.waiter == nil {
		return
	}

	cache := ensureLeaderRotationCache(state, epochHandler.Id, leaderPubKey)

	request := websocket_pack.WsLeaderRotationProofRequest{
		Route:                 "get_leader_rotation_proof",
		IndexOfLeaderToRotate: leaderIndex,
		AfpForFirstBlock:      cache.AfpForFirstBlock,
		SkipData:              cache.SkipData,
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
		handleLeaderRotationResponse(raw, epochHandler, leaderPubKey, epochFullID, state)
	}

	leaderRotationMutex.Lock()
	proofsCount := len(cache.Proofs)
	leaderRotationMutex.Unlock()

	if proofsCount >= majority {
		persistAggregatedLeaderRotationProof(cache, epochHandler.Id, leaderPubKey)
	}
}

func ensureLeaderRotationCache(state *epochRotationState, epochId int, leaderPubKey string) *leaderRotationCache {

	key := fmt.Sprintf("%d:%s", epochId, leaderPubKey)

	leaderRotationMutex.Lock()
	defer leaderRotationMutex.Unlock()

	if cache, ok := state.caches[key]; ok {
		return cache
	}

	cache := &leaderRotationCache{
		SkipData: loadLeaderSkipData(epochId, leaderPubKey),
		Proofs:   make(map[string]string),
	}

	cache.AfpForFirstBlock = loadAfpForFirstBlock(epochId, leaderPubKey)

	state.caches[key] = cache

	return cache
}

func loadLeaderSkipData(epochId int, leaderPubKey string) structures.LeaderVotingStat {

	skipData := structures.NewLeaderVotingStatTemplate()
	key := []byte(fmt.Sprintf("%d:%s", epochId, leaderPubKey))

	if raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil); err == nil {
		_ = json.Unmarshal(raw, &skipData)
	}

	return skipData
}

func loadAfpForFirstBlock(epochId int, leaderPubKey string) structures.AggregatedFinalizationProof {

	var afp structures.AggregatedFinalizationProof
	key := []byte("AFP:" + fmt.Sprintf("%d:%s:0", epochId, leaderPubKey))

	if raw, err := databases.EPOCH_DATA.Get(key, nil); err == nil {
		_ = json.Unmarshal(raw, &afp)
	}

	return afp
}

func handleLeaderRotationResponse(raw []byte, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *epochRotationState) {

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
		var response websocket_pack.WsLeaderRotationProofResponseOk
		if json.Unmarshal(raw, &response) == nil {
			handleLeaderRotationOk(response, epochHandler, leaderPubKey, epochFullID, state)
		}
	case "UPGRADE":
		var response websocket_pack.WsLeaderRotationProofResponseUpgrade
		if json.Unmarshal(raw, &response) == nil {
			handleLeaderRotationUpgrade(response, epochHandler, leaderPubKey, state)
		}
	}
}

func handleLeaderRotationOk(response websocket_pack.WsLeaderRotationProofResponseOk, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *epochRotationState) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	cache := ensureLeaderRotationCache(state, epochHandler.Id, leaderPubKey)
	firstBlockHash := defaultHash

	if cache.SkipData.Index >= 0 && cache.AfpForFirstBlock.BlockHash != "" {
		firstBlockHash = cache.AfpForFirstBlock.BlockHash
	}

	dataToVerify := strings.Join([]string{"LEADER_ROTATION_PROOF", leaderPubKey, firstBlockHash, strconv.Itoa(cache.SkipData.Index), cache.SkipData.Hash, epochFullID}, ":")

	quorumMap := make(map[string]bool)
	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	if cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig) {
		lowered := strings.ToLower(response.Voter)
		leaderRotationMutex.Lock()
		if quorumMap[lowered] {
			cache.Proofs[response.Voter] = response.Sig
		}
		leaderRotationMutex.Unlock()
	}
}

func handleLeaderRotationUpgrade(response websocket_pack.WsLeaderRotationProofResponseUpgrade, epochHandler *structures.EpochDataHandler, leaderPubKey string, state *epochRotationState) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	if !validateUpgradePayload(response, epochHandler, leaderPubKey) {
		return
	}

	cache := ensureLeaderRotationCache(state, epochHandler.Id, leaderPubKey)

	leaderRotationMutex.Lock()
	cache.SkipData = response.SkipData
	cache.AfpForFirstBlock = response.AfpForFirstBlock
	cache.Proofs = make(map[string]string)
	leaderRotationMutex.Unlock()
}

func validateUpgradePayload(response websocket_pack.WsLeaderRotationProofResponseUpgrade, epochHandler *structures.EpochDataHandler, leaderPubKey string) bool {

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

	if response.AfpForFirstBlock.BlockId != "" {

		parts := strings.Split(response.AfpForFirstBlock.BlockId, ":")

		if len(parts) != 3 || parts[0] != strconv.Itoa(epochHandler.Id) || parts[1] != leaderPubKey || parts[2] != "0" {
			return false
		}

		if !utils.VerifyAggregatedFinalizationProof(&response.AfpForFirstBlock, epochHandler) {
			return false
		}
	}

	return true
}

func persistAggregatedLeaderRotationProof(cache *leaderRotationCache, epochId int, leaderPubKey string) {

	leaderRotationMutex.Lock()
	defer leaderRotationMutex.Unlock()

	firstBlockHash := defaultHash

	if cache.SkipData.Index >= 0 && cache.AfpForFirstBlock.BlockHash != "" {
		firstBlockHash = cache.AfpForFirstBlock.BlockHash
	}

	aggregated := structures.AggregatedLeaderRotationProof{
		FirstBlockHash: firstBlockHash,
		SkipIndex:      cache.SkipData.Index,
		SkipHash:       cache.SkipData.Hash,
		Proofs:         cache.Proofs,
	}

	key := []byte(fmt.Sprintf("ALRP:%d:%s", epochId, leaderPubKey))
	if value, err := json.Marshal(aggregated); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}
}
