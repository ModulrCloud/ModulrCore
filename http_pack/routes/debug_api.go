package routes

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	leveldbutil "github.com/syndtr/goleveldb/leveldb/util"

	"github.com/valyala/fasthttp"
)

type debugProofInfo struct {
	Exists    bool   `json:"exists"`
	BlockId   string `json:"blockId,omitempty"`
	BlockHash string `json:"blockHash,omitempty"`
	EpochId   int    `json:"epochId,omitempty"`
	Proofs    int    `json:"proofs,omitempty"`
}

type debugBlockInfo struct {
	Exists     bool   `json:"exists"`
	BlockId    string `json:"blockId,omitempty"`
	Creator    string `json:"creator,omitempty"`
	Epoch      string `json:"epoch,omitempty"`
	Index      int    `json:"index,omitempty"`
	Hash       string `json:"hash,omitempty"`
	Time       int64  `json:"time,omitempty"`
	AgeMs      int64  `json:"ageMs,omitempty"`
	Txs        int    `json:"txs,omitempty"`
	HashMatch  *bool  `json:"hashMatch,omitempty"`
	ParseError string `json:"parseError,omitempty"`
}

type debugAlfpInfo struct {
	Exists        bool   `json:"exists"`
	Included      bool   `json:"included"`
	Leader        string `json:"leader"`
	ProofIndex    int    `json:"proofIndex"`
	ProofHash     string `json:"proofHash"`
	Signatures    int    `json:"signatures"`
	ProofBlockId  string `json:"proofBlockId,omitempty"`
	ProofBlockAfp bool   `json:"proofBlockAfp"`
}

type debugPodOutboxState struct {
	PendingCount int            `json:"pendingCount"`
	CountsByType map[string]int `json:"countsByType"`
	SampleIds    []string       `json:"sampleIds"`
}

type debugPipelineResponse struct {
	Node struct {
		PublicKey string `json:"publicKey"`
		NowMs     int64  `json:"nowMs"`
	} `json:"node"`
	Approvement struct {
		EpochId                 int      `json:"epochId"`
		EpochHash               string   `json:"epochHash"`
		StartTimestamp          uint64   `json:"startTimestamp"`
		ConfigCurrentLeader     int      `json:"configCurrentLeaderIndex"`
		WallClockLeaderIndex    int      `json:"wallClockLeaderIndex"`
		WallClockEpochProgress  float64  `json:"wallClockEpochProgress"`
		LeadersSequence         []string `json:"leadersSequence"`
		NetworkBlockTimeMs      int64    `json:"networkBlockTimeMs"`
		NetworkEpochDurationMs  int64    `json:"networkEpochDurationMs"`
		NetworkLeadershipTimeMs int64    `json:"networkLeadershipTimeMs"`
	} `json:"approvement"`
	Generation struct {
		EpochFullId string `json:"epochFullId"`
		NextIndex   int    `json:"nextIndex"`
		PrevHash    string `json:"prevHash"`
	} `json:"generation"`
	Finalizer struct {
		EpochId       int                             `json:"epochId"`
		EpochHash     string                          `json:"epochHash"`
		AlignmentData structures.AlignmentDataHandler `json:"alignmentData"`
	} `json:"finalizer"`
	ALFP struct {
		ProgressEpoch int             `json:"progressEpoch"`
		ProbeEpoch    int             `json:"probeEpoch"`
		ByLeader      []debugAlfpInfo `json:"byLeader"`
	} `json:"alfp"`
	LastMile struct {
		Tracker               *utils.LastMileSequenceState      `json:"tracker"`
		AHPCollectorTracker   *utils.LastMileSequenceState      `json:"ahpCollectorTracker"`
		AHPCollectorLag       int64                             `json:"ahpCollectorLag"`
		CurrentBlockId        string                            `json:"currentBlockId"`
		CurrentBlock          debugBlockInfo                    `json:"currentBlock"`
		CurrentBlockConfirmed bool                              `json:"currentBlockConfirmed"`
		PreviousAHP           debugProofInfo                    `json:"previousAhp"`
		CurrentAHP            debugProofInfo                    `json:"currentAhp"`
		Boundary              *structures.LastMileEpochBoundary `json:"boundary"`
	} `json:"lastMile"`
	Execution struct {
		EpochId               int             `json:"epochId"`
		LastExecutedHeight    int64           `json:"lastExecutedHeight"`
		NextHeight            int64           `json:"nextHeight"`
		LastExecutedBlock     debugBlockInfo  `json:"lastExecutedBlock"`
		NextHeightProbe       debugHeightCore `json:"nextHeightProbe"`
		NextPlusOneAHP        debugProofInfo  `json:"nextPlusOneAhp"`
		EpochRotationProofNow debugProofInfo  `json:"epochRotationProofForCurrentEpoch"`
	} `json:"execution"`
	PoDOutbox debugPodOutboxState `json:"podOutbox"`
}

