package websocket_pack

import (
	"github.com/Undchainorg/UndchainCore/block"
	"github.com/Undchainorg/UndchainCore/structures"
)

type WsLeaderRotationProofRequest struct {
	Route               string                                 `json:"route"`
	IndexOfPoolToRotate int                                    `json:"indexOfPoolToRotate"`
	AfpForFirstBlock    structures.AggregatedFinalizationProof `json:"afpForFirstBlock"`
	SkipData            structures.PoolVotingStat              `json:"skipData"`
}

type WsLeaderRotationProofResponseOk struct {
	Voter         string `json:"voter"`
	ForPoolPubkey string `json:"forPoolPubkey"`
	Status        string `json:"status"`
	Sig           string `json:"sig"`
}

type WsLeaderRotationProofResponseUpgrade struct {
	Voter            string                                 `json:"voter"`
	ForPoolPubkey    string                                 `json:"forPoolPubkey"`
	Status           string                                 `json:"status"`
	AfpForFirstBlock structures.AggregatedFinalizationProof `json:"afpForFirstBlock"`
	SkipData         structures.PoolVotingStat              `json:"skipData"`
}

type WsFinalizationProofRequest struct {
	Route            string                                 `json:"route"`
	Block            block.Block                            `json:"block"`
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
	Block *block.Block                            `json:"block"`
	Afp   *structures.AggregatedFinalizationProof `json:"afp"`
}
