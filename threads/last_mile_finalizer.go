// Threads for local last-mile sequencing, height attestations, and epoch rotation proofs.
package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/gorilla/websocket"
)

const (
	LAST_MILE_FINALIZERS_COUNT = 5

	LAST_MILE_AHP_COLLECTION_WINDOW  = 8
	LAST_MILE_AHP_COLLECTION_WORKERS = 3
	LAST_MILE_AHP_RETRY_BACKOFF_MIN  = 500 * time.Millisecond
	LAST_MILE_AHP_RETRY_BACKOFF_MAX  = 5 * time.Second
)

var (
	LAST_MILE_MUTEX           sync.Mutex
	LAST_MILE_ANCHOR_WS_CONNS = make(map[string]*websocket.Conn)

	LAST_MILE_EPOCH_HANDLERS_MUTEX sync.RWMutex
	LAST_MILE_EPOCH_HANDLERS       = make(map[int]structures.EpochDataHandler)
)

type lastMileAHPCollectionJob struct {
	Height        int64
	BlockId       string
	HeightInEpoch int
	BlockParts    lastMileBlockIdParts
	EpochHandler  *structures.EpochDataHandler
}

type lastMileAHPCollectionResult struct {
	Job       lastMileAHPCollectionJob
	Proof     *structures.AggregatedHeightProof
	BlockHash string
}

type lastMileBlockIdParts struct {
	EpochId int
	Creator string
	Index   int
}

type lastMileAHPQuorumClient struct {
	EpochId int
	Conns   map[string]*websocket.Conn
	Waiter  *utils.QuorumWaiter
	Guards  *utils.WebsocketGuards
}

type lastMileAHPRuntime struct {
	WorkerClients []*lastMileAHPQuorumClient
	BlockedHeight int64
	Failures      int
	RetryAfter    time.Time
}

// lastMileAfpBackfillInterval rate-limits PoD backfill requests for a missing
// next-block AFP so a stuck leader boundary doesn't hammer PoD every loop tick.
const lastMileAfpBackfillInterval = 750 * time.Millisecond

// backfillNextBlockAfpFromPoD fetches the AFP that finalizes nextBlockId from
// PoD and persists it locally so the finalizer can confirm the current block.
// PoD's GetBlockWithAfp(currentBlockId) returns the AFP stored under
// currentBlockId+1 (== nextBlockId), i.e. exactly the proof needed to confirm
// the current block. Returns true only when a verified AFP was persisted.
func backfillNextBlockAfpFromPoD(currentBlockId, nextBlockId string, epochHandler *structures.EpochDataHandler) bool {
	if epochHandler == nil {
		return false
	}

	resp := getBlockAndAfpFromPoD(currentBlockId)
	if resp == nil || resp.Afp == nil || resp.Afp.BlockId != nextBlockId {
		return false
	}

	if !utils.VerifyAggregatedFinalizationProof(resp.Afp, epochHandler) {
		return false
	}

	afpBytes, err := json.Marshal(resp.Afp)
	if err != nil {
		return false
	}

	if err := databases.EPOCH_DATA.Put([]byte(constants.DBKeyPrefixAfp+nextBlockId), afpBytes, nil); err != nil {
		return false
	}

	utils.LogWithTimeThrottled(
		"last_mile:afp_backfill:"+nextBlockId,
		2*time.Second,
		fmt.Sprintf("Last mile sequencer: backfilled missing AFP %s from PoD (self-healed propagation gap)", nextBlockId),
		utils.GREEN_COLOR,
	)

	return true
}

