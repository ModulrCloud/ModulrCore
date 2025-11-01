package threads

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ModulrCloud/ModulrCore/databases"
	"github.com/ModulrCloud/ModulrCore/handlers"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
)

func LeaderRotationThread() {

	for {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

		epochHandlerRef := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

		/*

			We don't do rotation when it's the last leader. For example, if we have sequence:

			[L0,L1,L2,...Ln]

			If we reached Ln - no need to rotate, it's already finish

		*/
		haveNextCandidate := epochHandlerRef.CurrentLeaderIndex+1 < len(epochHandlerRef.LeadersSequence)

		if haveNextCandidate && timeIsOutForCurrentLeader(&handlers.APPROVEMENT_THREAD_METADATA.Handler) {

			storedEpochIndex := epochHandlerRef.Id

			handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

			handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Lock()

			threadMetadataHandlerRef := &handlers.APPROVEMENT_THREAD_METADATA.Handler

			if storedEpochIndex == threadMetadataHandlerRef.EpochDataHandler.Id {

				threadMetadataHandlerRef.EpochDataHandler.CurrentLeaderIndex++

				// Store the updated AT

				jsonedHandler, errMarshal := json.Marshal(threadMetadataHandlerRef)

				if errMarshal != nil {

					fmt.Printf("Failed to marshal AT state: %v", errMarshal)

					panic("Impossible to marshal approvement thread state")

				}

				if err := databases.APPROVEMENT_THREAD_METADATA.Put([]byte("AT"), jsonedHandler, nil); err != nil {

					fmt.Printf("Failed to store AT state: %v", err)

					panic("Impossible to store the approvement thread state")

				}

			}

			handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

		} else {

			handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		}

		time.Sleep(200 * time.Millisecond)

	}

}

func timeIsOutForCurrentLeader(approvementThread *structures.ApprovementThreadMetadataHandler) bool {

	// Function to check if time frame for current leader is done and we have to move to next leader in sequence

	leaderShipTimeframe := approvementThread.NetworkParameters.LeadershipDuration

	currentIndex := int64(approvementThread.EpochDataHandler.CurrentLeaderIndex)

	return utils.GetUTCTimestampInMilliSeconds() >= int64(approvementThread.EpochDataHandler.StartTimestamp)+(currentIndex+1)*leaderShipTimeframe

}
