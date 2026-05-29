package tests

import (
	"testing"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func TestValidateRecoveryDataRequiresTeamSignatureForExactPayload(t *testing.T) {
	team := cryptography.GenerateKeyPair("", "", nil)
	data := buildSignedRecoveryDataForTest(t, team, 3, 42, "recovered-network")

	if err := utils.ValidateRecoveryData(data, team.Pub); err != nil {
		t.Fatalf("expected valid recovery data, got %v", err)
	}

	tamperedHeight := *data
	tamperedHeight.LastAbsoluteHeight++
	if err := utils.ValidateRecoveryData(&tamperedHeight, team.Pub); err == nil {
		t.Fatalf("expected tampered recovery height to invalidate team signature")
	}

	tamperedEpoch := *data
	tamperedEpoch.LastEpochIndex++
	if err := utils.ValidateRecoveryData(&tamperedEpoch, team.Pub); err == nil {
		t.Fatalf("expected tampered recovery epoch to invalidate team signature")
	}

	tamperedGenesis := *data
	tamperedGenesis.Genesis.NetworkId = "other-network"
	if err := utils.ValidateRecoveryData(&tamperedGenesis, team.Pub); err == nil {
		t.Fatalf("expected tampered recovery genesis to invalidate team signature")
	}

	otherTeam := cryptography.GenerateKeyPair("", "", nil)
	if err := utils.ValidateRecoveryData(data, otherTeam.Pub); err == nil {
		t.Fatalf("expected wrong team pubkey to reject recovery data")
	}
	if err := utils.ValidateRecoveryData(data, "not-a-pubkey"); err == nil {
		t.Fatalf("expected invalid team pubkey to reject recovery data")
	}
}

func TestLoadActiveRecoveryDataLoadsRegisteredPlanByHeight(t *testing.T) {
	databases.STATE = openTempDB(t)

	team := cryptography.GenerateKeyPair("", "", nil)
	data := buildSignedRecoveryDataForTest(t, team, 3, 42, "recovered-network")

	if loaded, err := utils.LoadActiveRecoveryData(); err != nil || loaded != nil {
		t.Fatalf("expected no active recovery before marker, got data=%+v err=%v", loaded, err)
	}
	if err := databases.STATE.Put([]byte(constants.DBKeyRecoveryActive), []byte("42"), nil); err != nil {
		t.Fatalf("failed to write recovery active marker: %v", err)
	}
	mustPutJSONToStateDB(t, constants.DBKeyPrefixRecoveryData+"42", data)

	loaded, err := utils.LoadActiveRecoveryData()
	if err != nil {
		t.Fatalf("expected active recovery data to load: %v", err)
	}
	if loaded == nil ||
		loaded.LastEpochIndex != data.LastEpochIndex ||
		loaded.LastAbsoluteHeight != data.LastAbsoluteHeight ||
		loaded.Genesis.NetworkId != data.Genesis.NetworkId ||
		loaded.TeamSig != data.TeamSig {
		t.Fatalf("unexpected loaded recovery data: %+v", loaded)
	}
}

func buildSignedRecoveryDataForTest(
	t *testing.T,
	team cryptography.Ed25519Box,
	lastEpochIndex int,
	lastHeight int64,
	networkId string,
) *structures.RecoveryData {
	t.Helper()

	genesis := structures.Genesis{
		NetworkId:                networkId,
		CoreMajorVersion:         7,
		FirstEpochStartTimestamp: 123456789,
		NetworkParameters:        structures.NetworkParameters{QuorumSize: 1},
	}
	payload, err := utils.BuildRecoveryPayload(lastEpochIndex, lastHeight, genesis)
	if err != nil {
		t.Fatalf("failed to build recovery payload: %v", err)
	}

	return &structures.RecoveryData{
		LastEpochIndex:     lastEpochIndex,
		LastAbsoluteHeight: lastHeight,
		Genesis:            genesis,
		TeamSig:            cryptography.GenerateSignature(team.Prv, payload),
	}
}