// LastMileFinalizerThread runs on ALL quorum member nodes.
// It walks through blocks in leader order (exactly like block_execution.go on main),
// verifies each block via AFP / SequenceAlignmentData, and writes
// LAST_MILE_HEIGHT_MAP:<height> => blockId into the local DB (used by SignHeightProof).
//
// Selected finalizers also handle epoch rotation proofs. A separate collector
// follows this local sequence to collect AggregatedHeightProof signatures.
func LastMileFinalizerThread() {
	lastProcessedEpoch := -1
	isFinalizer := false
	anchorConnectionsSent := false
	lastRotationEpoch := -1

	// Throttles per-blockId PoD backfill attempts for missing next-block AFPs.
	afpBackfillAttempts := make(map[string]time.Time)

	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)

	for {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		epochSnapshot := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		rememberLastMileEpochHandler(&epochSnapshot)

		if epochSnapshot.Id != lastProcessedEpoch {
			lastProcessedEpoch = epochSnapshot.Id
			isFinalizer = iAmLastMileFinalizer(&epochSnapshot)
			anchorConnectionsSent = false

			if isFinalizer {
				selected := selectLastMileFinalizersForEpoch(&epochSnapshot)
				utils.LogWithTime(
					fmt.Sprintf("Last mile finalizer: epoch %d => selected %d from quorum %v", epochSnapshot.Id, len(selected), selected),
					utils.CYAN_COLOR,
				)
			}
		}

		// Advance the rotation cursor from AERPs that ALREADY EXIST locally,
		// regardless of which node produced them. The finalizer subset is
		// reshuffled every epoch (selectLastMileFinalizersForEpoch), so the node
		// that produced AERP (e-1)->e is usually NOT a finalizer for e->(e+1).
		// Tracking rotation progress purely by local production therefore
		// deadlocks once LAST_MILE_FINALIZERS_COUNT < quorum size: no single node
		// is eligible to produce the next proof. Every node already persists each
		// AERP (replicated via PoD/quorum during epoch rotation), so observing the
		// stored proof is enough to keep the cursor moving.
		for LoadAggregatedEpochRotationProof(lastRotationEpoch+1) != nil {
			lastRotationEpoch++
		}

		// --- Finalizer-only: epoch rotation proof collection ---
		if epochSnapshot.Id > 0 {
			prevEpochId := lastRotationEpoch + 1
			if utils.HasLocallySequencedFullEpoch(prevEpochId) {
				prevEpochHandler := getEpochHandlerForTracker(prevEpochId)
				nextEpochHandler := getEpochHandlerForTracker(prevEpochId + 1)

				if prevEpochHandler != nil && nextEpochHandler != nil && iAmLastMileFinalizer(nextEpochHandler) {
					tmpConns, tmpWaiter, tmpGuards := openTemporaryQuorumConnections(prevEpochHandler)

					epochRotationProof := tryCollectAggregatedEpochRotationProofWithConns(
						prevEpochId, prevEpochId+1,
						prevEpochHandler, tmpConns, tmpWaiter,
					)

					closeTemporaryQuorumConnections(tmpConns, tmpGuards)

					if epochRotationProof != nil {
						if !anchorConnectionsSent {
							openAnchorConnectionsForLastMile()
							anchorConnectionsSent = true
						}

						ackProof := deliverAggregatedEpochRotationProofToAnchors(epochRotationProof)
						websocket_pack.SendAggregatedEpochRotationProofToPoD(*epochRotationProof)
						storeAggregatedEpochRotationProof(epochRotationProof)

						if ackProof != nil && utils.VerifyAggregatedAnchorEpochAckProof(ackProof) {
							utils.StoreAggregatedAnchorEpochAckProof(ackProof)
							websocket_pack.SendAggregatedAnchorEpochAckProofToPoD(*ackProof)

							deliverAggregatedAnchorEpochAckProofToNewQuorum(ackProof, nextEpochHandler)

							utils.LogWithTime(
								fmt.Sprintf("Aggregated anchor epoch ack proof collected and delivered for epoch %d->%d", prevEpochId, prevEpochId+1),
								utils.DEEP_GREEN_COLOR,
							)
						}

						lastRotationEpoch = prevEpochId
						utils.LogWithTime(
							fmt.Sprintf("Aggregated epoch rotation proof sent for epoch %d->%d", prevEpochId, prevEpochId+1),
							utils.DEEP_GREEN_COLOR,
						)
					}
				}
			}
		}

		if tracker.EpochId < epochSnapshot.Id {
			if syncedTracker, synced := syncLastMileTrackerToCurrentEpochStart(tracker, &epochSnapshot); synced {
				tracker = syncedTracker
				rotateFinalizerEpochIfNeeded(tracker.EpochId)
				continue
			}
		}

		// --- Block sequencing (ALL nodes, mirrors block_execution.go from main) ---

		epochHandler := getEpochHandlerForTracker(tracker.EpochId)

		if epochHandler == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Keep FINALIZER_THREAD_METADATA.EpochDataHandler in sync with the tracker.
		// Sequence/anchor threads rely on this for canonical sequencing of the current epoch.
		rotateFinalizerEpochIfNeeded(tracker.EpochId)

		if tracker.LeaderIndex >= len(epochHandler.LeadersSequence) {
			completedBoundary := buildCompletedEpochBoundaryFromTracker(tracker, tracker.EpochId)
			if completedBoundary == nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, tracker, completedBoundary); err != nil {
				utils.LogWithTime(
					fmt.Sprintf("Last mile sequencer: failed to persist completed epoch boundary for epoch %d: %v", tracker.EpochId, err),
					utils.RED_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}

			nextEpochHandler := getEpochHandlerForTracker(tracker.EpochId + 1)
			if nextEpochHandler != nil {
				nextTracker := *tracker
				nextTracker.AdvanceToNextEpoch()
				if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, nil); err != nil {
					utils.LogWithTime(
						fmt.Sprintf("Last mile sequencer: failed to persist tracker advance to epoch %d: %v", nextTracker.EpochId, err),
						utils.RED_COLOR,
					)
					time.Sleep(200 * time.Millisecond)
					continue
				}
				tracker = &nextTracker
				continue
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}

		leader := epochHandler.LeadersSequence[tracker.LeaderIndex]
		blockId := fmt.Sprintf("%d:%s:%d", tracker.EpochId, leader, tracker.BlockIndex)
		lastBlocksByLeaders := snapshotLastBlocksByLeaders()
		lastBlock, known := lastBlocksByLeaders[leader]

		// If alignment already proved that this leader ended earlier, we may have
		// locally produced/fetched extra blocks for the same leader that are not
		// part of the canonical sequence. Skip them and move to the next leader.
		if known && tracker.BlockIndex > lastBlock.Index {
			utils.LogWithTime(
				fmt.Sprintf(
					"Last mile sequencer: skipping non-canonical block position %s because aligned last index for leader is %d",
					blockId,
					lastBlock.Index,
				),
				utils.YELLOW_COLOR,
			)
			nextTracker := *tracker
			nextTracker.LeaderIndex++
			nextTracker.BlockIndex = 0
			if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, nil); err != nil {
				utils.LogWithTime(
					fmt.Sprintf("Last mile sequencer: failed to persist non-canonical skip for %s: %v", blockId, err),
					utils.RED_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			tracker = &nextTracker
			continue
		}

		blockHash := getBlockHashByBlockId(blockId, epochHandler)

		if blockHash == "" {
			nextEpochVisible := getEpochHandlerForTracker(tracker.EpochId+1) != nil

			if known && lastBlock.Index < 0 {
				nextTracker := *tracker
				nextTracker.LeaderIndex++
				nextTracker.BlockIndex = 0
				if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, nil); err != nil {
					utils.LogWithTime(
						fmt.Sprintf("Last mile sequencer: failed to persist empty-leader skip for epoch %d leader %s: %v", tracker.EpochId, leader, err),
						utils.RED_COLOR,
					)
					time.Sleep(200 * time.Millisecond)
					continue
				}
				tracker = &nextTracker
				continue
			}

			alignmentIndex := -999
			alignmentHash := ""
			if known {
				alignmentIndex = lastBlock.Index
				alignmentHash = lastBlock.Hash
			}

			utils.LogWithTimeThrottled(
				fmt.Sprintf("last_mile:missing_block:%d:%s:%d", tracker.EpochId, leader, tracker.BlockIndex),
				2*time.Second,
				fmt.Sprintf(
					"Last mile sequencer waiting for block body: trackerEpoch=%d currentEpoch=%d nextHeight=%d heightInEpoch=%d leaderIndex=%d/%d leader=%s blockIndex=%d blockId=%s alignmentKnown=%t alignmentIndex=%d alignmentHash=%s nextEpochVisible=%t",
					tracker.EpochId,
					epochSnapshot.Id,
					tracker.NextHeight,
					tracker.HeightInEpoch,
					tracker.LeaderIndex,
					len(epochHandler.LeadersSequence)-1,
					leader,
					tracker.BlockIndex,
					blockId,
					known,
					alignmentIndex,
					utils.ShortHash(alignmentHash),
					nextEpochVisible,
				),
				utils.YELLOW_COLOR,
			)

			time.Sleep(200 * time.Millisecond)
			continue
		}

		isLastBlock := false
		confirmed := false

		lastBlocksByLeaders = snapshotLastBlocksByLeaders()
		lastBlock, known = lastBlocksByLeaders[leader]

		if known && lastBlock.Index == tracker.BlockIndex && lastBlock.Hash == blockHash {
			confirmed = true
			isLastBlock = tracker.LeaderIndex < epochHandler.CurrentLeaderIndex
		}

		if !confirmed {
			nextBlockId := fmt.Sprintf("%d:%s:%d", tracker.EpochId, leader, tracker.BlockIndex+1)
			if utils.HasLocalVerifiedAfp(nextBlockId, epochHandler) {
				confirmed = true
			} else if last, ok := afpBackfillAttempts[nextBlockId]; !ok || time.Since(last) >= lastMileAfpBackfillInterval {
				// Self-heal AFP propagation gaps. A non-last block is confirmed by
				// the verified AFP of the NEXT block, which normally lands locally
				// when this node receives that next block during live consensus.
				// Occasionally (especially at leader boundaries) that AFP never
				// reaches a node's local DB even though the quorum produced it, and
				// the finalizer would then stall here forever — freezing AHP
				// collection and execution network-wide. Backfill the missing AFP
				// from PoD (the authoritative store) so the gap self-heals.
				afpBackfillAttempts[nextBlockId] = time.Now()
				if backfillNextBlockAfpFromPoD(blockId, nextBlockId, epochHandler) {
					confirmed = true
				}
				if len(afpBackfillAttempts) > 4096 {
					afpBackfillAttempts = map[string]time.Time{nextBlockId: afpBackfillAttempts[nextBlockId]}
				}
			}
		}

		if !confirmed {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		currentHeight := tracker.NextHeight
		currentHeightInEpoch := tracker.HeightInEpoch

		// Advance local sequencing independently from AHP collection. The AHP
		// collector follows this mapping with its own cursor.
		nextTracker := *tracker
		if isLastBlock {
			nextTracker.LeaderIndex++
			nextTracker.BlockIndex = 0
		} else {
			nextTracker.BlockIndex++
		}
		nextTracker.NextHeight = currentHeight + 1
		nextTracker.HeightInEpoch = currentHeightInEpoch + 1

		var completedBoundary *structures.LastMileEpochBoundary
		if nextTracker.LeaderIndex >= len(epochHandler.LeadersSequence) {
			completedBoundary = newLastMileEpochBoundary(epochHandler.Id, currentHeight, blockId, blockHash)
		}

		if err := utils.PersistLastMileMappingsAndStateTransition(
			constants.DBKeyLastMileFinalizerTracker,
			currentHeight,
			blockId,
			currentHeightInEpoch,
			&nextTracker,
			completedBoundary,
		); err != nil {
			utils.LogWithTime(
				fmt.Sprintf("Last mile sequencer: failed to persist local sequencing progress for %s at height %d: %v", blockId, currentHeight, err),
				utils.RED_COLOR,
			)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		tracker = &nextTracker
	}
}

func LastMileAHPCollectorThread() {
	lastProcessedEpoch := -1
	lastFirstBlockEpochId := -1

	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileAHPCollectorTracker)
	if getFirstBlockDataFromDB(tracker.EpochId) != nil {
		lastFirstBlockEpochId = tracker.EpochId
	}

	runtime := newLastMileAHPRuntime()
	defer runtime.Close()

	for {
		sequencerTracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)
		if sequencerTracker.NextHeight <= tracker.NextHeight {
			if syncedTracker, synced := catchUpLastMileWithinEpoch(tracker, sequencerTracker); synced {
				tracker = syncedTracker
				runtime.ResetBackoff()
				continue
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if wait := runtime.BackoffRemaining(tracker.NextHeight); wait > 0 {
			time.Sleep(wait)
			continue
		}

		nextTracker, progressed := collectAHPWindow(tracker, sequencerTracker, &lastProcessedEpoch, &lastFirstBlockEpochId, runtime)
		if !progressed {
			if syncedTracker, synced := syncAHPCollectorToSequencerBoundary(tracker, sequencerTracker); synced {
				tracker = syncedTracker
				runtime.ResetBackoff()
				continue
			}
			runtime.RecordBlockedHeight(tracker.NextHeight)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		tracker = nextTracker
		runtime.ResetBackoff()
	}
}

func collectAHPWindow(
	tracker *utils.LastMileSequenceState,
	sequencerTracker *utils.LastMileSequenceState,
	lastProcessedEpoch *int,
	lastFirstBlockEpochId *int,
	runtime *lastMileAHPRuntime,
) (*utils.LastMileSequenceState, bool) {
	jobs := buildAHPCollectionJobs(tracker, sequencerTracker)
	if len(jobs) == 0 {
		return tracker, false
	}

	if jobs[0].BlockParts.EpochId != *lastProcessedEpoch {
		*lastProcessedEpoch = jobs[0].BlockParts.EpochId
		utils.LogWithTime(
			fmt.Sprintf("Last mile AHP collector: processing epoch %d from height %d", jobs[0].BlockParts.EpochId, tracker.NextHeight),
			utils.CYAN_COLOR,
		)
	}

	workers := LAST_MILE_AHP_COLLECTION_WORKERS
	if workers > len(jobs) {
		workers = len(jobs)
	}
	if workers < 1 {
		workers = 1
	}

	jobCh := make(chan lastMileAHPCollectionJob, len(jobs))
	resultCh := make(chan lastMileAHPCollectionResult, len(jobs))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerIndex int) {
			defer wg.Done()

			for job := range jobCh {
				resultCh <- collectAHPForJob(job, runtime.WorkerClientRef(workerIndex))
			}
		}(i)
	}

	for _, job := range jobs {
		jobCh <- job
	}
	close(jobCh)

	wg.Wait()
	close(resultCh)

	results := make(map[int64]lastMileAHPCollectionResult, len(jobs))
	for result := range resultCh {
		if result.Proof != nil {
			results[result.Job.Height] = result
		}
	}

	nextTracker := *tracker
	committed := false

	for {
		result, ok := results[nextTracker.NextHeight]
		if !ok {
			break
		}
		storeAggregatedHeightProof(result.Proof)
		commitFirstBlockAHPIfNeeded(result.Proof, result.Job.BlockParts, result.BlockHash, lastFirstBlockEpochId)
		websocket_pack.SendAggregatedHeightProofToPoD(*result.Proof)

		advanced := advanceAHPCollectorTracker(&nextTracker, result.Job.BlockId, result.Job.EpochHandler)
		if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileAHPCollectorTracker, advanced, nil); err != nil {
			utils.LogWithTime(
				fmt.Sprintf("Last mile AHP collector: failed to persist tracker advance after height %d: %v", nextTracker.NextHeight, err),
				utils.RED_COLOR,
			)
			break
		}

		utils.LogWithTime(
			fmt.Sprintf("Aggregated height proof committed for height %d => %s (hash: %s...)", result.Proof.AbsoluteHeight, result.Job.BlockId, utils.ShortHash(result.BlockHash)),
			utils.DEEP_GREEN_COLOR,
		)

		nextTracker = *advanced
		committed = true
	}

	return &nextTracker, committed
}

