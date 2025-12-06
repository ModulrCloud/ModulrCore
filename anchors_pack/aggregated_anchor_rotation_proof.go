package anchors_pack

import "github.com/modulrcloud/modulr-core/structures"

type AggregatedAnchorRotationProof struct {
	EpochIndex int                   `json:"epochIndex"`
	Anchor     string                `json:"anchor"`
	VotingStat structures.VotingStat `json:"votingStat"`
	Signatures map[string]string     `json:"signatures"`
}
