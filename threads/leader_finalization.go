package threads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/gorilla/websocket"
)

type LeaderFinalizationCache struct {
	SkipData structures.VotingStat
	Proofs   map[string]string
	// Timestamp of the last broadcast to anchors to avoid spamming
	LastBroadcasted time.Time
}

type EpochRotationState struct {
	caches  map[string]*LeaderFinalizationCache
	wsConns map[string]*websocket.Conn
	waiter  *utils.QuorumWaiter
}

var (
	LEADER_FINALIZATION_MUTEX  = sync.Mutex{}
	LEADER_FINALIZATION_STATES = make(map[int]*EpochRotationState)
	PROCESSING_EPOCH_INDEX     = -1
)

func LeaderFinalizationThread() {

	for {

		if PROCESSING_EPOCH_INDEX == -1 {
			PROCESSING_EPOCH_INDEX = loadFinalizationProgress()
		}

		processingHandler := getOrLoadEpochHandler(PROCESSING_EPOCH_INDEX)

		if processingHandler == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if !weAreInEpochQuorum(processingHandler) {
			currentEpoch := PROCESSING_EPOCH_INDEX
			PROCESSING_EPOCH_INDEX++
			persistFinalizationProgress(PROCESSING_EPOCH_INDEX)
			cleanupLeaderFinalizationState(currentEpoch)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		state := ensureLeaderFinalizationState(processingHandler)
		majority := utils.GetQuorumMajority(processingHandler)

		networkParams := getNetworkParameters()

		for _, leaderIndex := range leadersReadyForAlfp(processingHandler, &networkParams) {
			tryCollectLeaderFinalizationProofs(processingHandler, leaderIndex, majority, state)
		}

		if allLeaderFinalizationProofsCollected(processingHandler) {
			currentEpoch := PROCESSING_EPOCH_INDEX
			PROCESSING_EPOCH_INDEX++
			persistFinalizationProgress(PROCESSING_EPOCH_INDEX)
			cleanupLeaderFinalizationState(currentEpoch)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func ensureLeaderFinalizationState(epochHandler *structures.EpochDataHandler) *EpochRotationState {

	LEADER_FINALIZATION_MUTEX.Lock()
	defer LEADER_FINALIZATION_MUTEX.Unlock()

	if state, ok := LEADER_FINALIZATION_STATES[epochHandler.Id]; ok {
		return state
	}

	state := &EpochRotationState{
		caches:  make(map[string]*LeaderFinalizationCache),
		wsConns: make(map[string]*websocket.Conn),
		waiter:  utils.NewQuorumWaiter(len(epochHandler.Quorum)),
	}

	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, state.wsConns)
	LEADER_FINALIZATION_STATES[epochHandler.Id] = state

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

	if raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte("ALFP_PROGRESS"), nil); err == nil {
		if idx, convErr := strconv.Atoi(string(raw)); convErr == nil {
			return idx
		}
	}

	return 0
}

func persistFinalizationProgress(epochId int) {

	_ = databases.FINALIZATION_VOTING_STATS.Put([]byte("ALFP_PROGRESS"), []byte(strconv.Itoa(epochId)), nil)
}

func cleanupLeaderFinalizationState(epochId int) {

	LEADER_FINALIZATION_MUTEX.Lock()
	defer LEADER_FINALIZATION_MUTEX.Unlock()

	state, ok := LEADER_FINALIZATION_STATES[epochId]
	if !ok {
		return
	}

	for _, conn := range state.wsConns {
		if conn != nil {
			_ = conn.Close()
		}
	}

	delete(LEADER_FINALIZATION_STATES, epochId)
}

func getNetworkParameters() structures.NetworkParameters {

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	params := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	return params
}

func weAreInEpochQuorum(epochHandler *structures.EpochDataHandler) bool {

	for _, quorumMember := range epochHandler.Quorum {
		if strings.EqualFold(quorumMember, globals.CONFIGURATION.PublicKey) {
			return true
		}
	}

	return false
}

func leadersReadyForAlfp(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters) []int {

	ready := make([]int, 0)

	for idx := range epochHandler.LeadersSequence {

		if leaderFinalizationConfirmedByAlignment(epochHandler.Id, epochHandler.LeadersSequence[idx]) {
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

func loadAggregatedLeaderFinalizationProof(epochId int, leader string) *structures.AggregatedLeaderFinalizationProof {

	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leader))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		return nil
	}

	var proof structures.AggregatedLeaderFinalizationProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}

	return &proof
}

func leaderFinalizationConfirmedByAlignment(epochId int, leader string) bool {

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id != epochId {
		return false
	}

	_, exists := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[leader]
	return exists
}

func allLeaderFinalizationProofsCollected(epochHandler *structures.EpochDataHandler) bool {

	for _, leader := range epochHandler.LeadersSequence {
		if !leaderHasAlfp(epochHandler.Id, leader) {
			return false
		}

		if executionMetadataMatches(epochHandler.Id) && !leaderFinalizationConfirmedByAlignment(epochHandler.Id, leader) {
			return false
		}
	}

	return true
}

func executionMetadataMatches(epochId int) bool {

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	return handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id == epochId
}

func leaderTimeIsOut(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters, leaderIndex int) bool {

	leadershipTimeframe := networkParams.LeadershipDuration
	return utils.GetUTCTimestampInMilliSeconds() >= int64(epochHandler.StartTimestamp)+(int64(leaderIndex)+1)*leadershipTimeframe
}

func tryCollectLeaderFinalizationProofs(epochHandler *structures.EpochDataHandler, leaderIndex, majority int, state *EpochRotationState) {

	leaderPubKey := epochHandler.LeadersSequence[leaderIndex]
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	if leaderFinalizationConfirmedByAlignment(epochHandler.Id, leaderPubKey) {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	if leaderHasAlfp(epochHandler.Id, leaderPubKey) || state.waiter == nil {
		if aggregated := loadAggregatedLeaderFinalizationProof(epochHandler.Id, leaderPubKey); aggregated != nil && shouldBroadcastLeaderFinalization(cache) {
			markLeaderFinalizationBroadcast(cache)
			sendAggregatedLeaderFinalizationProofToAnchors(aggregated)
		}
		return
	}

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

	LEADER_FINALIZATION_MUTEX.Lock()
	proofsCount := len(cache.Proofs)
	LEADER_FINALIZATION_MUTEX.Unlock()

	if proofsCount >= majority {
		persistAggregatedLeaderFinalizationProof(cache, epochHandler.Id, leaderPubKey)
	}
}

func ensureLeaderFinalizationCache(state *EpochRotationState, epochId int, leaderPubKey string) *LeaderFinalizationCache {

	key := fmt.Sprintf("%d:%s", epochId, leaderPubKey)

	LEADER_FINALIZATION_MUTEX.Lock()
	defer LEADER_FINALIZATION_MUTEX.Unlock()

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
		LEADER_FINALIZATION_MUTEX.Lock()
		if quorumMap[lowered] {
			cache.Proofs[response.Voter] = response.Sig
		}
		LEADER_FINALIZATION_MUTEX.Unlock()
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

	LEADER_FINALIZATION_MUTEX.Lock()
	cache.SkipData = response.SkipData
	cache.Proofs = make(map[string]string)
	LEADER_FINALIZATION_MUTEX.Unlock()
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

	LEADER_FINALIZATION_MUTEX.Lock()

	proofsCopy := make(map[string]string, len(cache.Proofs))
	for voter, sig := range cache.Proofs {
		proofsCopy[voter] = sig
	}

	aggregated := structures.AggregatedLeaderFinalizationProof{
		EpochIndex: epochId,
		Leader:     leaderPubKey,
		VotingStat: structures.VotingStat{
			Index: cache.SkipData.Index,
			Hash:  cache.SkipData.Hash,
			Afp:   cache.SkipData.Afp,
		},
		Signatures: proofsCopy,
	}

	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leaderPubKey))
	if value, err := json.Marshal(aggregated); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}

	LEADER_FINALIZATION_MUTEX.Unlock()

	markLeaderFinalizationBroadcast(cache)
	sendAggregatedLeaderFinalizationProofToAnchors(&aggregated)
}

func shouldBroadcastLeaderFinalization(cache *LeaderFinalizationCache) bool {

	LEADER_FINALIZATION_MUTEX.Lock()
	defer LEADER_FINALIZATION_MUTEX.Unlock()

	return time.Since(cache.LastBroadcasted) > time.Second
}

func markLeaderFinalizationBroadcast(cache *LeaderFinalizationCache) {

	LEADER_FINALIZATION_MUTEX.Lock()
	cache.LastBroadcasted = time.Now()
	LEADER_FINALIZATION_MUTEX.Unlock()
}

func sendAggregatedLeaderFinalizationProofToAnchors(aggregated *structures.AggregatedLeaderFinalizationProof) {

	if aggregated == nil {
		return
	}

	payload := structures.AcceptLeaderFinalizationProofRequest{
		LeaderFinalizations: []structures.AggregatedLeaderFinalizationProof{*aggregated},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, anchor := range globals.ANCHORS {

		go func(anchor structures.Anchor) {

			req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/accept_aggregated_leader_finalization_proof", anchor.AnchorUrl), bytes.NewReader(body))
			if err != nil {
				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				return
			}

			resp.Body.Close()
		}(anchor)
	}
}
