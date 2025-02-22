package arwenmandos

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	mj "github.com/ElrondNetwork/arwen-wasm-vm/mandos-go/json/model"
	"github.com/ElrondNetwork/elrond-go/core/vmcommon"
	vmi "github.com/ElrondNetwork/elrond-go/core/vmcommon"
)

func (ae *ArwenTestExecutor) executeTx(txIndex string, tx *mj.Transaction) (*vmi.VMOutput, error) {
	if tx.Type.HasSender() {
		beforeErr := ae.World.UpdateWorldStateBefore(
			tx.From.Value,
			tx.GasLimit.Value,
			tx.GasPrice.Value)
		if beforeErr != nil {
			return nil, fmt.Errorf("Could not set up tx %s: %w", txIndex, beforeErr)
		}

		if tx.ESDTValue.Value.Sign() > 0 {
			ae.World.StartTransferESDT(
				tx.From.Value,
				tx.To.Value,
				string(tx.ESDTTokenName.Value),
				tx.ESDTValue.Value)
		}
	}

	// we also use fake vm outputs for transactions that don't use the VM, just for convenience
	var output *vmi.VMOutput

	if !ae.senderHasEnoughBalance(tx) {
		// out of funds is handled by the protocol, so it needs to be mocked here
		output = outOfFundsResult()
	} else {
		switch tx.Type {
		case mj.ScDeploy:
			var err error
			output, err = ae.scCreate(txIndex, tx)
			if err != nil {
				return nil, err
			}
		case mj.ScCall:
			var err error
			output, err = ae.scCall(txIndex, tx)
			if err != nil {
				return nil, err
			}
		case mj.Transfer:
			var err error
			output, err = ae.simpleTransferOutput(tx)
			if err != nil {
				return nil, err
			}
		case mj.ValidatorReward:
			var err error
			output, err = ae.validatorRewardOutput(tx)
			if err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("unknown transaction type")
		}

	}

	if output.ReturnCode == vmi.Ok {
		err := ae.updateStateAfterTx(tx, output)
		if err != nil {
			return nil, err
		}
	} else {
		ae.World.RollbackChanges()
	}

	return output, nil
}

func (ae *ArwenTestExecutor) senderHasEnoughBalance(tx *mj.Transaction) bool {
	if !tx.Type.HasSender() {
		return true
	}
	sender := ae.World.AcctMap.GetAccount(tx.From.Value)
	return sender.Balance.Cmp(tx.Value.Value) >= 0
}

func (ae *ArwenTestExecutor) simpleTransferOutput(tx *mj.Transaction) (*vmi.VMOutput, error) {
	outputAccounts := make(map[string]*vmcommon.OutputAccount)
	outputAccounts[string(tx.To.Value)] = &vmcommon.OutputAccount{
		Address:      tx.To.Value,
		BalanceDelta: tx.Value.Value,
	}

	return &vmcommon.VMOutput{
		ReturnData:      make([][]byte, 0),
		ReturnCode:      vmcommon.Ok,
		ReturnMessage:   "",
		GasRemaining:    0,
		GasRefund:       big.NewInt(0),
		OutputAccounts:  outputAccounts,
		DeletedAccounts: make([][]byte, 0),
		TouchedAccounts: make([][]byte, 0),
		Logs:            make([]*vmcommon.LogEntry, 0),
	}, nil
}

func (ae *ArwenTestExecutor) validatorRewardOutput(tx *mj.Transaction) (*vmi.VMOutput, error) {
	reward := tx.Value.Value
	recipient := ae.World.AcctMap.GetAccount(tx.To.Value)
	if recipient == nil {
		return nil, fmt.Errorf("Tx recipient (address: %s) does not exist", hex.EncodeToString(tx.To.Value))
	}
	recipient.BalanceDelta = reward
	storageElrondReward := recipient.StorageValue(ElrondRewardKey)
	storageElrondReward = big.NewInt(0).Add(
		big.NewInt(0).SetBytes(storageElrondReward),
		reward).Bytes()

	outputAccounts := make(map[string]*vmcommon.OutputAccount)
	outputAccounts[string(tx.To.Value)] = &vmcommon.OutputAccount{
		Address:      tx.To.Value,
		BalanceDelta: tx.Value.Value,
		StorageUpdates: map[string]*vmi.StorageUpdate{
			ElrondRewardKey: &vmi.StorageUpdate{
				Offset: []byte(ElrondRewardKey),
				Data:   storageElrondReward,
			},
		},
	}

	return &vmcommon.VMOutput{
		ReturnData:      make([][]byte, 0),
		ReturnCode:      vmcommon.Ok,
		ReturnMessage:   "",
		GasRemaining:    0,
		GasRefund:       big.NewInt(0),
		OutputAccounts:  outputAccounts,
		DeletedAccounts: make([][]byte, 0),
		TouchedAccounts: make([][]byte, 0),
		Logs:            make([]*vmcommon.LogEntry, 0),
	}, nil
}

