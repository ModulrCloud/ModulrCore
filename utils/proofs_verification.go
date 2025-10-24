package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/ModulrCloud/ModulrCore/cryptography"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
)

func VerifyAggregatedEpochFinalizationProof(proof *structures.AggregatedEpochFinalizationProof, quorum []string, majority int, epochFullID string) bool {

	dataThatShouldBeSigned := "EPOCH_DONE:" +
		strconv.Itoa(int(proof.LastLeader)) + ":" +
		strconv.Itoa(int(proof.LastIndex)) + ":" +
		proof.LastHash + ":" +
		proof.HashOfFirstBlockByLastLeader + ":" +
		epochFullID

	okSignatures := 0
	seen := make(map[string]bool)
	quorumMap := make(map[string]bool)

	for _, pk := range quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	for pubKey, signature := range proof.Proofs {

		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {
			loweredPubKey := strings.ToLower(pubKey)
			if quorumMap[loweredPubKey] && !seen[loweredPubKey] {
				seen[loweredPubKey] = true
				okSignatures++
			}
		}
	}

	return okSignatures >= majority

}

func VerifyAggregatedFinalizationProof(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochFullID}, ":")

	majority := GetQuorumMajority(epochHandler)

	okSignatures := 0

	seen := make(map[string]bool)

	quorumMap := make(map[string]bool)

	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	for pubKey, signature := range proof.Proofs {

		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {

			loweredPubKey := strings.ToLower(pubKey)

			if quorumMap[loweredPubKey] && !seen[loweredPubKey] {
				seen[loweredPubKey] = true
				okSignatures++
			}
		}
	}

	return okSignatures >= majority
}

func VerifyAggregatedLeaderRotationProof(prevLeaderPubKey string, proof *structures.AggregatedLeaderRotationProof, epochHandler *structures.EpochDataHandler) bool {

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := "LEADER_ROTATION_PROOF:" + prevLeaderPubKey + ":" +
		proof.FirstBlockHash + ":" +
		strconv.Itoa(proof.SkipIndex) + ":" +
		proof.SkipHash + ":" +
		epochFullID

	majority := GetQuorumMajority(epochHandler)

	okSignatures := 0
	seen := make(map[string]bool)
	quorumMap := make(map[string]bool)

	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	for pubKey, signature := range proof.Proofs {

		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {

			loweredPubKey := strings.ToLower(pubKey)

			if quorumMap[loweredPubKey] && !seen[loweredPubKey] {
				seen[loweredPubKey] = true
				okSignatures++
			}

		}

	}

	return okSignatures >= majority

}

func GetVerifiedAggregatedFinalizationProofByBlockId(blockID string, epochHandler *structures.EpochDataHandler) *structures.AggregatedFinalizationProof {

	localAfpAsBytes, err := globals.EPOCH_DATA.Get([]byte("AFP:"+blockID), nil)

	if err == nil {

		var localAfpParsed structures.AggregatedFinalizationProof

		err = json.Unmarshal(localAfpAsBytes, &localAfpParsed)

		if err != nil {
			return nil
		}

		return &localAfpParsed

	}

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)

	resultChan := make(chan *structures.AggregatedFinalizationProof, len(quorum))

	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel() // ensure cancellation if function exits early

	for _, node := range quorum {

		wg.Add(1)

		go func(endpoint string) {

			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/aggregated_finalization_proof/"+blockID, nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var afp structures.AggregatedFinalizationProof

			if json.NewDecoder(resp.Body).Decode(&afp) == nil && VerifyAggregatedFinalizationProof(&afp, epochHandler) {

				// send the first valid result and immediately cancel all other requests
				select {

				case resultChan <- &afp:
					cancel() // stop all remaining goroutines and HTTP requests
				default:

				}
			}

		}(node.Url)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Return first valid AFP
	for res := range resultChan {
		if res != nil {
			return res
		}
	}

	return nil
}
