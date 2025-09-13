package structures

type NetworkParameters struct {
	ValidatorStake        uint64 `json:"VALIDATOR_STAKE"`
	MinimalStakePerEntity uint64 `json:"MINIMAL_STAKE_PER_ENTITY"`
	QuorumSize            int    `json:"QUORUM_SIZE"`
	EpochTime             int64  `json:"EPOCH_TIME"`
	LeadershipTimeframe   int64  `json:"LEADERSHIP_TIMEFRAME"`
	BlockTime             int64  `json:"BLOCK_TIME"`
	MaxBlockSizeInBytes   int64  `json:"MAX_BLOCK_SIZE_IN_BYTES"`
	TxLimitPerBlock       uint   `json:"TXS_LIMIT_PER_BLOCK"`
}

func CopyNetworkParameters(src NetworkParameters) NetworkParameters {
	return NetworkParameters{
		ValidatorStake:        src.ValidatorStake,
		MinimalStakePerEntity: src.MinimalStakePerEntity,
		QuorumSize:            src.QuorumSize,
		EpochTime:             src.EpochTime,
		LeadershipTimeframe:   src.LeadershipTimeframe,
		BlockTime:             src.BlockTime,
		MaxBlockSizeInBytes:   src.MaxBlockSizeInBytes,
		TxLimitPerBlock:       src.TxLimitPerBlock,
	}
}

type Staker struct {
	Stake uint64 `json:"stake"`
}

type PoolStorage struct {
	Pubkey      string            `json:"pubkey"`
	Percentage  uint8             `json:"percentage"`
	TotalStaked uint64            `json:"totalStaked"`
	Stakers     map[string]Staker `json:"stakers"`
	PoolUrl     string            `json:"poolURL"`
	WssPoolUrl  string            `json:"wssPoolURL"`
}

type Genesis struct {
	NetworkId                string             `json:"NETWORK_ID"`
	CoreMajorVersion         int                `json:"CORE_MAJOR_VERSION"`
	FirstEpochStartTimestamp uint64             `json:"FIRST_EPOCH_START_TIMESTAMP"`
	NetworkParameters        NetworkParameters  `json:"NETWORK_PARAMETERS"`
	Pools                    []PoolStorage      `json:"POOLS"`
	State                    map[string]Account `json:"STATE"`
}