func buildAHPCollectionJobs(tracker *utils.LastMileSequenceState, sequencerTracker *utils.LastMileSequenceState) []lastMileAHPCollectionJob {
	if tracker == nil || sequencerTracker == nil || sequencerTracker.NextHeight <= tracker.NextHeight {
		return nil
	}

	limit := tracker.NextHeight + int64(LAST_MILE_AHP_COLLECTION_WINDOW)
	if limit > sequencerTracker.NextHeight {
		limit = sequencerTracker.NextHeight
	}

	jobs := make([]lastMileAHPCollectionJob, 0, int(limit-tracker.NextHeight))
	for height := tracker.NextHeight; height < limit; height++ {
		blockId := utils.LoadHeightBlockIdMapping(height)
		heightInEpoch, ok := utils.LoadHeightInEpochMapping(height)
		if blockId == "" || !ok {
			break
		}

		blockParts, ok := parseLastMileBlockId(blockId)
		if !ok {
			break
		}

		epochHandler := getEpochHandlerForTracker(blockParts.EpochId)
		if epochHandler == nil {
			break
		}

		jobs = append(jobs, lastMileAHPCollectionJob{
			Height:        height,
			BlockId:       blockId,
			HeightInEpoch: heightInEpoch,
			BlockParts:    blockParts,
			EpochHandler:  epochHandler,
		})
	}

	return jobs
}

func collectAHPForJob(job lastMileAHPCollectionJob, quorumClient **lastMileAHPQuorumClient) lastMileAHPCollectionResult {
	blockHash := getVerifiedBlockHashForAHP(job.BlockId)
	if blockHash == "" {
		return lastMileAHPCollectionResult{Job: job}
	}

	proof := fetchVerifiedAggregatedHeightProofForEpoch(int(job.Height), job.EpochHandler)
	if proof != nil {
		if proof.BlockId != job.BlockId || proof.BlockHash != blockHash || proof.HeightInEpoch != job.HeightInEpoch {
			return lastMileAHPCollectionResult{Job: job}
		}
		return lastMileAHPCollectionResult{Job: job, Proof: proof, BlockHash: blockHash}
	}

	if !iAmLastMileFinalizer(job.EpochHandler) {
		return lastMileAHPCollectionResult{Job: job}
	}

	client := getLastMileAHPQuorumClient(quorumClient, job.EpochHandler)
	if client == nil {
		return lastMileAHPCollectionResult{Job: job}
	}

	proof = tryCollectAggregatedHeightProofWithConns(
		int(job.Height),
		job.BlockId,
		blockHash,
		job.BlockParts.EpochId,
		job.HeightInEpoch,
		job.EpochHandler,
		client.Conns,
		client.Waiter,
	)
	if proof == nil {
		return lastMileAHPCollectionResult{Job: job}
	}

	return lastMileAHPCollectionResult{Job: job, Proof: proof, BlockHash: blockHash}
}

func getLastMileAHPQuorumClient(clientRef **lastMileAHPQuorumClient, epochHandler *structures.EpochDataHandler) *lastMileAHPQuorumClient {
	if clientRef == nil || epochHandler == nil {
		return nil
	}
	if *clientRef != nil && (*clientRef).EpochId == epochHandler.Id {
		return *clientRef
	}

	closeLastMileAHPQuorumClient(*clientRef)

	conns, waiter, guards := openTemporaryQuorumConnections(epochHandler)
	if waiter == nil || guards == nil {
		*clientRef = nil
		return nil
	}

	*clientRef = &lastMileAHPQuorumClient{
		EpochId: epochHandler.Id,
		Conns:   conns,
		Waiter:  waiter,
		Guards:  guards,
	}
	return *clientRef
}

func closeLastMileAHPQuorumClient(client *lastMileAHPQuorumClient) {
	if client == nil {
		return
	}
	closeTemporaryQuorumConnections(client.Conns, client.Guards)
}