type debugHeightCore struct {
	Height                   int64          `json:"height"`
	LastMileMappedBlockId    string         `json:"lastMileMappedBlockId"`
	LastMileHeightInEpoch    *int           `json:"lastMileHeightInEpoch,omitempty"`
	ExecutedStateBlockId     string         `json:"executedStateBlockId,omitempty"`
	AHP                      debugProofInfo `json:"ahp"`
	BlockFromAHP             debugBlockInfo `json:"blockFromAhp"`
	BlockFromLastMileMap     debugBlockInfo `json:"blockFromLastMileMap"`
	CanExecutionApplyNow     bool           `json:"canExecutionApplyNow"`
	ExecutionBlocker         string         `json:"executionBlocker,omitempty"`
	SignHeightProofReadiness string         `json:"signHeightProofReadiness"`
}

type debugHeightProbeResponse struct {
	NodePublicKey string          `json:"nodePublicKey"`
	Core          debugHeightCore `json:"core"`
	ExecutionNext int64           `json:"executionNext"`
	LastMileNext  int64           `json:"lastMileNext"`
	AHPNext       int64           `json:"ahpNext"`
}

type debugLeaderPipelineResponse struct {
	NodePublicKey  string `json:"nodePublicKey"`
	EpochId        int    `json:"epochId"`
	LeaderIndex    int    `json:"leaderIndex"`
	Leader         string `json:"leader"`
	LeaderStartMs  int64  `json:"leaderStartMs"`
	LeaderEndMs    int64  `json:"leaderEndMs"`
	LeaderFinished bool   `json:"leaderFinished"`
	LocalBlocks    struct {
		Count      int            `json:"count"`
		Highest    debugBlockInfo `json:"highest"`
		HighestAFP debugProofInfo `json:"highestAfp"`
		NextAFP    debugProofInfo `json:"nextAfp"`
	} `json:"localBlocks"`
	ALFP             debugAlfpInfo                `json:"alfp"`
	Alignment        *structures.ExecutionStats   `json:"alignment,omitempty"`
	LastMileTracker  *utils.LastMileSequenceState `json:"lastMileTracker"`
	AHPTracker       *utils.LastMileSequenceState `json:"ahpTracker"`
	LastMileRelation string                       `json:"lastMileRelation"`
	AHPRelation      string                       `json:"ahpRelation"`
}