func outOfFundsResult() *vmi.VMOutput {
	return &vmcommon.VMOutput{
		ReturnData:      make([][]byte, 0),
		ReturnCode:      vmcommon.OutOfFunds,
		ReturnMessage:   "",
		GasRemaining:    0,
		GasRefund:       big.NewInt(0),
		OutputAccounts:  make(map[string]*vmcommon.OutputAccount),
		DeletedAccounts: make([][]byte, 0),
		TouchedAccounts: make([][]byte, 0),
		Logs:            make([]*vmcommon.LogEntry, 0),
	}
}

func (ae *ArwenTestExecutor) scCreate(txIndex string, tx *mj.Transaction) (*vmi.VMOutput, error) {
	txHash := generateTxHash(txIndex)
	input := &vmi.ContractCreateInput{
		ContractCode: tx.Code.Value,
		VMInput: vmi.VMInput{
			CallerAddr:     tx.From.Value,
			Arguments:      mj.JSONBytesFromTreeValues(tx.Arguments),
			CallValue:      tx.Value.Value,
			GasPrice:       tx.GasPrice.Value,
			GasProvided:    tx.GasLimit.Value,
			OriginalTxHash: txHash,
			CurrentTxHash:  txHash,
			ESDTValue:      tx.ESDTValue.Value,
			ESDTTokenName:  tx.ESDTTokenName.Value,
		},
	}

	return ae.vm.RunSmartContractCreate(input)
}

func (ae *ArwenTestExecutor) scCall(txIndex string, tx *mj.Transaction) (*vmi.VMOutput, error) {
	recipient := ae.World.AcctMap.GetAccount(tx.To.Value)
	if recipient == nil {
		return nil, fmt.Errorf("Tx recipient (address: %s) does not exist", hex.EncodeToString(tx.To.Value))
	}
	if len(recipient.Code) == 0 {
		return nil, fmt.Errorf("Tx recipient (address: %s) is not a smart contract", hex.EncodeToString(tx.To.Value))
	}
	txHash := generateTxHash(txIndex)
	input := &vmi.ContractCallInput{
		RecipientAddr: tx.To.Value,
		Function:      tx.Function,
		VMInput: vmi.VMInput{
			CallerAddr:     tx.From.Value,
			Arguments:      mj.JSONBytesFromTreeValues(tx.Arguments),
			CallValue:      tx.Value.Value,
			GasPrice:       tx.GasPrice.Value,
			GasProvided:    tx.GasLimit.Value,
			OriginalTxHash: txHash,
			CurrentTxHash:  txHash,
			ESDTValue:      tx.ESDTValue.Value,
			ESDTTokenName:  tx.ESDTTokenName.Value,
		},
	}

	return ae.vm.RunSmartContractCall(input)
}

func (ae *ArwenTestExecutor) updateStateAfterTx(
	tx *mj.Transaction,
	output *vmi.VMOutput) error {

	// subtract call value from sender (this is not reflected in the delta)
	// except for validatorReward, there is no sender there
	if tx.Type.HasSender() {
		_ = ae.World.UpdateBalanceWithDelta(tx.From.Value, big.NewInt(0).Neg(tx.Value.Value))
	}

	// update accounts based on deltas
	updErr := ae.World.UpdateAccounts(output.OutputAccounts, output.DeletedAccounts)
	if updErr != nil {
		return updErr
	}

	// sum of all balance deltas should equal call value (unless we got an error)
	// (unless it is validatorReward, when funds just pop into existence)
	if tx.Type.HasSender() {
		sumOfBalanceDeltas := big.NewInt(0)
		for _, oa := range output.OutputAccounts {
			sumOfBalanceDeltas = sumOfBalanceDeltas.Add(sumOfBalanceDeltas, oa.BalanceDelta)
		}
		if sumOfBalanceDeltas.Cmp(tx.Value.Value) != 0 {
			return fmt.Errorf("sum of balance deltas should equal call value. Sum of balance deltas: %d (0x%x). Call value: %d (0x%x)",
				sumOfBalanceDeltas, sumOfBalanceDeltas, tx.Value.Value, tx.Value.Value)
		}
	}

	return nil
}
