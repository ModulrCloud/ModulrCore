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

const votingAgreementPrefix = "agreement:"

type versionVotingPayload struct {
	NewMajorVersion int `json:"newMajorVersion"`
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
	if votingType != "version" {
		return false
	}

	newMajorVersion, err := strconv.Atoi(delayedTransaction["newMajorVersion"])
	if err != nil || newMajorVersion < 0 {
		return false
	}

	quorumAgreements := collectVotingAgreements(delayedTransaction)
	if len(quorumAgreements) == 0 {
		return false
	}

	epochHandler, ok := currentEpochHandlerForContext(context)
	if !ok {
		return false
	}

	if !verifyVotingAcceptMajority(epochHandler, votingType, newMajorVersion, quorumAgreements) {
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

	return true
}

func collectVotingAgreements(delayedTransaction map[string]string) map[string]string {
	quorumAgreements := make(map[string]string)

	for key, signature := range delayedTransaction {
		if !strings.HasPrefix(key, votingAgreementPrefix) || signature == "" {
			continue
		}

		pubkey := strings.TrimPrefix(key, votingAgreementPrefix)
		if pubkey == "" {
			continue
		}

		quorumAgreements[pubkey] = signature
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

func verifyVotingAcceptMajority(epochHandler *structures.EpochDataHandler, votingType string, newMajorVersion int, quorumAgreements map[string]string) bool {
	if epochHandler == nil || len(quorumAgreements) == 0 {
		return false
	}

	dataThatShouldBeSigned, ok := BuildVotingAcceptSigningPayload(epochHandler, votingType, newMajorVersion)
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
	if epochHandler == nil || votingType != "version" {
		return "", false
	}

	payloadBytes, err := json.Marshal(versionVotingPayload{NewMajorVersion: newMajorVersion})
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

func removeFromSlice[T comparable](s []T, v T) []T {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
