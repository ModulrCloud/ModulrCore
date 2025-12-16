package websocket_pack

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func SendBlockAndAfpToPoD(block block_pack.Block, afp structures.AggregatedFinalizationProof) {
	req := WsBlockWithAfpStoreRequest{Route: "accept_block_with_afp", Block: block, Afp: afp}
	if reqBytes, err := json.Marshal(req); err == nil {
		_, _ = utils.SendWebsocketMessageToPoD(reqBytes)
	}
}

func SendAggregatedLeaderFinalizationProofToPoD(proof structures.AggregatedLeaderFinalizationProof) {
	req := WsAggregatedLeaderFinalizationProofStoreRequest{Route: "accept_aggregated_leader_finalization_proof", Proof: proof}
	if reqBytes, err := json.Marshal(req); err == nil {
		_, _ = utils.SendWebsocketMessageToPoD(reqBytes)
	}
}

func GetAggregatedLeaderFinalizationProofFromPoD(epochIndex int, leader string) *structures.AggregatedLeaderFinalizationProof {
	req := WsAggregatedLeaderFinalizationProofRequest{Route: "get_aggregated_leader_finalization_proof", EpochIndex: epochIndex, Leader: leader}
	if reqBytes, err := json.Marshal(req); err == nil {
		if respBytes, err := utils.SendWebsocketMessageToPoD(reqBytes); err == nil {
			var resp WsAggregatedLeaderFinalizationProofResponse
			if err := json.Unmarshal(respBytes, &resp); err == nil {
				return resp.Proof
			}
		}
	}
	return nil
}
