package structures

type VotingStat struct {
	Index int                         `json:"index"`
	Hash  string                      `json:"hash"`
	Afp   AggregatedFinalizationProof `json:"afp"`
}

func NewLeaderVotingStatTemplate() VotingStat {

	return VotingStat{
		Index: -1,
		Hash:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Afp:   AggregatedFinalizationProof{},
	}

}