func GetDebugPipelineState(ctx *fasthttp.RequestCtx) {
	now := utils.GetUTCTimestampInMilliSeconds()

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	approvement := handlers.APPROVEMENT_THREAD_METADATA.Handler
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	finalizer := handlers.FINALIZER_THREAD_METADATA.Handler
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	cursor := handlers.EXECUTION_THREAD_METADATA.ChainCursor
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)
	ahpTracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileAHPCollectorTracker)
	response := debugPipelineResponse{}
	response.Node.PublicKey = globals.CONFIGURATION.PublicKey
	response.Node.NowMs = now

	epoch := approvement.EpochDataHandler
	network := approvement.NetworkParameters
	response.Approvement.EpochId = epoch.Id
	response.Approvement.EpochHash = epoch.Hash
	response.Approvement.StartTimestamp = epoch.StartTimestamp
	response.Approvement.ConfigCurrentLeader = epoch.CurrentLeaderIndex
	response.Approvement.WallClockLeaderIndex = wallClockLeaderIndex(epoch, network, now)
	response.Approvement.WallClockEpochProgress = wallClockEpochProgress(epoch, network, now)
	response.Approvement.LeadersSequence = append([]string(nil), epoch.LeadersSequence...)
	response.Approvement.NetworkBlockTimeMs = network.BlockTime
	response.Approvement.NetworkEpochDurationMs = network.EpochDuration
	response.Approvement.NetworkLeadershipTimeMs = network.LeadershipDuration

	response.Generation.EpochFullId = handlers.GENERATION_THREAD_METADATA.EpochFullId
	response.Generation.NextIndex = handlers.GENERATION_THREAD_METADATA.NextIndex
	response.Generation.PrevHash = handlers.GENERATION_THREAD_METADATA.PrevHash

	response.Finalizer.EpochId = finalizer.EpochDataHandler.Id
	response.Finalizer.EpochHash = finalizer.EpochDataHandler.Hash
	response.Finalizer.AlignmentData = finalizer.SequenceAlignmentData

	alfpProbeEpoch := finalizer.EpochDataHandler.Id
	if finalizer.EpochDataHandler.Hash == "" {
		alfpProbeEpoch = epoch.Id
	}
	response.ALFP.ProgressEpoch = loadAlfpProgressDebug()
	response.ALFP.ProbeEpoch = alfpProbeEpoch
	if epochHandler := getDebugEpochHandler(alfpProbeEpoch); epochHandler != nil {
		response.ALFP.ByLeader = buildDebugAlfpByLeader(epochHandler)
	}

	response.LastMile.Tracker = tracker
	response.LastMile.AHPCollectorTracker = ahpTracker
	response.LastMile.AHPCollectorLag = tracker.NextHeight - ahpTracker.NextHeight
	response.LastMile.Boundary = utils.LoadLastMileEpochBoundary(tracker.EpochId)
	response.LastMile.CurrentBlockId = lastMileTrackerBlockId(tracker)
	response.LastMile.CurrentBlock = debugLoadBlock(response.LastMile.CurrentBlockId, "")
	response.LastMile.CurrentBlockConfirmed = debugBlockConfirmedForLastMile(tracker)
	response.LastMile.PreviousAHP = debugLoadAHP(tracker.NextHeight - 1)
	response.LastMile.CurrentAHP = debugLoadAHP(tracker.NextHeight)

	response.Execution.EpochId = cursor.EpochDataHandler.Id
	response.Execution.LastExecutedHeight = cursor.LastExecutedLocalHeight
	response.Execution.NextHeight = cursor.LastExecutedLocalHeight + 1
	response.Execution.LastExecutedBlock = debugLoadExecutedBlock(cursor.LastExecutedLocalHeight)
	response.Execution.NextHeightProbe = buildDebugHeightCore(cursor.LastExecutedLocalHeight+1, &cursor)
	response.Execution.NextPlusOneAHP = debugLoadAHP(cursor.LastExecutedLocalHeight + 2)
	response.Execution.EpochRotationProofNow = debugLoadAERP(cursor.EpochDataHandler.Id)

	response.PoDOutbox = buildDebugPodOutboxState()

	helpers.WriteJSON(ctx, fasthttp.StatusOK, response)
}

func GetDebugHeightProbe(ctx *fasthttp.RequestCtx) {
	heightStr, ok := ctx.UserValue("height").(string)
	if !ok || heightStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}
	height, err := strconv.ParseInt(heightStr, 10, 64)
	if err != nil || height < 0 {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	cursor := handlers.EXECUTION_THREAD_METADATA.ChainCursor
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()
	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)
	ahpTracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileAHPCollectorTracker)

	helpers.WriteJSON(ctx, fasthttp.StatusOK, debugHeightProbeResponse{
		NodePublicKey: globals.CONFIGURATION.PublicKey,
		Core:          buildDebugHeightCore(height, &cursor),
		ExecutionNext: cursor.LastExecutedLocalHeight + 1,
		LastMileNext:  tracker.NextHeight,
		AHPNext:       ahpTracker.NextHeight,
	})
}