func newLastMileAHPRuntime() *lastMileAHPRuntime {
	workerCount := LAST_MILE_AHP_COLLECTION_WORKERS
	if workerCount < 1 {
		workerCount = 1
	}
	return &lastMileAHPRuntime{
		WorkerClients: make([]*lastMileAHPQuorumClient, workerCount),
		BlockedHeight: -1,
	}
}

func (runtime *lastMileAHPRuntime) WorkerClientRef(workerIndex int) **lastMileAHPQuorumClient {
	if runtime == nil || workerIndex < 0 || workerIndex >= len(runtime.WorkerClients) {
		return nil
	}
	return &runtime.WorkerClients[workerIndex]
}

func (runtime *lastMileAHPRuntime) RecordBlockedHeight(height int64) {
	if runtime == nil {
		return
	}
	if runtime.BlockedHeight != height {
		runtime.BlockedHeight = height
		runtime.Failures = 0
	}
	runtime.Failures++
	delay := LAST_MILE_AHP_RETRY_BACKOFF_MIN
	for i := 1; i < runtime.Failures && delay < LAST_MILE_AHP_RETRY_BACKOFF_MAX; i++ {
		delay *= 2
	}
	if delay > LAST_MILE_AHP_RETRY_BACKOFF_MAX {
		delay = LAST_MILE_AHP_RETRY_BACKOFF_MAX
	}
	runtime.RetryAfter = time.Now().Add(delay)
}

func (runtime *lastMileAHPRuntime) BackoffRemaining(height int64) time.Duration {
	if runtime == nil || runtime.BlockedHeight != height || runtime.RetryAfter.IsZero() {
		return 0
	}
	return time.Until(runtime.RetryAfter)
}

func (runtime *lastMileAHPRuntime) ResetBackoff() {
	if runtime == nil {
		return
	}
	runtime.BlockedHeight = -1
	runtime.Failures = 0
	runtime.RetryAfter = time.Time{}
}

func (runtime *lastMileAHPRuntime) Close() {
	if runtime == nil {
		return
	}
	for idx, client := range runtime.WorkerClients {
		closeLastMileAHPQuorumClient(client)
		runtime.WorkerClients[idx] = nil
	}
}

func fetchVerifiedAggregatedHeightProofForEpoch(absoluteHeight int, epochHandler *structures.EpochDataHandler) *structures.AggregatedHeightProof {
	if epochHandler == nil {
		return nil
	}

	if proof := LoadAggregatedHeightProof(absoluteHeight); proof != nil &&
		proof.EpochId == epochHandler.Id &&
		utils.VerifyAggregatedHeightProof(proof, epochHandler) {
		return proof
	}

	if proof := websocket_pack.GetAggregatedHeightProofFromPoD(absoluteHeight); proof != nil &&
		proof.EpochId == epochHandler.Id &&
		utils.VerifyAggregatedHeightProof(proof, epochHandler) {
		return proof
	}

	return utils.GetAggregatedHeightProofFromQuorumByHeight(absoluteHeight, epochHandler)
}

func catchUpLastMileWithinEpoch(
	tracker *utils.LastMileSequenceState,
	sequencerTracker *utils.LastMileSequenceState,
) (*utils.LastMileSequenceState, bool) {
	if tracker == nil || sequencerTracker == nil {
		return nil, false
	}

	epochHandler := getEpochHandlerForTracker(tracker.EpochId)
	if epochHandler == nil {
		return nil, false
	}

	proof := fetchVerifiedAggregatedHeightProofForEpoch(int(tracker.NextHeight), epochHandler)
	if proof == nil || proof.EpochId != tracker.EpochId || proof.HeightInEpoch != tracker.HeightInEpoch {
		return nil, false
	}

	blockParts, ok := parseLastMileBlockId(proof.BlockId)
	if !ok || blockParts.EpochId != proof.EpochId {
		return nil, false
	}

	blockHash := getVerifiedBlockHashForAHP(proof.BlockId)
	if blockHash == "" || blockHash != proof.BlockHash {
		return nil, false
	}

	storeAggregatedHeightProof(proof)
	commitFirstBlockAHPIfNeeded(proof, blockParts, blockHash, nil)
	websocket_pack.SendAggregatedHeightProofToPoD(*proof)

	if sequencerTracker.NextHeight <= int64(proof.AbsoluteHeight) {
		sequencerNext := advanceAHPCollectorTracker(sequencerTracker, proof.BlockId, epochHandler)
		if err := utils.PersistLastMileMappingsAndStateTransition(
			constants.DBKeyLastMileFinalizerTracker,
			int64(proof.AbsoluteHeight),
			proof.BlockId,
			proof.HeightInEpoch,
			sequencerNext,
			nil,
		); err != nil {
			utils.LogWithTime(
				fmt.Sprintf("Last mile sequencer: failed to persist intra-epoch catch-up at height %d: %v", proof.AbsoluteHeight, err),
				utils.RED_COLOR,
			)
			return nil, false
		}
	}

	nextTracker := advanceAHPCollectorTracker(tracker, proof.BlockId, epochHandler)
	if err := utils.PersistLastMileMappingsAndStateTransition(
		constants.DBKeyLastMileAHPCollectorTracker,
		int64(proof.AbsoluteHeight),
		proof.BlockId,
		proof.HeightInEpoch,
		nextTracker,
		nil,
	); err != nil {
		utils.LogWithTime(
			fmt.Sprintf("Last mile AHP collector: failed to persist intra-epoch catch-up at height %d: %v", proof.AbsoluteHeight, err),
			utils.RED_COLOR,
		)
		return nil, false
	}

	utils.LogWithTime(
		fmt.Sprintf("Last mile intra-epoch catch-up: accepted verified AHP height %d => %s", proof.AbsoluteHeight, proof.BlockId),
		utils.CYAN_COLOR,
	)

	return nextTracker, true
}

func getVerifiedBlockHashForAHP(blockId string) string {
	// Resolve the block strictly within the consensus (genesis) network. During a
	// pending recovery the execution-scoped fetcher (fetchBlockForExecution) reads
	// the previous network, whose epoch-0 blockIds collide with the recovered
	// chain, so it would return stale blocks for new-network height proofs.
	parts := strings.Split(blockId, ":")
	if len(parts) != 3 {
		return ""
	}
	epochIndex, epochErr := strconv.Atoi(parts[0])
	blockIndex, indexErr := strconv.Atoi(parts[2])
	if epochErr != nil || indexErr != nil || blockIndex < 0 {
		return ""
	}

	epochHandler := getEpochHandlerForTracker(epochIndex)
	if epochHandler == nil {
		return ""
	}

	block := block_pack.GetBlockForConsensus(epochIndex, parts[1], uint(blockIndex), epochHandler)
	if block == nil {
		return ""
	}
	if block.Creator != parts[1] || block.Index != blockIndex || !block.VerifySignatureForNetwork(globals.GENESIS.NetworkId) {
		return ""
	}

	return block.GetHash()
}

func commitFirstBlockAHPIfNeeded(
	proof *structures.AggregatedHeightProof,
	blockParts lastMileBlockIdParts,
	blockHash string,
	lastFirstBlockEpochId *int,
) {
	if proof == nil || proof.HeightInEpoch != 0 {
		return
	}
	if lastFirstBlockEpochId != nil && proof.EpochId == *lastFirstBlockEpochId {
		return
	}
	if lastFirstBlockEpochId == nil && getFirstBlockDataFromDB(proof.EpochId) != nil {
		return
	}

	storeFirstBlockAggregatedHeightProof(proof)
	if len(blockParts.Creator) > 0 {
		_ = storeDataAboutFirstBlockInEpoch(proof.EpochId, &FirstBlockData{
			FirstBlockCreator: blockParts.Creator,
			FirstBlockHash:    blockHash,
		})
	}
	if lastFirstBlockEpochId != nil {
		*lastFirstBlockEpochId = proof.EpochId
	}
	utils.LogWithTime(
		fmt.Sprintf("First core block in epoch %d detected (HeightInEpoch=0): creator=%s, hash=%s...", proof.EpochId, blockParts.Creator, utils.ShortHash(blockHash)),
		utils.GREEN_COLOR,
	)
}

