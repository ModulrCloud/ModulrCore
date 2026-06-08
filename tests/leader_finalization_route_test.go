package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

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
	"github.com/lxzan/gws"
)

func TestGetLeaderFinalizationProofReturnsNotReadyForActiveLastLeader(t *testing.T) {
	validator := configureLeaderFinalizationRouteState(t)
	leader := "leader-last"
	epochHandler := structures.EpochDataHandler{
		Id:                 2,
		Hash:               "epoch-hash",
		Quorum:             []string{validator.Pub},
		LeadersSequence:    []string{"leader-first", leader},
		CurrentLeaderIndex: 1,
		StartTimestamp:     uint64(time.Now().Add(time.Hour).UnixMilli()),
	}
	setActiveApprovementEpochForLeaderFinalizationTest(epochHandler, structures.NetworkParameters{EpochDuration: int64(time.Hour / time.Millisecond)})

	resp := requestLeaderFinalizationProof(t, websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   constants.WsRouteGetLeaderFinalizationProof,
		EpochIndex:              epochHandler.Id,
		IndexOfLeaderToFinalize: 1,
		SkipData:                structures.NewLeaderVotingStatTemplate(),
	})

	if resp["status"] != "NOT_READY" {
		t.Fatalf("expected NOT_READY for active last leader, got %+v", resp)
	}
}

// Regression: the last leader of an epoch must not be finalizable just because the
// epoch stopped being fresh (time-based). It must wait for the EPOCH_FINISH:N lock —
// the same signal that stops AFP voting. Otherwise a validator could sign the leader
// finalization proof at a stale index while still voting AFPs for later blocks,
// splitting the epoch boundary and stalling the network.
func TestGetLeaderFinalizationProofReturnsNotReadyForLastLeaderWithoutEpochFinishSignal(t *testing.T) {
	validator := configureLeaderFinalizationRouteState(t)
	leader := "leader-last"
	epochHandler := structures.EpochDataHandler{
		Id:                 2,
		Hash:               "epoch-hash",
		Quorum:             []string{validator.Pub},
		LeadersSequence:    []string{"leader-first", leader},
		CurrentLeaderIndex: 1,
		StartTimestamp:     uint64(time.Now().Add(-time.Hour).UnixMilli()),
	}
	// Epoch is no longer fresh, but EPOCH_FINISH:N has NOT been raised yet.
	setActiveApprovementEpochForLeaderFinalizationTest(epochHandler, structures.NetworkParameters{EpochDuration: 1})

	resp := requestLeaderFinalizationProof(t, websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   constants.WsRouteGetLeaderFinalizationProof,
		EpochIndex:              epochHandler.Id,
		IndexOfLeaderToFinalize: 1,
		SkipData:                structures.NewLeaderVotingStatTemplate(),
	})

	if resp["status"] != "NOT_READY" {
		t.Fatalf("expected NOT_READY for last leader without EPOCH_FINISH signal, got %+v", resp)
	}
}

// Once EPOCH_FINISH:N is set (AFP voting is locked), the last leader becomes
// finalizable and the route signs the proof.
func TestGetLeaderFinalizationProofReturnsOKForLastLeaderAfterEpochFinishSignal(t *testing.T) {
	validator := configureLeaderFinalizationRouteState(t)
	leader := "leader-last"
	epochHandler := structures.EpochDataHandler{
		Id:                 2,
		Hash:               "epoch-hash",
		Quorum:             []string{validator.Pub},
		LeadersSequence:    []string{"leader-first", leader},
		CurrentLeaderIndex: 1,
		StartTimestamp:     uint64(time.Now().Add(-time.Hour).UnixMilli()),
	}
	setActiveApprovementEpochForLeaderFinalizationTest(epochHandler, structures.NetworkParameters{EpochDuration: 1})

	// Raise the EPOCH_FINISH:N lock — the same signal that stops AFP voting.
	if err := databases.EPOCH_DATA.Put([]byte(constants.DBKeyPrefixEpochFinish+strconv.Itoa(epochHandler.Id)), []byte("TRUE"), nil); err != nil {
		t.Fatalf("failed to set EPOCH_FINISH signal: %v", err)
	}

	resp := requestLeaderFinalizationProof(t, websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   constants.WsRouteGetLeaderFinalizationProof,
		EpochIndex:              epochHandler.Id,
		IndexOfLeaderToFinalize: 1,
		SkipData:                structures.NewLeaderVotingStatTemplate(),
	})

	if resp["status"] != "OK" || resp["voter"] != validator.Pub || resp["forLeaderPubkey"] != leader {
		t.Fatalf("expected OK for last leader after EPOCH_FINISH signal, got %+v", resp)
	}
}