func GetDebugLeaderPipeline(ctx *fasthttp.RequestCtx) {
	epochStr, ok := ctx.UserValue("epoch").(string)
	if !ok || epochStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epoch")
		return
	}
	leaderIndexStr, ok := ctx.UserValue("leaderIndex").(string)
	if !ok || leaderIndexStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid leaderIndex")
		return
	}
	epochId, err := strconv.Atoi(epochStr)
	if err != nil || epochId < 0 {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epoch")
		return
	}
	leaderIndex, err := strconv.Atoi(leaderIndexStr)
	if err != nil || leaderIndex < 0 {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid leaderIndex")
		return
	}

	epochHandler := getDebugEpochHandler(epochId)
	if epochHandler == nil || leaderIndex >= len(epochHandler.LeadersSequence) {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Epoch or leader not found")
		return
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	network := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	leader := epochHandler.LeadersSequence[leaderIndex]
	highest, count := debugFindHighestLocalBlock(epochId, leader)
	nextAfpBlockId := ""
	if highest.Exists {
		nextAfpBlockId = fmt.Sprintf("%d:%s:%d", epochId, leader, highest.Index+1)
	}

	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	alignment, hasAlignment := handlers.FINALIZER_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[leader]
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)
	ahpTracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileAHPCollectorTracker)
	response := debugLeaderPipelineResponse{
		NodePublicKey:    globals.CONFIGURATION.PublicKey,
		EpochId:          epochId,
		LeaderIndex:      leaderIndex,
		Leader:           leader,
		LeaderStartMs:    int64(epochHandler.StartTimestamp) + int64(leaderIndex)*network.LeadershipDuration,
		LeaderEndMs:      int64(epochHandler.StartTimestamp) + int64(leaderIndex+1)*network.LeadershipDuration,
		LeaderFinished:   utils.GetUTCTimestampInMilliSeconds() >= int64(epochHandler.StartTimestamp)+int64(leaderIndex+1)*network.LeadershipDuration,
		ALFP:             debugLoadALFP(epochId, leader),
		LastMileTracker:  tracker,
		AHPTracker:       ahpTracker,
		LastMileRelation: describeLastMileRelation(tracker, epochId, leaderIndex),
		AHPRelation:      describeLastMileRelation(ahpTracker, epochId, leaderIndex),
	}
	response.LocalBlocks.Count = count
	response.LocalBlocks.Highest = highest
	if highest.Exists {
		response.LocalBlocks.HighestAFP = debugLoadAFP(highest.BlockId)
	}
	if nextAfpBlockId != "" {
		response.LocalBlocks.NextAFP = debugLoadAFP(nextAfpBlockId)
	}
	if hasAlignment {
		alignmentCopy := alignment
		response.Alignment = &alignmentCopy
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, response)
}

func GetDebugPodOutboxState(ctx *fasthttp.RequestCtx) {
	helpers.WriteJSON(ctx, fasthttp.StatusOK, buildDebugPodOutboxState())
}