func parseLastMileBlockId(blockId string) (lastMileBlockIdParts, bool) {
	parts := strings.Split(blockId, ":")
	if len(parts) != 3 {
		return lastMileBlockIdParts{}, false
	}
	epochId, err := strconv.Atoi(parts[0])
	if err != nil {
		return lastMileBlockIdParts{}, false
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil {
		return lastMileBlockIdParts{}, false
	}
	return lastMileBlockIdParts{
		EpochId: epochId,
		Creator: parts[1],
		Index:   index,
	}, true
}

func advanceAHPCollectorTracker(tracker *utils.LastMileSequenceState, blockId string, epochHandler *structures.EpochDataHandler) *utils.LastMileSequenceState {
	nextHeight := tracker.NextHeight + 1
	if mappedNextBlockId := utils.LoadHeightBlockIdMapping(nextHeight); mappedNextBlockId != "" {
		if mapped, ok := buildAHPCollectorTrackerForMappedHeight(nextHeight, mappedNextBlockId); ok {
			return mapped
		}
	}

	blockParts, ok := parseLastMileBlockId(blockId)
	if !ok {
		nextTracker := *tracker
		nextTracker.NextHeight = nextHeight
		nextTracker.HeightInEpoch++
		return &nextTracker
	}

	nextTracker := *tracker
	nextTracker.EpochId = blockParts.EpochId
	nextTracker.LeaderIndex = leaderIndexForPubkey(epochHandler, blockParts.Creator)
	nextTracker.BlockIndex = blockParts.Index + 1
	nextTracker.NextHeight = nextHeight
	nextTracker.HeightInEpoch++
	return &nextTracker
}

func buildAHPCollectorTrackerForMappedHeight(height int64, blockId string) (*utils.LastMileSequenceState, bool) {
	blockParts, ok := parseLastMileBlockId(blockId)
	if !ok {
		return nil, false
	}
	heightInEpoch, ok := utils.LoadHeightInEpochMapping(height)
	if !ok {
		return nil, false
	}
	epochHandler := getEpochHandlerForTracker(blockParts.EpochId)
	if epochHandler == nil {
		return nil, false
	}
	return &utils.LastMileSequenceState{
		EpochId:       blockParts.EpochId,
		LeaderIndex:   leaderIndexForPubkey(epochHandler, blockParts.Creator),
		BlockIndex:    blockParts.Index,
		NextHeight:    height,
		HeightInEpoch: heightInEpoch,
	}, true
}

func leaderIndexForPubkey(epochHandler *structures.EpochDataHandler, leader string) int {
	if epochHandler == nil {
		return 0
	}
	for idx, candidate := range epochHandler.LeadersSequence {
		if candidate == leader {
			return idx
		}
	}
	return 0
}

func syncAHPCollectorToSequencerBoundary(
	tracker *utils.LastMileSequenceState,
	sequencerTracker *utils.LastMileSequenceState,
) (*utils.LastMileSequenceState, bool) {
	if tracker == nil || sequencerTracker == nil || tracker.EpochId >= sequencerTracker.EpochId {
		return nil, false
	}
	previousBoundary := utils.LoadLastMileEpochBoundary(sequencerTracker.EpochId - 1)
	if previousBoundary == nil {
		return nil, false
	}
	nextTracker := &utils.LastMileSequenceState{
		EpochId:       sequencerTracker.EpochId,
		LeaderIndex:   0,
		BlockIndex:    0,
		NextHeight:    previousBoundary.FinishedOnHeight + 1,
		HeightInEpoch: 0,
	}
	if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileAHPCollectorTracker, nextTracker, nil); err != nil {
		utils.LogWithTime(
			fmt.Sprintf("Last mile AHP collector: failed to fast-forward tracker to epoch %d: %v", sequencerTracker.EpochId, err),
			utils.RED_COLOR,
		)
		return nil, false
	}
	utils.LogWithTime(
		fmt.Sprintf("Last mile AHP collector: fast-forwarded tracker to epoch %d height=%d using sequencer boundary",
			nextTracker.EpochId,
			nextTracker.NextHeight,
		),
		utils.CYAN_COLOR,
	)
	return nextTracker, true
}

func selectLastMileFinalizersForEpoch(epochHandler *structures.EpochDataHandler) []string {
	quorum := epochHandler.Quorum

	if len(quorum) == 0 {
		return nil
	}

	count := LAST_MILE_FINALIZERS_COUNT
	if count > len(quorum) {
		count = len(quorum)
	}

	seed := utils.Blake3(fmt.Sprintf("LAST_MILE_FINALIZERS_SELECTION:%d:%s", epochHandler.Id, epochHandler.Hash))

	indices := make([]int, len(quorum))
	for i := range indices {
		indices[i] = i
	}

	for i := 0; i < count; i++ {
		hashHex := utils.Blake3(seed + "_" + strconv.Itoa(i))
		r := utils.HashHexToUint64(hashHex) % uint64(len(quorum)-i)
		j := i + int(r)
		indices[i], indices[j] = indices[j], indices[i]
	}

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = quorum[indices[i]]
	}

	return result
}

func iAmLastMileFinalizer(epochHandler *structures.EpochDataHandler) bool {

	selected := selectLastMileFinalizersForEpoch(epochHandler)

	return slices.Contains(selected, globals.CONFIGURATION.PublicKey)
}

func rememberLastMileEpochHandler(epochHandler *structures.EpochDataHandler) {
	if epochHandler == nil {
		return
	}
	LAST_MILE_EPOCH_HANDLERS_MUTEX.Lock()
	LAST_MILE_EPOCH_HANDLERS[epochHandler.Id] = *epochHandler
	LAST_MILE_EPOCH_HANDLERS_MUTEX.Unlock()
}

func getRememberedLastMileEpochHandler(epochId int) *structures.EpochDataHandler {
	LAST_MILE_EPOCH_HANDLERS_MUTEX.RLock()
	handler, ok := LAST_MILE_EPOCH_HANDLERS[epochId]
	LAST_MILE_EPOCH_HANDLERS_MUTEX.RUnlock()
	if !ok {
		return nil
	}
	return &handler
}

func openTemporaryQuorumConnections(epochHandler *structures.EpochDataHandler) (map[string]*websocket.Conn, *utils.QuorumWaiter, *utils.WebsocketGuards) {
	conns := make(map[string]*websocket.Conn)
	guards := utils.NewWebsocketGuards()
	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, conns, guards)
	waiter := utils.NewQuorumWaiter(len(epochHandler.Quorum), guards)

	return conns, waiter, guards
}

func closeTemporaryQuorumConnections(conns map[string]*websocket.Conn, guards *utils.WebsocketGuards) {
	if guards == nil {
		return
	}
	guards.ConnMu.Lock()
	defer guards.ConnMu.Unlock()

	for id, conn := range conns {
		if conn != nil {
			_ = conn.Close()
		}
		delete(conns, id)
	}
}

func openAnchorConnectionsForLastMile() {
	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()

	for _, conn := range LAST_MILE_ANCHOR_WS_CONNS {
		if conn != nil {
			_ = conn.Close()
		}
	}

	LAST_MILE_ANCHOR_WS_CONNS = make(map[string]*websocket.Conn)

	for _, anchor := range globals.ANCHORS {
		if anchor.WssAnchorUrl == "" {
			continue
		}

		conn, _, err := websocket.DefaultDialer.Dial(anchor.WssAnchorUrl, nil)
		if err != nil {
			continue
		}

		LAST_MILE_ANCHOR_WS_CONNS[anchor.Pubkey] = conn
	}
}

