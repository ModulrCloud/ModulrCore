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