func buildDebugHeightCore(height int64, cursor *structures.ChainCursor) debugHeightCore {
	mappedBlockId := utils.LoadHeightBlockIdMapping(height)
	heightInEpoch, hasHeightInEpoch := utils.LoadHeightInEpochMapping(height)
	executedStateBlockId := debugLoadStateBlockId(height)
	ahp := debugLoadAHP(height)

	core := debugHeightCore{
		Height:                height,
		LastMileMappedBlockId: mappedBlockId,
		ExecutedStateBlockId:  executedStateBlockId,
		AHP:                   ahp,
		BlockFromAHP:          debugLoadBlock(ahp.BlockId, ahp.BlockHash),
		BlockFromLastMileMap:  debugLoadBlock(mappedBlockId, ""),
	}
	if hasHeightInEpoch {
		core.LastMileHeightInEpoch = &heightInEpoch
	}

	if mappedBlockId == "" {
		core.SignHeightProofReadiness = "missing_last_mile_height_mapping"
	} else if !hasHeightInEpoch {
		core.SignHeightProofReadiness = "missing_height_in_epoch_mapping"
	} else if !core.BlockFromLastMileMap.Exists {
		core.SignHeightProofReadiness = "missing_block_body_for_mapped_block"
	} else {
		core.SignHeightProofReadiness = "ready"
	}

	nextHeight := cursor.LastExecutedLocalHeight + 1
	switch {
	case height != nextHeight:
		core.ExecutionBlocker = fmt.Sprintf("not_execution_next_height:%d", nextHeight)
	case !ahp.Exists:
		core.ExecutionBlocker = "missing_aggregated_height_proof"
	case !debugLoadAHP(height + 1).Exists:
		core.ExecutionBlocker = "missing_next_aggregated_height_proof"
	case !core.BlockFromAHP.Exists:
		core.ExecutionBlocker = "missing_block_body"
	case ahp.EpochId > cursor.EpochDataHandler.Id && !debugLoadAERP(cursor.EpochDataHandler.Id).Exists:
		core.ExecutionBlocker = "missing_epoch_rotation_proof"
	default:
		core.CanExecutionApplyNow = true
	}

	return core
}

func buildDebugAlfpByLeader(epochHandler *structures.EpochDataHandler) []debugAlfpInfo {
	out := make([]debugAlfpInfo, 0, len(epochHandler.LeadersSequence))
	for _, leader := range epochHandler.LeadersSequence {
		out = append(out, debugLoadALFP(epochHandler.Id, leader))
	}
	return out
}

func wallClockLeaderIndex(epoch structures.EpochDataHandler, network structures.NetworkParameters, now int64) int {
	if network.LeadershipDuration <= 0 || len(epoch.LeadersSequence) == 0 || now < int64(epoch.StartTimestamp) {
		return 0
	}
	idx := int((now - int64(epoch.StartTimestamp)) / network.LeadershipDuration)
	if idx > len(epoch.LeadersSequence) {
		return len(epoch.LeadersSequence)
	}
	return idx
}

func wallClockEpochProgress(epoch structures.EpochDataHandler, network structures.NetworkParameters, now int64) float64 {
	if network.EpochDuration <= 0 {
		return 0
	}
	return float64(now-int64(epoch.StartTimestamp)) / float64(network.EpochDuration)
}

func getDebugEpochHandler(epochId int) *structures.EpochDataHandler {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	if handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.Id == epochId {
		epoch := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return &epoch
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	if handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler.Id == epochId &&
		handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler.Hash != "" {
		epoch := handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler
		handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()
		return &epoch
	}
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	if handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochDataHandler.Id == epochId {
		epoch := handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochDataHandler
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()
		return &epoch
	}
	absoluteEpochId := epochId + handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochOffset
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if snapshot := utils.GetEpochSnapshot(absoluteEpochId); snapshot != nil {
		return &snapshot.EpochDataHandler
	}
	if absoluteEpochId != epochId {
		if snapshot := utils.GetEpochSnapshot(epochId); snapshot != nil {
			return &snapshot.EpochDataHandler
		}
	}
	return nil
}

func loadAlfpProgressDebug() int {
	raw, err := databases.FINALIZATION_THREAD_METADATA.Get([]byte(constants.DBKeyAlfpProgress), nil)
	if err != nil {
		return 0
	}
	value, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0
	}
	return value
}

func debugLoadAHP(height int64) debugProofInfo {
	if height < 0 {
		return debugProofInfo{}
	}
	proof := utils.LoadAggregatedHeightProof(int(height))
	if proof == nil {
		return debugProofInfo{}
	}
	return debugProofInfo{
		Exists:    true,
		BlockId:   proof.BlockId,
		BlockHash: proof.BlockHash,
		EpochId:   proof.EpochId,
		Proofs:    len(proof.Proofs),
	}
}