func TestGetLeaderFinalizationProofReturnsOKForCompletedLeader(t *testing.T) {
	validator := configureLeaderFinalizationRouteState(t)
	leader := "leader-first"
	epochHandler := structures.EpochDataHandler{
		Id:                 2,
		Hash:               "epoch-hash",
		Quorum:             []string{validator.Pub},
		LeadersSequence:    []string{leader, "leader-last"},
		CurrentLeaderIndex: 1,
		StartTimestamp:     uint64(time.Now().Add(-time.Hour).UnixMilli()),
	}
	setActiveApprovementEpochForLeaderFinalizationTest(epochHandler, structures.NetworkParameters{EpochDuration: 1})

	resp := requestLeaderFinalizationProof(t, websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   constants.WsRouteGetLeaderFinalizationProof,
		EpochIndex:              epochHandler.Id,
		IndexOfLeaderToFinalize: 0,
		SkipData:                structures.NewLeaderVotingStatTemplate(),
	})

	if resp["status"] != "OK" || resp["voter"] != validator.Pub || resp["forLeaderPubkey"] != leader {
		t.Fatalf("unexpected OK response: %+v", resp)
	}
	sig, _ := resp["sig"].(string)
	payload := strings.Join([]string{
		constants.SigningPrefixLeaderFinalization,
		leader,
		"-1",
		constants.ZeroHash,
		epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id),
	}, ":")
	if !cryptography.VerifySignature(payload, validator.Pub, sig) {
		t.Fatalf("leader finalization signature does not verify")
	}
}

func TestGetLeaderFinalizationProofReturnsUpgradeForHigherLocalVotingStat(t *testing.T) {
	validator := configureLeaderFinalizationRouteState(t)
	leader := "leader-first"
	epochHandler := structures.EpochDataHandler{
		Id:                 2,
		Hash:               "epoch-hash",
		Quorum:             []string{validator.Pub},
		LeadersSequence:    []string{leader, "leader-last"},
		CurrentLeaderIndex: 1,
		StartTimestamp:     uint64(time.Now().Add(-time.Hour).UnixMilli()),
	}
	setActiveApprovementEpochForLeaderFinalizationTest(epochHandler, structures.NetworkParameters{EpochDuration: 1})

	localVotingStat := buildCoreLeaderVotingStatForTest(t, epochHandler, validator, leader, 7)
	writeJSONToFinalizationDB(t, strconv.Itoa(epochHandler.Id)+":"+leader, localVotingStat)

	resp := requestLeaderFinalizationProof(t, websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   constants.WsRouteGetLeaderFinalizationProof,
		EpochIndex:              epochHandler.Id,
		IndexOfLeaderToFinalize: 0,
		SkipData:                structures.NewLeaderVotingStatTemplate(),
	})

	if resp["status"] != "UPGRADE" || resp["voter"] != validator.Pub || resp["forLeaderPubkey"] != leader {
		t.Fatalf("unexpected UPGRADE response: %+v", resp)
	}

	raw, err := json.Marshal(resp["skipData"])
	if err != nil {
		t.Fatalf("failed to remarshal skipData: %v", err)
	}
	var skipData structures.VotingStat
	if err := json.Unmarshal(raw, &skipData); err != nil {
		t.Fatalf("failed to decode skipData: %v", err)
	}
	if skipData.Index != localVotingStat.Index || skipData.Hash != localVotingStat.Hash {
		t.Fatalf("unexpected upgraded skipData: %+v", skipData)
	}
	if !utils.VerifyAggregatedFinalizationProof(&skipData.Afp, &epochHandler) {
		t.Fatalf("upgraded skipData AFP does not verify")
	}
}

func TestGetFinalizationProofDoesNotSignConflictingBlocksConcurrently(t *testing.T) {
	validator := configureLeaderFinalizationRouteState(t)
	leader := cryptography.GenerateKeyPair("", "", nil)
	epochHandler := structures.EpochDataHandler{
		Id:                 2,
		Hash:               "epoch-hash",
		Quorum:             []string{validator.Pub},
		LeadersSequence:    []string{leader.Pub},
		CurrentLeaderIndex: 0,
		StartTimestamp:     uint64(time.Now().UnixMilli()),
	}
	setActiveApprovementEpochForLeaderFinalizationTest(epochHandler, structures.NetworkParameters{EpochDuration: int64(time.Hour / time.Millisecond)})

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)
	blockA := buildSignedFinalizationBlockForTest(t, leader, epochFullID, 0, "conflict-a")
	blockB := buildSignedFinalizationBlockForTest(t, leader, epochFullID, 0, "conflict-b")
	if blockA.GetHash() == blockB.GetHash() {
		t.Fatalf("test setup produced identical block hashes")
	}

	serverURL := startLeaderFinalizationWebsocketServer(t)
	requests := []websocket_pack.WsFinalizationProofRequest{
		{
			Route: constants.WsRouteGetFinalizationProof,
			Block: blockA,
		},
		{
			Route: constants.WsRouteGetFinalizationProof,
			Block: blockB,
		},
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	responses := make([]map[string]any, len(requests))
	errs := make([]error, len(requests))
	for i := range requests {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			responses[idx], errs[idx] = requestFinalizationProofFromServer(serverURL, requests[idx])
		}(i)
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("finalization proof request failed: %v", err)
		}
	}

	signedHashes := make(map[string]bool)
	for _, resp := range responses {
		if sig, _ := resp["finalizationProof"].(string); sig == "" {
			continue
		}
		votedForHash, _ := resp["votedForHash"].(string)
		if votedForHash == "" {
			t.Fatalf("signed response is missing votedForHash: %+v", resp)
		}
		signedHashes[votedForHash] = true
	}

	if len(signedHashes) == 0 {
		t.Fatalf("expected one block to receive a finalization signature, got responses %+v", responses)
	}
	if len(signedHashes) > 1 {
		t.Fatalf("validator signed conflicting hashes for the same block id: %+v responses=%+v", signedHashes, responses)
	}
}