// getOrRedialAnchorConn returns the live websocket to the given anchor, redialing
// lazily if the cached connection is missing. The redial is bounded to a few
// attempts with a small backoff so that a transient anchor hiccup doesn't force
// us to wait for the next epoch rotation to refresh LAST_MILE_ANCHOR_WS_CONNS.
//
// Returns nil if the anchor URL is empty or all redial attempts failed.
func getOrRedialAnchorConn(anchorPubkey, anchorUrl string) *websocket.Conn {
	if anchorUrl == "" {
		return nil
	}

	LAST_MILE_MUTEX.Lock()
	conn := LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey]
	LAST_MILE_MUTEX.Unlock()
	if conn != nil {
		return conn
	}

	const (
		anchorRedialAttempts = 3
		anchorRedialBackoff  = 200 * time.Millisecond
	)

	for attempt := 1; attempt <= anchorRedialAttempts; attempt++ {
		fresh, _, err := websocket.DefaultDialer.Dial(anchorUrl, nil)
		if err == nil {
			LAST_MILE_MUTEX.Lock()
			if existing := LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey]; existing != nil {
				_ = fresh.Close()
				conn = existing
			} else {
				LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey] = fresh
				conn = fresh
			}
			LAST_MILE_MUTEX.Unlock()
			return conn
		}
		utils.LogWithTimeThrottled(
			"last_mile:anchor:redial:"+anchorPubkey,
			2*time.Second,
			fmt.Sprintf("Last mile: anchor %s redial failed (attempt %d/%d): %v", anchorPubkey, attempt, anchorRedialAttempts, err),
			utils.YELLOW_COLOR,
		)
		if attempt < anchorRedialAttempts {
			time.Sleep(anchorRedialBackoff)
		}
	}
	return nil
}

// dropAnchorConn closes and removes the cached connection for the anchor so the
// next getOrRedialAnchorConn call dials fresh. Safe under LAST_MILE_MUTEX.
func dropAnchorConn(anchorPubkey string, expected *websocket.Conn) {
	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()
	if cur, ok := LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey]; ok && (expected == nil || cur == expected) {
		if cur != nil {
			_ = cur.Close()
		}
		delete(LAST_MILE_ANCHOR_WS_CONNS, anchorPubkey)
	}
}

func storeAggregatedHeightProof(proof *structures.AggregatedHeightProof) {
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixAggregatedHeightProof+"%d", proof.AbsoluteHeight))

	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_THREAD_METADATA.Put(key, value, nil)
	}
}

func LoadAggregatedHeightProof(absoluteHeight int) *structures.AggregatedHeightProof {
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixAggregatedHeightProof+"%d", absoluteHeight))

	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)

	if err != nil {
		return nil
	}

	var proof structures.AggregatedHeightProof

	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}

	return &proof
}

func getEpochHandlerForTracker(epochId int) *structures.EpochDataHandler {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	if handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.Id == epochId {
		copy := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return &copy
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	if handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler.Id == epochId {
		copy := handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler
		handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()
		return &copy
	}
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	if remembered := getRememberedLastMileEpochHandler(epochId); remembered != nil {
		return remembered
	}

	if snapshot := utils.GetEpochSnapshot(toAbsoluteEpochId(epochId)); snapshot != nil {
		return &snapshot.EpochDataHandler
	}
	if toAbsoluteEpochId(epochId) != epochId {
		if snapshot := getEpochSnapshotFromApprovementDB(epochId); snapshot != nil {
			return &snapshot.EpochDataHandler
		}
	}

	if derived := deriveEpochHandlerFromNextEpochData(epochId); derived != nil {
		return derived
	}

	return nil
}

func deriveEpochHandlerFromNextEpochData(epochId int) *structures.EpochDataHandler {
	if epochId <= 0 {
		return nil
	}

	prevEpochHandler := getEpochHandlerForTracker(epochId - 1)
	if prevEpochHandler == nil {
		return nil
	}

	nextEpochData := utils.LoadNextEpochData(epochId)
	if nextEpochData == nil {
		proof := LoadAggregatedEpochRotationProof(epochId - 1)
		if proof == nil || proof.NextEpochId != epochId || !utils.VerifyAggregatedEpochRotationProof(proof, prevEpochHandler) {
			return nil
		}
		nextEpochData = &proof.EpochData
	}

	startTimestamp := nextEpochData.NextEpochStartTimestamp
	if startTimestamp == 0 {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		epochDuration := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.EpochDuration
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		startTimestamp = prevEpochHandler.StartTimestamp + uint64(epochDuration)
	}

	currentLeaderIndex := 0
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	leadershipDuration := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.LeadershipDuration
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
	if leadershipDuration > 0 && len(nextEpochData.NextEpochLeadersSequence) > 0 {
		now := utils.GetUTCTimestampInMilliSeconds()
		if now >= int64(startTimestamp) {
			currentLeaderIndex = int((now - int64(startTimestamp)) / leadershipDuration)
			if currentLeaderIndex > len(nextEpochData.NextEpochLeadersSequence) {
				currentLeaderIndex = len(nextEpochData.NextEpochLeadersSequence)
			}
		}
	}

	return &structures.EpochDataHandler{
		Id:                 epochId,
		Hash:               nextEpochData.NextEpochHash,
		ValidatorsRegistry: nextEpochData.NextEpochValidatorsRegistry,
		Quorum:             nextEpochData.NextEpochQuorum,
		LeadersSequence:    nextEpochData.NextEpochLeadersSequence,
		StartTimestamp:     startTimestamp,
		CurrentLeaderIndex: currentLeaderIndex,
	}
}

func getEpochSnapshotFromApprovementDB(epochId int) *structures.EpochDataSnapshot {
	key := []byte(constants.DBKeyPrefixEpochHandler + strconv.Itoa(epochId))
	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}

	var snapshot structures.EpochDataSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil
	}
	return &snapshot
}

func snapshotLastBlocksByLeaders() map[string]structures.ExecutionStats {
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	defer handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	data := handlers.FINALIZER_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders
	if data == nil {
		return make(map[string]structures.ExecutionStats)
	}

	cp := make(map[string]structures.ExecutionStats, len(data))
	for k, v := range data {
		cp[k] = v
	}
	return cp
}

// rotateFinalizerEpochIfNeeded synchronizes FINALIZER_THREAD_METADATA.Handler.EpochDataHandler
// with tracker.EpochId. Whenever LMF advances to a new epoch (either via natural
// LeadersSequence completion or via fast-forward sync), this function loads the
// next epoch's handler from APPROVEMENT_THREAD_METADATA DB and resets SequenceAlignmentData.
//
// Returns true if rotation actually happened. Returns false if no rotation was needed,
// or if the next epoch handler is not yet available locally.
func rotateFinalizerEpochIfNeeded(targetEpochId int) bool {
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	currentEpochId := handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler.Id
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	if currentEpochId == targetEpochId {
		return false
	}

	nextEpochHandler := getEpochHandlerForTracker(targetEpochId)
	if nextEpochHandler == nil {
		return false
	}

	handlers.FINALIZER_THREAD_METADATA.RWMutex.Lock()
	defer handlers.FINALIZER_THREAD_METADATA.RWMutex.Unlock()

	if handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler.Id == targetEpochId {
		return false
	}

	handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler = *nextEpochHandler
	handlers.FINALIZER_THREAD_METADATA.Handler.SequenceAlignmentData = structures.AlignmentDataHandler{
		CurrentAnchorAssumption:         0,
		CurrentAnchorBlockIndexObserved: -1,
		CurrentLeaderToExecBlocksFrom:   0,
		LastBlocksByLeaders:             make(map[string]structures.ExecutionStats),
		LastBlocksByAnchors:             make(map[int]structures.ExecutionStats),
	}

	persistFinalizerThreadMetadataLocked()

	utils.LogWithTime(
		fmt.Sprintf("FINALIZER_THREAD_METADATA: rotated to epoch %d (from %d)", targetEpochId, currentEpochId),
		utils.CYAN_COLOR,
	)

	return true
}