func debugLoadAFP(blockId string) debugProofInfo {
	if blockId == "" {
		return debugProofInfo{}
	}
	raw, err := databases.EPOCH_DATA.Get([]byte(constants.DBKeyPrefixAfp+blockId), nil)
	if err != nil {
		return debugProofInfo{}
	}
	var proof structures.AggregatedFinalizationProof
	if json.Unmarshal(raw, &proof) != nil {
		return debugProofInfo{}
	}
	return debugProofInfo{
		Exists:    true,
		BlockId:   proof.BlockId,
		BlockHash: proof.BlockHash,
		Proofs:    len(proof.Proofs),
	}
}

func debugLoadAERP(epochId int) debugProofInfo {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, epochId))
	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return debugProofInfo{}
	}
	var proof structures.AggregatedEpochRotationProof
	if json.Unmarshal(raw, &proof) != nil {
		return debugProofInfo{}
	}
	return debugProofInfo{
		Exists:    true,
		EpochId:   proof.EpochId,
		BlockId:   proof.FinishedOnBlockId,
		BlockHash: proof.FinishedOnHash,
		Proofs:    len(proof.Proofs),
	}
}

func debugLoadALFP(epochId int, leader string) debugAlfpInfo {
	info := debugAlfpInfo{
		Leader:   leader,
		Included: utils.HasAnyAlfpIncluded(epochId, leader),
	}
	key := []byte(fmt.Sprintf("%s%d:%s", constants.DBKeyPrefixAlfp, epochId, leader))
	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return info
	}
	var proof structures.AggregatedLeaderFinalizationProof
	if json.Unmarshal(raw, &proof) != nil {
		return info
	}
	info.Exists = true
	info.ProofIndex = proof.VotingStat.Index
	info.ProofHash = proof.VotingStat.Hash
	info.Signatures = len(proof.Signatures)
	info.ProofBlockId = proof.VotingStat.Afp.BlockId
	info.ProofBlockAfp = proof.VotingStat.Afp.BlockId != ""
	return info
}

func debugLoadBlock(blockId, expectedHash string) debugBlockInfo {
	if blockId == "" {
		return debugBlockInfo{}
	}
	raw, err := databases.BLOCKS.Get([]byte(blockId), nil)
	if err != nil || len(raw) == 0 {
		return debugBlockInfo{BlockId: blockId}
	}
	var block block_pack.Block
	if err := json.Unmarshal(raw, &block); err != nil {
		return debugBlockInfo{BlockId: blockId, ParseError: err.Error()}
	}
	hash := block.GetHashForNetwork(globals.GENESIS.NetworkId)
	info := debugBlockInfo{
		Exists:  true,
		BlockId: blockId,
		Creator: block.Creator,
		Epoch:   block.Epoch,
		Index:   block.Index,
		Hash:    hash,
		Time:    block.Time,
		AgeMs:   utils.GetUTCTimestampInMilliSeconds() - block.Time,
		Txs:     len(block.Transactions),
	}
	if expectedHash != "" {
		match := hash == expectedHash
		info.HashMatch = &match
	}
	return info
}

func debugLoadStateBlockId(height int64) string {
	if height < 0 {
		return ""
	}
	raw, err := databases.STATE.Get([]byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixBlockIndex, height)), nil)
	if err != nil {
		return ""
	}
	return string(raw)
}

func debugLoadExecutedBlock(height int64) debugBlockInfo {
	return debugLoadBlock(debugLoadStateBlockId(height), "")
}

func lastMileTrackerBlockId(tracker *utils.LastMileSequenceState) string {
	if tracker == nil {
		return ""
	}
	epochHandler := getDebugEpochHandler(tracker.EpochId)
	if epochHandler == nil || tracker.LeaderIndex < 0 || tracker.LeaderIndex >= len(epochHandler.LeadersSequence) {
		return ""
	}
	return fmt.Sprintf("%d:%s:%d", tracker.EpochId, epochHandler.LeadersSequence[tracker.LeaderIndex], tracker.BlockIndex)
}