func configureLeaderFinalizationRouteState(t *testing.T) cryptography.Ed25519Box {
	t.Helper()

	keyPair := cryptography.GenerateKeyPair("", "", nil)
	globals.CONFIGURATION.PublicKey = keyPair.Pub
	globals.CONFIGURATION.PrivateKey = keyPair.Prv
	globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)
	databases.FINALIZATION_THREAD_METADATA = openTempDB(t)
	databases.EPOCH_DATA = openTempDB(t)
	databases.APPROVEMENT_THREAD_METADATA = openTempDB(t)
	databases.BLOCKS = openTempDB(t)

	t.Cleanup(func() {
		globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)
	})

	return keyPair
}

func setActiveApprovementEpochForLeaderFinalizationTest(epochHandler structures.EpochDataHandler, params structures.NetworkParameters) {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Lock()
	defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

	handlers.APPROVEMENT_THREAD_METADATA.Handler = structures.ApprovementThreadMetadataHandler{
		CoreMajorVersion:  1,
		NetworkParameters: params,
		EpochDataHandler:  epochHandler,
	}
}

func requestLeaderFinalizationProof(t *testing.T, request websocket_pack.WsLeaderFinalizationProofRequest) map[string]any {
	t.Helper()

	serverURL := startLeaderFinalizationWebsocketServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket test server: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write websocket request: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read websocket response: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("failed to decode websocket response %q: %v", raw, err)
	}
	return resp
}

func requestFinalizationProofFromServer(serverURL string, request websocket_pack.WsFinalizationProofRequest) (map[string]any, error) {
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial websocket test server: %w", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(request); err != nil {
		return nil, fmt.Errorf("write websocket request: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read websocket response: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode websocket response %q: %w", raw, err)
	}
	return resp, nil
}

func startLeaderFinalizationWebsocketServer(t *testing.T) string {
	t.Helper()

	upgrader := gws.NewUpgrader(&websocket_pack.Handler{}, &gws.ServerOption{
		ParallelEnabled: true,
		Recovery:        gws.Recovery,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r)
		if err != nil {
			t.Errorf("failed to upgrade websocket request: %v", err)
			return
		}
		go conn.ReadLoop()
	}))
	t.Cleanup(server.Close)

	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func buildSignedFinalizationBlockForTest(t *testing.T, leader cryptography.Ed25519Box, epochFullID string, index int, variant string) block_pack.Block {
	t.Helper()

	block := block_pack.Block{
		Creator: leader.Pub,
		Time:    int64(1000 + index),
		Epoch:   epochFullID,
		Transactions: []structures.Transaction{
			{
				Nonce:   uint64(index + 1),
				Payload: map[string]string{"variant": variant},
			},
		},
		ExtraData: block_pack.ExtraDataToBlock{
			Rest: map[string]string{"variant": variant},
		},
		Index:    index,
		PrevHash: constants.ZeroHash,
	}
	block.Sig = cryptography.GenerateSignature(leader.Prv, block.GetHash())
	if !block.VerifySignature() {
		t.Fatalf("test block signature does not verify")
	}

	return block
}

func buildCoreLeaderVotingStatForTest(
	t *testing.T,
	epochHandler structures.EpochDataHandler,
	validator cryptography.Ed25519Box,
	leader string,
	index int,
) structures.VotingStat {
	t.Helper()

	prevBlockHash := "prev-core-block-hash"
	blockID := strconv.Itoa(epochHandler.Id) + ":" + leader + ":" + strconv.Itoa(index)
	blockHash := "core-block-hash-" + strconv.Itoa(index)
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)
	afpPayload := strings.Join([]string{prevBlockHash, blockID, blockHash, epochFullID}, ":")

	return structures.VotingStat{
		Index: index,
		Hash:  blockHash,
		Afp: structures.AggregatedFinalizationProof{
			PrevBlockHash: prevBlockHash,
			BlockId:       blockID,
			BlockHash:     blockHash,
			Proofs: map[string]string{
				validator.Pub: cryptography.GenerateSignature(validator.Prv, afpPayload),
			},
		},
	}
}
