package host

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ElrondNetwork/arwen-wasm-vm/arwen"
	"github.com/ElrondNetwork/elrond-go/core"
	"github.com/ElrondNetwork/elrond-go/core/vmcommon"
	"github.com/stretchr/testify/require"
)

var ESDTTransferGasCost = uint64(1)
var ESDTTestTokenName = []byte("TT")

func TestExecution_ExecuteOnSameContext_BuiltinFunctions(t *testing.T) {
	// TODO rewrite to use ExecuteOnDestContext(). It is disallowed to call a
	// built-in function with ExecuteOnSameContext().
	t.Skip()
	code := GetTestSCCode("exec-same-ctx-builtin", "../../")
	scBalance := big.NewInt(1000)

	host, stubBlockchainHook := defaultTestArwenForCall(t, code, scBalance)
	stubBlockchainHook.ProcessBuiltInFunctionCalled = dummyProcessBuiltInFunction
	host.protocolBuiltinFunctions = getDummyBuiltinFunctionNames()

	input := DefaultTestContractCallInput()
	input.RecipientAddr = parentAddress

	// Run function testBuiltins1
	input.Function = "testBuiltins1"
	input.GasProvided = 100000

	vmOutput, err := host.RunSmartContractCall(input)
	require.Nil(t, err)

	require.NotNil(t, vmOutput)
	expectedVMOutput := expectedVMOutputSameCtxBuiltinFunctions1(code)
	require.Equal(t, expectedVMOutput, vmOutput)

	// Run function testBuiltins2
	input.Function = "testBuiltins2"
	input.GasProvided = 100000

	vmOutput, err = host.RunSmartContractCall(input)
	require.Nil(t, err)

	require.NotNil(t, vmOutput)
	expectedVMOutput = expectedVMOutputSameCtxBuiltinFunctions2(code)
	require.Equal(t, expectedVMOutput, vmOutput)

	// Run function testBuiltins3
	input.Function = "testBuiltins3"
	input.GasProvided = 100000

	vmOutput, err = host.RunSmartContractCall(input)
	require.Nil(t, err)

	require.NotNil(t, vmOutput)
	expectedVMOutput = expectedVMOutputSameCtxBuiltinFunctions3(code)
	require.Equal(t, expectedVMOutput, vmOutput)
}

func TestExecution_AsyncCall_BuiltinFails(t *testing.T) {
	code := GetTestSCCode("async-call-builtin", "../../")
	scBalance := big.NewInt(1000)

	host, stubBlockchainHook := defaultTestArwenForCall(t, code, scBalance)
	stubBlockchainHook.ProcessBuiltInFunctionCalled = dummyProcessBuiltInFunction
	host.protocolBuiltinFunctions = getDummyBuiltinFunctionNames()

	input := DefaultTestContractCallInput()
	input.RecipientAddr = parentAddress
	input.Function = "performAsyncCallToBuiltin"
	input.Arguments = [][]byte{{1}}
	input.GasProvided = 1000000

	vmOutput, err := host.RunSmartContractCall(input)
	require.Nil(t, err)

	require.NotNil(t, vmOutput)
	require.Equal(t, vmcommon.Ok, vmOutput.ReturnCode)
	require.Equal(t, [][]byte{[]byte("hello"), {4}}, vmOutput.ReturnData)
}

func TestESDT_GettersAPI(t *testing.T) {
	code := GetTestSCCode("exchange", "../../")
	scBalance := big.NewInt(1000)

	host, _ := defaultTestArwenForCall(t, code, scBalance)

	input := DefaultTestContractCallInput()
	input.RecipientAddr = parentAddress
	input.Function = "validateGetters"
	input.GasProvided = 1000000
	input.ESDTValue = big.NewInt(5)
	input.ESDTTokenName = ESDTTestTokenName

	vmOutput, err := host.RunSmartContractCall(input)
	require.Nil(t, err)

	require.NotNil(t, vmOutput)
	require.Equal(t, vmcommon.Ok, vmOutput.ReturnCode)
}

