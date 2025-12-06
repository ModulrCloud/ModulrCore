package structures

type AggregatedLeaderFinalizationProof struct {
	EpochIndex int               `json:"epochIndex"`
	Leader     string            `json:"leader"`
	VotingStat VotingStat        `json:"votingStat"`
	Signatures map[string]string `json:"signatures"`
}
