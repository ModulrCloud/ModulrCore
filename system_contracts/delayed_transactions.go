package system_contracts

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
)

type DelayedTxExecutorFunction = func(map[string]string, string) bool

type versionVotingPayload struct {
	NewMajorVersion int `json:"newMajorVersion"`
}

type parametersVotingPayload struct {
	UpdateField string `json:"updateField"`
	NewValue    string `json:"newValue"`
}

var DELAYED_TRANSACTIONS_MAP = map[string]DelayedTxExecutorFunction{
	"createValidator": CreateValidator,
	"updateValidator": UpdateValidator,
	"stake":           Stake,
	"unstake":         Unstake,
	"votingAccept":    VotingAccept,
}

type threadContext struct {
	validatorsCache      map[string]*structures.ValidatorStorage
	db                   *leveldb.DB
	getValidator         func(pubkey string) *structures.ValidatorStorage
	putValidatorCache    func(key string, vs *structures.ValidatorStorage)
	markValidatorTouched func(key string, vs *structures.ValidatorStorage)
	touchValidatorCache  func(key string)
	networkParams        structures.NetworkParameters
	validatorsRegistry   *[]string
	onStakeDelta         func(delta int64)
	onUnstakeRefund      func(unstaker string, amount uint64)
}

func resolveContext(context string) (threadContext, bool) {
	switch context {
	case constants.ContextApprovementThread:
		return threadContext{
			validatorsCache:      handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache,
			db:                   databases.APPROVEMENT_THREAD_METADATA,
			getValidator:         utils.GetValidatorFromApprovementThreadStateUnderLock,
			putValidatorCache:    utils.PutApprovementValidatorCache,
			markValidatorTouched: utils.MarkApprovementValidatorTouched,
			touchValidatorCache:  utils.TouchApprovementValidatorCache,
			networkParams:        handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters,
			validatorsRegistry:   &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry,
		}, true

	case constants.ContextExecutionThread:
		return threadContext{
			validatorsCache:      handlers.EXECUTION_THREAD_METADATA.ValidatorsStoragesCache,
			db:                   databases.STATE,
			getValidator:         utils.GetValidatorFromExecThreadState,
			putValidatorCache:    utils.PutExecValidatorCache,
			markValidatorTouched: utils.MarkExecValidatorTouched,
			touchValidatorCache:  utils.TouchExecValidatorCache,
			networkParams:        handlers.EXECUTION_THREAD_METADATA.ChainCursor.NetworkParameters,
			validatorsRegistry:   &handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochDataHandler.ValidatorsRegistry,
			onStakeDelta: func(delta int64) {
				handlers.EXECUTION_THREAD_METADATA.ChainCursor.Statistics.StakingDelta += delta
				handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochStatistics.StakingDelta += delta
			},
			onUnstakeRefund: func(unstaker string, amount uint64) {
				unstakerAccount := utils.GetAccountFromExecThreadState(unstaker)
				unstakerAccount.Balance += amount
			},
		}, true

	default:
		return threadContext{}, false
	}
}

func CreateValidator(delayedTransaction map[string]string, context string) bool {
	validatorPubkey := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	validatorURL := delayedTransaction["validatorURL"]
	wssValidatorURL := delayedTransaction["wssValidatorURL"]

	if validatorURL == "" || wssValidatorURL == "" || percentage > 100 {
		return false
	}

	tc, ok := resolveContext(context)
	if !ok {
		return false
	}

	validatorStorageKey := constants.DBKeyPrefixValidatorStorage + validatorPubkey

	if _, existsInCache := tc.validatorsCache[validatorStorageKey]; existsInCache {
		return false
	}

	_, existErr := tc.db.Get([]byte(validatorStorageKey), nil)
	if existErr != leveldb.ErrNotFound {
		return false
	}

	vs := &structures.ValidatorStorage{
		Pubkey:      validatorPubkey,
		Percentage:  percentage,
		TotalStaked: 0,
		Stakers: map[string]uint64{
			validatorPubkey: 0,
		},
		ValidatorUrl:    validatorURL,
		WssValidatorUrl: wssValidatorURL,
	}

	tc.putValidatorCache(validatorStorageKey, vs)
	tc.markValidatorTouched(validatorStorageKey, vs)

	return true
}

func UpdateValidator(delayedTransaction map[string]string, context string) bool {
	validatorPubkey := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	validatorURL := delayedTransaction["validatorURL"]
	wssValidatorURL := delayedTransaction["wssValidatorURL"]

	if validatorURL == "" || wssValidatorURL == "" || percentage > 100 {
		return false
	}

	tc, ok := resolveContext(context)
	if !ok {
		return false
	}

	validatorStorageId := constants.DBKeyPrefixValidatorStorage + validatorPubkey
	validatorStorage := tc.getValidator(validatorPubkey)

	if validatorStorage == nil {
		return false
	}

	validatorStorage.Percentage = percentage
	validatorStorage.ValidatorUrl = validatorURL
	validatorStorage.WssValidatorUrl = wssValidatorURL

	tc.markValidatorTouched(validatorStorageId, validatorStorage)
	tc.touchValidatorCache(validatorStorageId)

	return true
}