func TestESDT_GettersAPI_ExecuteAfterBuiltinCall(t *testing.T) {
	dummyCode := GetTestSCCode("init-simple", "../../")
	exchangeCode := GetTestSCCode("exchange", "../../")
	scBalance := big.NewInt(1000)
	esdtValue := int64(5)

	host, stubBlockchainHook := defaultTestArwenForCall(t, exchangeCode, scBalance)
	stubBlockchainHook.ProcessBuiltInFunctionCalled = dummyProcessBuiltInFunction
	host.protocolBuiltinFunctions = getDummyBuiltinFunctionNames()

	input := DefaultTestContractCallInput()
	err := host.Output().TransferValueOnly(input.RecipientAddr, input.CallerAddr, input.CallValue)
	require.Nil(t, err)

	input.RecipientAddr = parentAddress
	input.Function = core.BuiltInFunctionESDTTransfer
	input.GasProvided = 1000000
	input.Arguments = [][]byte{
		ESDTTestTokenName,
		big.NewInt(esdtValue).Bytes(),
		[]byte("validateGetters"),
	}

	host.InitState()

	_ = host.Runtime().StartWasmerInstance(dummyCode, input.GasProvided, true)
	vmOutput, asyncInfo, _, err := host.ExecuteOnDestContext(input)

	require.Nil(t, err)
	require.NotNil(t, vmOutput)

	require.Zero(t, len(asyncInfo.AsyncContextMap))

	require.Equal(t, vmcommon.Ok, vmOutput.ReturnCode)
	host.Clean()
}

func dummyProcessBuiltInFunction(input *vmcommon.ContractCallInput) (*vmcommon.VMOutput, error) {
	outPutAccounts := make(map[string]*vmcommon.OutputAccount)
	outPutAccounts[string(parentAddress)] = &vmcommon.OutputAccount{
		BalanceDelta: big.NewInt(0),
		Address:      parentAddress}

	if input.Function == "builtinClaim" {
		outPutAccounts[string(parentAddress)].BalanceDelta = big.NewInt(42)
		return &vmcommon.VMOutput{
			GasRemaining:   400 + input.GasLocked,
			OutputAccounts: outPutAccounts,
		}, nil
	}
	if input.Function == "builtinDoSomething" {
		return &vmcommon.VMOutput{
			GasRemaining:   400 + input.GasLocked,
			OutputAccounts: outPutAccounts,
		}, nil
	}
	if input.Function == "builtinFail" {
		return &vmcommon.VMOutput{
			GasRemaining:  0 + input.GasLocked,
			GasRefund:     big.NewInt(0),
			ReturnCode:    vmcommon.UserError,
			ReturnMessage: "whatdidyoudo",
		}, nil
	}
	if input.Function == core.BuiltInFunctionESDTTransfer {
		vmOutput := &vmcommon.VMOutput{
			GasRemaining: 0,
		}
		function := string(input.Arguments[2])
		esdtTransferTxData := function
		for _, arg := range input.Arguments[3:] {
			esdtTransferTxData += "@" + hex.EncodeToString(arg)
		}
		outTransfer := vmcommon.OutputTransfer{
			Value:    big.NewInt(0),
			GasLimit: input.GasProvided - ESDTTransferGasCost + input.GasLocked,
			Data:     []byte(esdtTransferTxData),
			CallType: vmcommon.AsynchronousCall,
		}
		vmOutput.OutputAccounts = make(map[string]*vmcommon.OutputAccount)
		vmOutput.OutputAccounts[string(input.RecipientAddr)] = &vmcommon.OutputAccount{
			Address:         input.RecipientAddr,
			OutputTransfers: []vmcommon.OutputTransfer{outTransfer},
		}
		// TODO when ESDT token balance querying is implemented, ensure the
		// transfers that happen here are persisted in the mock accounts
		return vmOutput, nil
	}

	return nil, arwen.ErrFuncNotFound
}

func getDummyBuiltinFunctionNames() vmcommon.FunctionNames {
	names := make(vmcommon.FunctionNames)

	var empty struct{}
	names["builtinClaim"] = empty
	names["builtinDoSomething"] = empty
	names["builtinFail"] = empty
	names[core.BuiltInFunctionESDTTransfer] = empty

	return names
}