func debugBlockConfirmedForLastMile(tracker *utils.LastMileSequenceState) bool {
	blockId := lastMileTrackerBlockId(tracker)
	if blockId == "" {
		return false
	}
	epochHandler := getDebugEpochHandler(tracker.EpochId)
	if epochHandler == nil {
		return false
	}
	nextBlockId := strings.TrimSuffix(blockId, fmt.Sprintf(":%d", tracker.BlockIndex)) + fmt.Sprintf(":%d", tracker.BlockIndex+1)
	return utils.HasLocalVerifiedAfp(nextBlockId, epochHandler)
}

func debugFindHighestLocalBlock(epochId int, leader string) (debugBlockInfo, int) {
	prefix := fmt.Sprintf("%d:%s:", epochId, leader)
	it := databases.BLOCKS.NewIterator(leveldbutil.BytesPrefix([]byte(prefix)), nil)
	defer it.Release()

	highestIndex := -1
	highestBlockId := ""
	count := 0
	for it.Next() {
		blockId := string(it.Key())
		parsedIndex, ok := parseDebugBlockIndex(blockId)
		if !ok {
			continue
		}
		count++
		if parsedIndex > highestIndex {
			highestIndex = parsedIndex
			highestBlockId = blockId
		}
	}
	return debugLoadBlock(highestBlockId, ""), count
}

func parseDebugBlockIndex(blockId string) (int, bool) {
	parts := strings.Split(blockId, ":")
	if len(parts) != 3 {
		return 0, false
	}
	idx, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, false
	}
	return idx, true
}

func describeLastMileRelation(tracker *utils.LastMileSequenceState, epochId int, leaderIndex int) string {
	if tracker == nil {
		return "tracker_missing"
	}
	if tracker.EpochId < epochId {
		return "last_mile_before_epoch"
	}
	if tracker.EpochId > epochId {
		return "last_mile_after_epoch"
	}
	if tracker.LeaderIndex < leaderIndex {
		return "last_mile_before_leader"
	}
	if tracker.LeaderIndex > leaderIndex {
		return "last_mile_after_leader"
	}
	return "last_mile_on_leader"
}

func buildDebugPodOutboxState() debugPodOutboxState {
	state := debugPodOutboxState{
		CountsByType: make(map[string]int),
		SampleIds:    make([]string, 0, 20),
	}
	it := databases.FINALIZATION_THREAD_METADATA.NewIterator(leveldbutil.BytesPrefix([]byte(constants.DBKeyPrefixPodOutbox)), nil)
	defer it.Release()

	for it.Next() {
		id := strings.TrimPrefix(string(it.Key()), constants.DBKeyPrefixPodOutbox)
		state.PendingCount++
		state.CountsByType[classifyDebugOutboxId(id)]++
		if len(state.SampleIds) < 20 {
			state.SampleIds = append(state.SampleIds, id)
		}
	}
	return state
}

func classifyDebugOutboxId(id string) string {
	switch {
	case strings.HasPrefix(id, "CORE_BLOCK:"):
		return "CORE_BLOCK"
	case strings.HasPrefix(id, constants.DBKeyPrefixAlfp):
		return "ALFP"
	case strings.HasPrefix(id, constants.DBKeyPrefixAggregatedHeightProof):
		return "AHP"
	case strings.HasPrefix(id, constants.DBKeyPrefixAggregatedEpochRotationProof):
		return "AERP"
	case strings.HasPrefix(id, constants.DBKeyPrefixAggregatedAnchorEpochAckProof):
		return "ANCHOR_ACK"
	case strings.HasPrefix(id, constants.DBKeyPrefixEpochAnnouncementProof):
		return "EPOCH_ANNOUNCEMENT"
	default:
		return "OTHER"
	}
}
