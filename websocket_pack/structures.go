package websocket_pack

import (
	"github.com/ModulrCloud/ModulrCore/block_pack"
	"github.com/ModulrCloud/ModulrCore/structures"
)

type WsLeaderRotationProofRequest struct {
	Route                 string                                 `json:"route"`
	IndexOfLeaderToRotate int                                    `json:"indexOfLeaderToRotate"`
	AfpForFirstBlock      structures.AggregatedFinalizationProof `json:"afpForFirstBlock"`
	SkipData              structures.LeaderVotingStat            `json:"skipData"`
}

type WsLeaderRotationProofResponseOk struct {
	Voter           string `json:"voter"`
	ForLeaderPubkey string `json:"forLeaderPubkey"`
	Status          string `json:"status"`
	Sig             string `json:"sig"`
}

type WsLeaderRotationProofResponseUpgrade struct {
	Voter            string                                 `json:"voter"`
	ForLeaderPubkey  string                                 `json:"forLeaderPubkey"`
	Status           string                                 `json:"status"`
	AfpForFirstBlock structures.AggregatedFinalizationProof `json:"afpForFirstBlock"`
	SkipData         structures.LeaderVotingStat            `json:"skipData"`
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