func getBlockHashByBlockId(blockId string, epochHandler *structures.EpochDataHandler) string {
	raw, err := databases.BLOCKS.Get([]byte(blockId), nil)
	if err != nil {
		if epochHandler == nil {
			return ""
		}
		parts := strings.Split(blockId, ":")
		if len(parts) != 3 {
			return ""
		}
		epochIndex, epochErr := strconv.Atoi(parts[0])
		blockIndex, indexErr := strconv.Atoi(parts[2])
		if epochErr != nil || indexErr != nil || blockIndex < 0 {
			return ""
		}
		block := block_pack.GetBlockForConsensus(epochIndex, parts[1], uint(blockIndex), epochHandler)
		if block == nil {
			return ""
		}
		raw, marshalErr := json.Marshal(block)
		if marshalErr == nil {
			_ = databases.BLOCKS.Put([]byte(blockId), raw, nil)
		}
		return block.GetHash()
	}

	var block block_pack.Block
	if json.Unmarshal(raw, &block) == nil {
		return block.GetHash()
	}

	return ""
}

func newLastMileEpochBoundary(epochId int, finishedOnHeight int64, finishedOnBlockId, finishedOnHash string) *structures.LastMileEpochBoundary {
	if finishedOnHash == "" {
		finishedOnHash = constants.ZeroHash
	}

	return &structures.LastMileEpochBoundary{
		EpochId:           epochId,
		FinishedOnHeight:  finishedOnHeight,
		FinishedOnBlockId: finishedOnBlockId,
		FinishedOnHash:    finishedOnHash,
	}
}

func buildCompletedEpochBoundaryFromTracker(tracker *utils.LastMileSequenceState, epochId int) *structures.LastMileEpochBoundary {
	if tracker == nil {
		return nil
	}

	finishedOnHeight := tracker.NextHeight - 1
	if finishedOnHeight < 0 {
		return newLastMileEpochBoundary(epochId, -1, "", constants.ZeroHash)
	}

	finishedOnBlockId := utils.LoadHeightBlockIdMapping(finishedOnHeight)
	if finishedOnBlockId == "" {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:boundary_missing_block_id:%d", epochId),
			2*time.Second,
			fmt.Sprintf("Last mile sequencer: missing blockId mapping for completed epoch %d at height %d", epochId, finishedOnHeight),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	finishedOnHash := getBlockHashByBlockId(finishedOnBlockId, nil)
	if finishedOnHash == "" {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:boundary_missing_hash:%d:%d", epochId, finishedOnHeight),
			2*time.Second,
			fmt.Sprintf("Last mile sequencer: missing block hash for completed epoch %d at height %d blockId=%s", epochId, finishedOnHeight, finishedOnBlockId),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	return newLastMileEpochBoundary(epochId, finishedOnHeight, finishedOnBlockId, finishedOnHash)
}

func syncLastMileTrackerToCurrentEpochStart(
	tracker *utils.LastMileSequenceState,
	currentEpochHandler *structures.EpochDataHandler,
) (*utils.LastMileSequenceState, bool) {
	if tracker == nil || currentEpochHandler == nil || currentEpochHandler.Id <= 0 || tracker.EpochId >= currentEpochHandler.Id {
		return nil, false
	}

	nextTracker := *tracker

	for nextTracker.EpochId < currentEpochHandler.Id {
		proof := fetchVerifiedAggregatedEpochRotationProof(nextTracker.EpochId)
		if proof == nil || proof.NextEpochId != nextTracker.EpochId+1 {
			return nil, false
		}

		nextHeight := proof.FinishedOnHeight + 1
		if nextHeight < nextTracker.NextHeight {
			utils.LogWithTimeThrottled(
				fmt.Sprintf("last_mile:catchup_regression:%d:%d", nextTracker.EpochId, proof.NextEpochId),
				2*time.Second,
				fmt.Sprintf(
					"Last mile sequencer: refusing tracker fast-forward to epoch %d because proof boundary height %d would regress local nextHeight %d",
					proof.NextEpochId,
					proof.FinishedOnHeight,
					nextTracker.NextHeight,
				),
				utils.YELLOW_COLOR,
			)
			return nil, false
		}

		provenBoundary := newLastMileEpochBoundary(
			proof.EpochId,
			proof.FinishedOnHeight,
			proof.FinishedOnBlockId,
			proof.FinishedOnHash,
		)

		nextTracker = utils.LastMileSequenceState{
			EpochId:       proof.NextEpochId,
			LeaderIndex:   0,
			BlockIndex:    0,
			NextHeight:    nextHeight,
			HeightInEpoch: 0,
		}

		if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, provenBoundary); err != nil {
			utils.LogWithTime(
				fmt.Sprintf("Last mile sequencer: failed to persist catch-up tracker sync to epoch %d: %v", proof.NextEpochId, err),
				utils.RED_COLOR,
			)
			return nil, false
		}
	}

	utils.LogWithTime(
		fmt.Sprintf(
			"Last mile sequencer: fast-forwarded tracker from epoch %d to epoch %d using verified rotation proof chain (nextHeight=%d)",
			tracker.EpochId,
			nextTracker.EpochId,
			nextTracker.NextHeight,
		),
		utils.CYAN_COLOR,
	)

	return &nextTracker, true
}

func tryCollectAggregatedHeightProofWithConns(
	absoluteHeight int,
	blockId string,
	blockHash string,
	epochId int,
	heightInEpoch int,
	epochHandler *structures.EpochDataHandler,
	wsConns map[string]*websocket.Conn,
	waiter *utils.QuorumWaiter,
) *structures.AggregatedHeightProof {
	majority := utils.GetQuorumMajority(epochHandler)

	request := websocket_pack.WsHeightProofRequest{
		Route:          constants.WsRouteSignHeightProof,
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
		EpochId:        epochId,
		HeightInEpoch:  heightInEpoch,
	}

	message, err := json.Marshal(request)

	if err != nil {
		return nil
	}

	if waiter == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	validateProof := func(id string, raw []byte) bool {
		var response websocket_pack.WsHeightProofResponse

		if json.Unmarshal(raw, &response) != nil {
			return false
		}

		if !slices.Contains(epochHandler.Quorum, response.Voter) {
			return false
		}

		dataToVerify := strings.Join([]string{
			constants.SigningPrefixHeightProof,
			strconv.Itoa(absoluteHeight),
			blockId,
			blockHash,
			strconv.Itoa(epochId),
			strconv.Itoa(heightInEpoch),
		}, ":")

		return cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig)
	}

	responses, ok := waiter.SendAndWaitValidated(ctx, message, epochHandler.Quorum, wsConns, majority, validateProof)

	if !ok {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:ha_majority_failed:%d", absoluteHeight),
			5*time.Second,
			fmt.Sprintf("Last mile: failed to collect aggregated height proof majority for height %d (quorum=%d majority=%d)", absoluteHeight, len(epochHandler.Quorum), majority),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	proofs := make(map[string]string)

	for _, raw := range responses {
		var response websocket_pack.WsHeightProofResponse

		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.AggregatedHeightProof{
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
		EpochId:        epochId,
		HeightInEpoch:  heightInEpoch,
		Proofs:         proofs,
	}
}

func tryCollectAggregatedEpochRotationProofWithConns(
	epochId, nextEpochId int,
	prevEpochHandler *structures.EpochDataHandler,
	wsConns map[string]*websocket.Conn, waiter *utils.QuorumWaiter,
) *structures.AggregatedEpochRotationProof {
	if prevEpochHandler == nil || waiter == nil {
		return nil
	}

	localEpochData := utils.LoadNextEpochData(nextEpochId)
	if localEpochData == nil {
		return nil
	}

	epochDataHash := utils.ComputeEpochDataHash(localEpochData)
	if epochDataHash == "" {
		return nil
	}

	majority := utils.GetQuorumMajority(prevEpochHandler)
	boundary := utils.LoadLastMileEpochBoundary(epochId)
	if boundary == nil {
		return nil
	}

	request := websocket_pack.WsEpochRotationProofRequest{
		Route:             constants.WsRouteSignEpochRotationProof,
		EpochId:           epochId,
		NextEpochId:       nextEpochId,
		EpochDataHash:     epochDataHash,
		FinishedOnHeight:  boundary.FinishedOnHeight,
		FinishedOnBlockId: boundary.FinishedOnBlockId,
		FinishedOnHash:    boundary.FinishedOnHash,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataToVerify := utils.BuildEpochRotationProofSigningPayload(
		epochId,
		nextEpochId,
		epochDataHash,
		boundary.FinishedOnHeight,
		boundary.FinishedOnBlockId,
		boundary.FinishedOnHash,
	)

	validateProof := func(id string, raw []byte) bool {
		var response websocket_pack.WsEpochRotationProofResponse
		if json.Unmarshal(raw, &response) != nil {
			return false
		}
		if !slices.Contains(prevEpochHandler.Quorum, response.Voter) {
			return false
		}
		return cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig)
	}

	responses, ok := waiter.SendAndWaitValidated(ctx, message, prevEpochHandler.Quorum, wsConns, majority, validateProof)
	if !ok {
		return nil
	}

	proofs := make(map[string]string)
	for _, raw := range responses {
		var response websocket_pack.WsEpochRotationProofResponse
		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.AggregatedEpochRotationProof{
		EpochId:           epochId,
		NextEpochId:       nextEpochId,
		EpochData:         *localEpochData,
		EpochDataHash:     epochDataHash,
		FinishedOnHeight:  boundary.FinishedOnHeight,
		FinishedOnBlockId: boundary.FinishedOnBlockId,
		FinishedOnHash:    boundary.FinishedOnHash,
		Proofs:            proofs,
	}
}

func storeAggregatedEpochRotationProof(proof *structures.AggregatedEpochRotationProof) {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, proof.EpochId))
	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_THREAD_METADATA.Put(key, value, nil)
	}
}

