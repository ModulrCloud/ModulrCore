package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

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

func configureLeaderFinalizationRouteState(t *testing.T) cryptography.Ed25519Box {
	t.Helper()

	keyPair := cryptography.GenerateKeyPair("", "", nil)
	globals.CONFIGURATION.PublicKey = keyPair.Pub
	globals.CONFIGURATION.PrivateKey = keyPair.Prv
	globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)
	databases.FINALIZATION_THREAD_METADATA = openTempDB(t)
	databases.EPOCH_DATA = openTempDB(t)
	databases.APPROVEMENT_THREAD_METADATA = openTempDB(t)

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
