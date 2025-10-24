package structures

type LeaderVotingStat struct {
	Index int                         `json:"index"`
	Hash  string                      `json:"hash"`
	Afp   AggregatedFinalizationProof `json:"afp"`
}

func NewLeaderVotingStatTemplate() LeaderVotingStat {

	return LeaderVotingStat{
		Index: -1,
		Hash:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Afp:   AggregatedFinalizationProof{},
	}

}