func Stake(delayedTransaction map[string]string, context string) bool {
	staker := delayedTransaction["staker"]
	validatorPubkey := delayedTransaction["validatorPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {
		return false
	}

	tc, ok := resolveContext(context)
	if !ok {
		return false
	}

	validatorStorage := tc.getValidator(validatorPubkey)

	if validatorStorage == nil {
		return false
	}

	if amount < tc.networkParams.MinimalStakePerStaker {
		return false
	}

	currentStake := validatorStorage.Stakers[staker]
	currentStake += amount

	validatorStorage.TotalStaked += amount
	validatorStorage.Stakers[staker] = currentStake

	if tc.onStakeDelta != nil {
		tc.onStakeDelta(int64(amount))
	}

	if validatorStorage.TotalStaked >= tc.networkParams.ValidatorRequiredStake {
		if !slices.Contains(*tc.validatorsRegistry, validatorPubkey) {
			*tc.validatorsRegistry = append(*tc.validatorsRegistry, validatorPubkey)
		}
	}

	return true
}

func Unstake(delayedTransaction map[string]string, context string) bool {
	unstaker := delayedTransaction["unstaker"]
	validatorPubkey := delayedTransaction["validatorPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {
		return false
	}

	tc, ok := resolveContext(context)
	if !ok {
		return false
	}

	validatorStorage := tc.getValidator(validatorPubkey)

	if validatorStorage == nil {
		return false
	}

	stakerStake, exists := validatorStorage.Stakers[unstaker]

	if !exists {
		return false
	}

	if stakerStake < amount {
		return false
	}

	stakerStake -= amount
	validatorStorage.TotalStaked -= amount

	if tc.onStakeDelta != nil {
		tc.onStakeDelta(-int64(amount))
	}

	if tc.onUnstakeRefund != nil {
		tc.onUnstakeRefund(unstaker, amount)
	}

	if stakerStake == 0 {
		delete(validatorStorage.Stakers, unstaker)
	} else {
		validatorStorage.Stakers[unstaker] = stakerStake
	}

	if validatorStorage.TotalStaked < tc.networkParams.ValidatorRequiredStake {
		*tc.validatorsRegistry = removeFromSlice(*tc.validatorsRegistry, validatorPubkey)
	}

	return true
}

func VotingAccept(delayedTransaction map[string]string, context string) bool {
	votingType := delayedTransaction["votingType"]

	quorumAgreements := collectVotingAgreements(delayedTransaction)
	if len(quorumAgreements) == 0 {
		return false
	}

	epochHandler, ok := currentEpochHandlerForContext(context)
	if !ok {
		return false
	}

	switch votingType {
	case "version":
		newMajorVersion, err := strconv.Atoi(delayedTransaction["newMajorVersion"])
		if err != nil || newMajorVersion < 0 {
			return false
		}

		payload := versionVotingPayload{NewMajorVersion: newMajorVersion}
		if !verifyVotingAcceptMajority(epochHandler, votingType, payload, quorumAgreements) {
			return false
		}

		switch context {
		case constants.ContextApprovementThread:
			handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion = newMajorVersion
		case constants.ContextExecutionThread:
			handlers.EXECUTION_THREAD_METADATA.ChainCursor.CoreMajorVersion = newMajorVersion
		default:
			return false
		}

	case "parameters":
		payload := parametersVotingPayload{
			UpdateField: delayedTransaction["updateField"],
			NewValue:    delayedTransaction["newValue"],
		}

		if !networkParameterUpdateIsValid(payload.UpdateField, payload.NewValue) {
			return false
		}

		if !verifyVotingAcceptMajority(epochHandler, votingType, payload, quorumAgreements) {
			return false
		}

		switch context {
		case constants.ContextApprovementThread:
			return applyNetworkParameterUpdate(&handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters, payload.UpdateField, payload.NewValue)
		case constants.ContextExecutionThread:
			return applyNetworkParameterUpdate(&handlers.EXECUTION_THREAD_METADATA.ChainCursor.NetworkParameters, payload.UpdateField, payload.NewValue)
		default:
			return false
		}

	default:
		return false
	}

	return true
}

func collectVotingAgreements(delayedTransaction map[string]string) map[string]string {
	quorumAgreements := map[string]string{}
	if err := json.Unmarshal([]byte(delayedTransaction["agreements"]), &quorumAgreements); err != nil {
		return map[string]string{}
	}

	return quorumAgreements
}