func LoadAggregatedEpochRotationProof(epochId int) *structures.AggregatedEpochRotationProof {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, epochId))
	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}
	var proof structures.AggregatedEpochRotationProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}
	return &proof
}

func deliverAggregatedEpochRotationProofToAnchors(proof *structures.AggregatedEpochRotationProof) *structures.AggregatedAnchorEpochAckProof {
	message, err := json.Marshal(struct {
		Route string                                  `json:"route"`
		Proof structures.AggregatedEpochRotationProof `json:"proof"`
	}{
		Route: constants.WsRouteAcceptAggregatedEpochRotationProof,
		Proof: *proof,
	})

	if err != nil {
		return nil
	}

	type anchorAck struct {
		Anchor string
		Sig    string
	}

	// Iterate over the canonical anchor list (not the conn map) so we also try
	// anchors whose cached connection is currently nil — getOrRedialAnchorConn
	// will attempt a bounded lazy redial for them.
	anchors := globals.ANCHORS

	ackChan := make(chan anchorAck, len(anchors))
	var wg sync.WaitGroup

	for _, anchor := range anchors {
		if anchor.WssAnchorUrl == "" {
			continue
		}
		wg.Add(1)
		go func(pubkey, wsUrl string) {
			defer wg.Done()

			c := getOrRedialAnchorConn(pubkey, wsUrl)
			if c == nil {
				return
			}

			if err := c.WriteMessage(websocket.TextMessage, message); err != nil {
				dropAnchorConn(pubkey, c)
				// Lazy retry: redial once and try the same write again.
				if c2 := getOrRedialAnchorConn(pubkey, wsUrl); c2 != nil {
					if err := c2.WriteMessage(websocket.TextMessage, message); err != nil {
						dropAnchorConn(pubkey, c2)
						return
					}
					c = c2
				} else {
					return
				}
			}

			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, respBytes, err := c.ReadMessage()
			c.SetReadDeadline(time.Time{})
			if err != nil {
				dropAnchorConn(pubkey, c)
				return
			}

			var resp struct {
				Status    string `json:"status"`
				Anchor    string `json:"anchor"`
				Signature string `json:"signature"`
			}
			if json.Unmarshal(respBytes, &resp) != nil || resp.Status != "OK" || resp.Signature == "" || resp.Anchor == "" {
				return
			}

			ackChan <- anchorAck{Anchor: resp.Anchor, Sig: resp.Signature}
		}(anchor.Pubkey, anchor.WssAnchorUrl)
	}

	go func() {
		wg.Wait()
		close(ackChan)
	}()

	proofs := make(map[string]string)
	for ack := range ackChan {
		proofs[ack.Anchor] = ack.Sig
	}

	majority := utils.GetAnchorsQuorumMajority()
	if len(proofs) < majority {
		return nil
	}

	return &structures.AggregatedAnchorEpochAckProof{
		EpochId:       proof.EpochId,
		NextEpochId:   proof.NextEpochId,
		EpochDataHash: proof.EpochDataHash,
		Proofs:        proofs,
	}
}

func deliverAggregatedAnchorEpochAckProofToNewQuorum(proof *structures.AggregatedAnchorEpochAckProof, nextEpochHandler *structures.EpochDataHandler) {
	if proof == nil || nextEpochHandler == nil {
		return
	}

	message, err := json.Marshal(websocket_pack.WsAcceptAggregatedAnchorEpochAckProofRequest{
		Route: constants.WsRouteAcceptAggregatedAnchorEpochAckProof,
		Proof: *proof,
	})
	if err != nil {
		return
	}

	const (
		ackDialAttempts = 3
		ackDialBackoff  = 200 * time.Millisecond
	)

	for _, member := range nextEpochHandler.Quorum {
		if member == globals.CONFIGURATION.PublicKey {
			continue
		}
		validatorStorage := utils.GetValidatorFromApprovementThreadState(member)
		if validatorStorage == nil || validatorStorage.WssValidatorUrl == "" {
			continue
		}
		go func(memberPubkey, wsUrl string) {
			var conn *websocket.Conn
			for attempt := 1; attempt <= ackDialAttempts; attempt++ {
				c, _, dialErr := websocket.DefaultDialer.Dial(wsUrl, nil)
				if dialErr == nil {
					conn = c
					break
				}
				utils.LogWithTimeThrottled(
					"last_mile:ack:dial:"+memberPubkey,
					2*time.Second,
					fmt.Sprintf("Last mile: ack-proof dial to new-quorum member %s failed (attempt %d/%d): %v", memberPubkey, attempt, ackDialAttempts, dialErr),
					utils.YELLOW_COLOR,
				)
				if attempt < ackDialAttempts {
					time.Sleep(ackDialBackoff)
				}
			}
			if conn == nil {
				return
			}
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, message)
		}(member, validatorStorage.WssValidatorUrl)
	}
}

// persistFinalizerThreadMetadataLocked serializes handlers.FINALIZER_THREAD_METADATA.Handler
// and writes it to FINALIZATION_THREAD_METADATA under DBKeyFinalizerThreadMetadata.
//
// Caller MUST hold handlers.FINALIZER_THREAD_METADATA.RWMutex (Lock, not RLock).
func persistFinalizerThreadMetadataLocked() {
	payload, err := json.Marshal(&handlers.FINALIZER_THREAD_METADATA.Handler)
	if err != nil {
		utils.LogWithTime("FINALIZER_THREAD_METADATA: marshal failed: "+err.Error(), utils.RED_COLOR)
		return
	}

	if err := databases.FINALIZATION_THREAD_METADATA.Put([]byte(constants.DBKeyFinalizerThreadMetadata), payload, nil); err != nil {
		utils.LogWithTime("FINALIZER_THREAD_METADATA: persist failed: "+err.Error(), utils.RED_COLOR)
	}
}
