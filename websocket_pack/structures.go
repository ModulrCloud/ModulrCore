package websocket_pack

import (
	"github.com/modulrcloud/modulr-core/anchors_pack"
	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/structures"
)

type WsLeaderFinalizationProofRequest struct {
	Route                   string                `json:"route"`
	IndexOfLeaderToFinalize int                   `json:"indexOfLeaderToFinalize"`
	SkipData                structures.VotingStat `json:"skipData"`
}

type WsLeaderFinalizationProofResponseOk struct {
	Voter           string `json:"voter"`
	ForLeaderPubkey string `json:"forLeaderPubkey"`
	Status          string `json:"status"`
	Sig             string `json:"sig"`
}

type WsLeaderFinalizationProofResponseUpgrade struct {
	Voter           string                `json:"voter"`
	ForLeaderPubkey string                `json:"forLeaderPubkey"`
	Status          string                `json:"status"`
	SkipData        structures.VotingStat `json:"skipData"`
}

type WsFinalizationProofRequest struct {
	Route            string                                 `json:"route"`
	Block            block_pack.Block                       `json:"block"`
	PreviousBlockAfp structures.AggregatedFinalizationProof `json:"previousBlockAfp"`
}

type WsFinalizationProofResponse struct {
	Voter             string `json:"voter"`
	FinalizationProof string `json:"finalizationProof"`
	VotedForHash      string `json:"votedForHash"`
}

type WsBlockWithAfpRequest struct {
	Route   string `json:"route"`
	BlockId string `json:"blockID"`
}

type WsBlockWithAfpResponse struct {
	Block *block_pack.Block                       `json:"block"`
	Afp   *structures.AggregatedFinalizationProof `json:"afp"`
}

type WsAnchorBlockWithAfpRequest struct {
	Route   string `json:"route"`
	BlockId string `json:"blockID"`
}

type WsAnchorBlockWithAfpResponse struct {
	Block *anchors_pack.AnchorBlock               `json:"block"`
	Afp   *structures.AggregatedFinalizationProof `json:"afp"`
}