func currentEpochHandlerForContext(context string) (*structures.EpochDataHandler, bool) {
	switch context {
	case constants.ContextApprovementThread:
		return &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler, true
	case constants.ContextExecutionThread:
		return &handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochDataHandler, true
	default:
		return nil, false
	}
}

func verifyVotingAcceptMajority(epochHandler *structures.EpochDataHandler, votingType string, votingPayload any, quorumAgreements map[string]string) bool {
	if epochHandler == nil || len(quorumAgreements) == 0 {
		return false
	}

	dataThatShouldBeSigned, ok := buildVotingAcceptSigningPayload(epochHandler, votingType, votingPayload)
	if !ok {
		return false
	}

	quorumMap := make(map[string]bool, len(epochHandler.Quorum))
	for _, pubkey := range epochHandler.Quorum {
		quorumMap[pubkey] = true
	}

	unique := make(map[string]bool, len(quorumAgreements))
	okSignatures := 0

	for signerPubkey, signature := range quorumAgreements {
		if unique[signerPubkey] || !quorumMap[signerPubkey] {
			continue
		}

		if cryptography.VerifySignature(dataThatShouldBeSigned, signerPubkey, signature) {
			unique[signerPubkey] = true
			okSignatures++
		}
	}

	return okSignatures >= utils.GetQuorumMajority(epochHandler)
}

func BuildVotingAcceptSigningPayload(epochHandler *structures.EpochDataHandler, votingType string, newMajorVersion int) (string, bool) {
	if votingType != "version" {
		return "", false
	}

	return buildVotingAcceptSigningPayload(epochHandler, votingType, versionVotingPayload{NewMajorVersion: newMajorVersion})
}

func BuildVotingAcceptParametersSigningPayload(epochHandler *structures.EpochDataHandler, updateField string, newValue string) (string, bool) {
	return buildVotingAcceptSigningPayload(epochHandler, "parameters", parametersVotingPayload{UpdateField: updateField, NewValue: newValue})
}

func buildVotingAcceptSigningPayload(epochHandler *structures.EpochDataHandler, votingType string, votingPayload any) (string, bool) {
	if epochHandler == nil {
		return "", false
	}

	payloadBytes, err := json.Marshal(votingPayload)
	if err != nil {
		return "", false
	}

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	return strings.Join([]string{
		"votingAccept",
		epochFullID,
		votingType,
		string(payloadBytes),
	}, ":"), true
}

func networkParameterUpdateIsValid(updateField string, newValue string) bool {
	params := structures.NetworkParameters{}
	return applyNetworkParameterUpdate(&params, updateField, newValue)
}

func applyNetworkParameterUpdate(params *structures.NetworkParameters, updateField string, newValue string) bool {
	if params == nil {
		return false
	}

	switch updateField {
	case "VALIDATOR_REQUIRED_STAKE":
		value, ok := parseUint64NetworkParameter(newValue)
		if !ok {
			return false
		}
		params.ValidatorRequiredStake = value

	case "MINIMAL_STAKE_PER_STAKER":
		value, ok := parseUint64NetworkParameter(newValue)
		if !ok {
			return false
		}
		params.MinimalStakePerStaker = value

	case "QUORUM_SIZE":
		value, ok := parseIntNetworkParameter(newValue)
		if !ok {
			return false
		}
		params.QuorumSize = value

	case "EPOCH_DURATION":
		value, ok := parseInt64NetworkParameter(newValue)
		if !ok {
			return false
		}
		params.EpochDuration = value

	case "LEADERSHIP_DURATION":
		value, ok := parseInt64NetworkParameter(newValue)
		if !ok {
			return false
		}
		params.LeadershipDuration = value

	case "BLOCK_TIME":
		value, ok := parseInt64NetworkParameter(newValue)
		if !ok {
			return false
		}
		params.BlockTime = value

	case "MAX_BLOCK_SIZE_IN_BYTES":
		value, ok := parseInt64NetworkParameter(newValue)
		if !ok {
			return false
		}
		params.MaxBlockSizeInBytes = value

	case "TXS_LIMIT_PER_BLOCK":
		value, ok := parseIntNetworkParameter(newValue)
		if !ok {
			return false
		}
		params.TxLimitPerBlock = value

	default:
		return false
	}

	return true
}

func parseUint64NetworkParameter(raw string) (uint64, bool) {
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}

func parseIntNetworkParameter(raw string) (int, bool) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, false
	}

	return value, true
}

func parseInt64NetworkParameter(raw string) (int64, bool) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, false
	}

	return value, true
}

func removeFromSlice[T comparable](s []T, v T) []T {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
