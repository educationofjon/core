package consensus

import (
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.sia.tech/core/v2/merkle"
	"go.sia.tech/core/v2/types"

	"lukechampine.com/frand"
)

var (
	maxCurrency       = types.NewCurrency(math.MaxUint64, math.MaxUint64)
	testingDifficulty = types.Work{NumHashes: [32]byte{30: 1}}
)

func testingKeypair(seed uint64) (types.PublicKey, types.PrivateKey) {
	var b [32]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	privkey := types.NewPrivateKeyFromSeed(b[:])
	return privkey.PublicKey(), privkey
}

func genesisWithSiacoinOutputs(scos ...types.SiacoinOutput) types.Block {
	return types.Block{
		Header:       types.BlockHeader{Timestamp: time.Unix(734600000, 0)},
		Transactions: []types.Transaction{{SiacoinOutputs: scos}},
	}
}

func signAllInputs(txn *types.Transaction, s State, priv types.PrivateKey) {
	sigHash := s.InputSigHash(*txn)
	for i := range txn.SiacoinInputs {
		txn.SiacoinInputs[i].Signatures = []types.Signature{priv.SignHash(sigHash)}
	}
	for i := range txn.SiafundInputs {
		txn.SiafundInputs[i].Signatures = []types.Signature{priv.SignHash(sigHash)}
	}
}

func TestBlockRewardValue(t *testing.T) {
	reward := func(height uint64) types.Currency {
		return (&State{Index: types.ChainIndex{Height: height - 1}}).BlockReward()
	}

	tests := []struct {
		height uint64
		exp    types.Currency
	}{
		{0, types.Siacoins(300000)},
		{1, types.Siacoins(299999)},
		{100000, types.Siacoins(200000)},
		{269999, types.Siacoins(30001)},
		{270000, types.Siacoins(30000)},
		{270001, types.Siacoins(30000)},
		{1e6, types.Siacoins(30000)},
	}
	for _, test := range tests {
		if got := reward(test.height); got != test.exp {
			t.Errorf("expected %v, got %v", test.exp, got)
		}
	}
}

func TestEphemeralOutputs(t *testing.T) {
	pubkey, privkey := testingKeypair(0)
	sau := GenesisUpdate(genesisWithSiacoinOutputs(types.SiacoinOutput{
		Address: types.StandardAddress(pubkey),
		Value:   types.Siacoins(1),
	}), testingDifficulty)

	// create an ephemeral output
	parentTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      sau.NewSiacoinElements[1],
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Address: types.StandardAddress(pubkey),
			Value:   types.Siacoins(1),
		}},
	}
	signAllInputs(&parentTxn, sau.State, privkey)
	ephemeralOutput := types.SiacoinElement{
		StateElement: types.StateElement{
			ID: types.ElementID{
				Source: types.Hash256(parentTxn.ID()),
				Index:  0,
			},
			LeafIndex: types.EphemeralLeafIndex,
		},
		SiacoinOutput: types.SiacoinOutput{
			Value:   parentTxn.SiacoinOutputs[0].Value,
			Address: types.StandardAddress(pubkey),
		},
	}

	// create a transaction that spends the ephemeral output
	childTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      ephemeralOutput,
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Address: types.StandardAddress(pubkey),
			Value:   ephemeralOutput.Value,
		}},
	}
	signAllInputs(&childTxn, sau.State, privkey)

	// the transaction set should be valid
	if err := sau.State.ValidateTransactionSet([]types.Transaction{parentTxn, childTxn}); err != nil {
		t.Fatal(err)
	}

	// change the value of the output and attempt to spend it
	mintTxn := childTxn.DeepCopy()
	mintTxn.SiacoinInputs[0].Parent.Value = types.Siacoins(1e6)
	mintTxn.SiacoinOutputs[0].Value = mintTxn.SiacoinInputs[0].Parent.Value
	signAllInputs(&mintTxn, sau.State, privkey)

	if err := sau.State.ValidateTransactionSet([]types.Transaction{parentTxn, mintTxn}); err == nil {
		t.Fatal("ephemeral output with wrong value should be rejected")
	}

	// add another transaction to the set that double-spends the output
	doubleSpendTxn := childTxn.DeepCopy()
	doubleSpendTxn.SiacoinOutputs[0].Address = types.VoidAddress
	signAllInputs(&doubleSpendTxn, sau.State, privkey)

	if err := sau.State.ValidateTransactionSet([]types.Transaction{parentTxn, childTxn, doubleSpendTxn}); err == nil {
		t.Fatal("ephemeral output double-spend not rejected")
	}

	invalidTxn := childTxn.DeepCopy()
	invalidTxn.SiacoinInputs[0].Parent.Address = types.VoidAddress
	signAllInputs(&invalidTxn, sau.State, privkey)

	if err := sau.State.ValidateTransactionSet([]types.Transaction{parentTxn, invalidTxn}); err == nil {
		t.Fatal("transaction claims wrong address for ephemeral output")
	}
}

func TestValidateTransaction(t *testing.T) {
	// This test constructs a complex transaction and then corrupts it in
	// various ways to produce validation errors. Since the transaction is so
	// complex, we need to perform quite a bit of setup to create the necessary
	// outputs and accumulator state.

	// create genesis block with multiple outputs and file contracts
	pubkey, privkey := testingKeypair(0)
	renterPubkey, renterPrivkey := testingKeypair(1)
	hostPubkey, hostPrivkey := testingKeypair(2)
	data := frand.Bytes(64 * 2)
	dataRoot := merkle.NodeHash(
		merkle.StorageProofLeafHash(data[:64]),
		merkle.StorageProofLeafHash(data[64:]),
	)
	genesisBlock := types.Block{
		Header: types.BlockHeader{Timestamp: time.Unix(734600000, 0)},
		Transactions: []types.Transaction{{
			SiacoinOutputs: []types.SiacoinOutput{
				{
					Address: types.StandardAddress(pubkey),
					Value:   types.Siacoins(11),
				},
				{
					Address: types.StandardAddress(pubkey),
					Value:   types.Siacoins(11),
				},
				{
					Address: types.StandardAddress(pubkey),
					Value:   maxCurrency,
				},
			},
			SiafundOutputs: []types.SiafundOutput{
				{
					Address: types.StandardAddress(pubkey),
					Value:   100,
				},
				{
					Address: types.StandardAddress(pubkey),
					Value:   100,
				},
				{
					Address: types.StandardAddress(pubkey),
					Value:   math.MaxUint64,
				},
			},
			FileContracts: []types.FileContract{
				// unresolved open contract
				{
					WindowStart: 5,
					WindowEnd:   10,
					RenterOutput: types.SiacoinOutput{
						Address: types.StandardAddress(renterPubkey),
						Value:   types.Siacoins(58),
					},
					HostOutput: types.SiacoinOutput{
						Address: types.StandardAddress(renterPubkey),
						Value:   types.Siacoins(19),
					},
					TotalCollateral: types.ZeroCurrency,
					RenterPublicKey: renterPubkey,
					HostPublicKey:   hostPubkey,
				},
				// unresolved closed contract
				{
					WindowStart:     0,
					WindowEnd:       10,
					Filesize:        uint64(len(data)),
					FileMerkleRoot:  dataRoot,
					RenterPublicKey: renterPubkey,
					HostPublicKey:   hostPubkey,
				},
				// resolved-valid contract
				{
					WindowStart:     0,
					WindowEnd:       10,
					Filesize:        uint64(len(data)),
					FileMerkleRoot:  dataRoot,
					RenterPublicKey: renterPubkey,
					HostPublicKey:   hostPubkey,
				},
				// resolved-missed contract
				{
					WindowStart:     0,
					WindowEnd:       0,
					RenterPublicKey: renterPubkey,
					HostPublicKey:   hostPubkey,
				},
			},
		}},
	}
	sau := GenesisUpdate(genesisBlock, testingDifficulty)
	spentSC := sau.NewSiacoinElements[1]
	unspentSC := sau.NewSiacoinElements[2]
	overflowSC := sau.NewSiacoinElements[3]
	spentSF := sau.NewSiafundElements[0]
	unspentSF := sau.NewSiafundElements[1]
	overflowSF := sau.NewSiafundElements[2]
	openContract := sau.NewFileContracts[0]
	closedContract := sau.NewFileContracts[1]
	resolvedValidContract := sau.NewFileContracts[2]
	resolvedMissedContract := sau.NewFileContracts[3]
	closedProof := types.StorageProof{
		WindowStart: sau.State.Index,
		WindowProof: sau.HistoryProof(),
	}
	proofIndex := sau.State.StorageProofLeafIndex(closedContract.Filesize, closedProof.WindowStart, closedContract.ID)
	copy(closedProof.Leaf[:], data[64*proofIndex:])
	if proofIndex == 0 {
		closedProof.Proof = append(closedProof.Proof, merkle.StorageProofLeafHash(data[64:]))
	} else {
		closedProof.Proof = append(closedProof.Proof, merkle.StorageProofLeafHash(data[:64]))
	}
	resolvedValidProof := types.StorageProof{
		WindowStart: sau.State.Index,
		WindowProof: sau.HistoryProof(),
	}
	proofIndex = sau.State.StorageProofLeafIndex(resolvedValidContract.Filesize, resolvedValidProof.WindowStart, resolvedValidContract.ID)
	copy(resolvedValidProof.Leaf[:], data[64*proofIndex:])
	if proofIndex == 0 {
		resolvedValidProof.Proof = append(resolvedValidProof.Proof, merkle.StorageProofLeafHash(data[64:]))
	} else {
		resolvedValidProof.Proof = append(resolvedValidProof.Proof, merkle.StorageProofLeafHash(data[:64]))
	}

	// mine a block so that resolvedMissedContract's proof window expires, then
	// construct a setup transaction that spends some of the outputs and
	// resolves some of the contracts
	b := mineBlock(sau.State, genesisBlock)
	if err := sau.State.ValidateBlock(b); err != nil {
		t.Fatal(err)
	}
	sau = ApplyBlock(sau.State, b)
	sau.UpdateElementProof(&spentSC.StateElement)
	sau.UpdateElementProof(&unspentSC.StateElement)
	sau.UpdateElementProof(&spentSF.StateElement)
	sau.UpdateElementProof(&unspentSF.StateElement)
	sau.UpdateElementProof(&openContract.StateElement)
	sau.UpdateElementProof(&closedContract.StateElement)
	sau.UpdateElementProof(&resolvedValidContract.StateElement)
	sau.UpdateElementProof(&resolvedMissedContract.StateElement)
	sau.UpdateWindowProof(&closedProof)
	sau.UpdateWindowProof(&resolvedValidProof)
	resolveTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      spentSC,
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiafundInputs: []types.SiafundInput{{
			Parent:      spentSF,
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Address: types.VoidAddress,
			Value:   spentSC.Value,
		}},
		SiafundOutputs: []types.SiafundOutput{{
			Address: types.VoidAddress,
			Value:   spentSF.Value,
		}},
		FileContractResolutions: []types.FileContractResolution{
			{
				Parent: resolvedMissedContract,
			},
			{
				Parent:       resolvedValidContract,
				StorageProof: resolvedValidProof,
			},
		},
	}
	signAllInputs(&resolveTxn, sau.State, privkey)
	b = mineBlock(sau.State, b, resolveTxn)
	if err := sau.State.ValidateBlock(b); err != nil {
		t.Fatal(err)
	}
	sau = ApplyBlock(sau.State, b)
	sau.UpdateElementProof(&spentSC.StateElement)
	sau.UpdateElementProof(&unspentSC.StateElement)
	sau.UpdateElementProof(&spentSF.StateElement)
	sau.UpdateElementProof(&unspentSF.StateElement)
	sau.UpdateElementProof(&openContract.StateElement)
	sau.UpdateElementProof(&closedContract.StateElement)
	sau.UpdateElementProof(&resolvedValidContract.StateElement)
	sau.UpdateElementProof(&resolvedMissedContract.StateElement)
	sau.UpdateWindowProof(&closedProof)
	s := sau.State

	// finally, create the valid transaction, which spends the remaining outputs
	// and revises/resolves the remaining contracts
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      unspentSC,
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Address: types.VoidAddress,
			Value:   types.Siacoins(1),
		}},
		SiafundInputs: []types.SiafundInput{{
			Parent:      unspentSF,
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiafundOutputs: []types.SiafundOutput{{
			Address: types.VoidAddress,
			Value:   unspentSF.Value,
		}},
		FileContracts: []types.FileContract{{
			WindowStart:     100,
			WindowEnd:       105,
			RenterOutput:    types.SiacoinOutput{Value: types.Siacoins(1)},
			HostOutput:      types.SiacoinOutput{Value: types.Siacoins(4)},
			TotalCollateral: types.Siacoins(1),
			RenterPublicKey: renterPubkey,
			HostPublicKey:   hostPubkey,
		}},
		FileContractRevisions: []types.FileContractRevision{{
			Parent: openContract,
			Revision: types.FileContract{
				WindowStart:     200,
				WindowEnd:       205,
				RenterOutput:    types.SiacoinOutput{Value: types.Siacoins(77)},
				HostOutput:      types.SiacoinOutput{Value: types.Siacoins(0)},
				TotalCollateral: types.ZeroCurrency,
				RevisionNumber:  1,
			},
		}},
		FileContractResolutions: []types.FileContractResolution{{
			Parent:       closedContract,
			StorageProof: closedProof,
		}},
		Attestations: []types.Attestation{{
			PublicKey: pubkey,
			Key:       "foo",
			Value:     []byte("bar"),
		}},
		MinerFee: types.Siacoins(48).Div64(10),
	}
	fc := &txn.FileContracts[0]
	contractHash := s.ContractSigHash(*fc)
	fc.RenterSignature = renterPrivkey.SignHash(contractHash)
	fc.HostSignature = hostPrivkey.SignHash(contractHash)
	rev := &txn.FileContractRevisions[0]
	contractHash = s.ContractSigHash(rev.Revision)
	rev.Revision.RenterSignature = renterPrivkey.SignHash(contractHash)
	rev.Revision.HostSignature = hostPrivkey.SignHash(contractHash)
	txn.Attestations[0].Signature = privkey.SignHash(s.AttestationSigHash(txn.Attestations[0]))
	signAllInputs(&txn, s, privkey)

	if err := s.ValidateTransaction(txn); err != nil {
		t.Fatal(err)
	}

	// corrupt the transaction in various ways to trigger validation errors
	tests := []struct {
		desc    string
		corrupt func(*types.Transaction)
	}{
		{
			"zero-valued SiacoinOutput",
			func(txn *types.Transaction) {
				txn.SiacoinOutputs[0].Value = types.ZeroCurrency
			},
		},
		{
			"zero-valued SiafundOutput",
			func(txn *types.Transaction) {
				txn.SiafundOutputs[0].Value = 0
			},
		},
		{
			"siacoin input address does not match spend policy",
			func(txn *types.Transaction) {
				txn.SiacoinInputs[0].SpendPolicy = types.AnyoneCanSpend()
			},
		},
		{
			"siafund input address does not match spend policy",
			func(txn *types.Transaction) {
				txn.SiafundInputs[0].SpendPolicy = types.AnyoneCanSpend()
			},
		},
		{
			"siacoin outputs that do not equal inputs",
			func(txn *types.Transaction) {
				txn.SiacoinOutputs[0].Value = txn.SiacoinOutputs[0].Value.Div64(2)
			},
		},
		{
			"siacoin inputs that overflow",
			func(txn *types.Transaction) {
				txn.SiacoinInputs = append(txn.SiacoinInputs, types.SiacoinInput{
					Parent:      overflowSC,
					SpendPolicy: types.PolicyPublicKey(pubkey),
				})
				signAllInputs(txn, s, privkey)
			},
		},
		{
			"siacoin outputs that overflow",
			func(txn *types.Transaction) {
				txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
					Value: maxCurrency,
				})
			},
		},
		{
			"siafund outputs that do not equal inputs",
			func(txn *types.Transaction) {
				txn.SiafundOutputs[0].Value /= 2
			},
		},
		{
			"siafund inputs that overflow",
			func(txn *types.Transaction) {
				txn.SiafundInputs = append(txn.SiafundInputs, types.SiafundInput{
					Parent:      overflowSF,
					SpendPolicy: types.PolicyPublicKey(pubkey),
				})
				signAllInputs(txn, s, privkey)
			},
		},
		{
			"siafund outputs that overflow",
			func(txn *types.Transaction) {
				txn.SiafundOutputs = append(txn.SiafundOutputs, types.SiafundOutput{
					Value: math.MaxUint64,
				})
			},
		},
		{
			"file contract renter output overflows",
			func(txn *types.Transaction) {
				txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
					Value: maxCurrency.Sub(types.Siacoins(2)),
				})
				txn.FileContracts[0].RenterOutput.Value = types.Siacoins(2)
			},
		},
		{
			"file contract host output overflows",
			func(txn *types.Transaction) {
				txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
					Value: maxCurrency.Sub(types.Siacoins(2)),
				})
				txn.FileContracts[0].RenterOutput.Value = types.ZeroCurrency
				txn.FileContracts[0].HostOutput.Value = types.Siacoins(2)
			},
		},
		{
			"file contract collateral overflows",
			func(txn *types.Transaction) {
				txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
					Value: maxCurrency.Sub(types.Siacoins(2)),
				})
				txn.FileContracts[0].RenterOutput.Value = types.ZeroCurrency
				txn.FileContracts[0].HostOutput.Value = types.ZeroCurrency
				txn.FileContracts[0].TotalCollateral = types.Siacoins(2)
			},
		},
		{
			"file contract tax overflows",
			func(txn *types.Transaction) {
				txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
					Value: maxCurrency.Sub(types.Siacoins(2)),
				})
				txn.FileContracts[0].RenterOutput.Value = types.Siacoins(1)
				txn.FileContracts[0].HostOutput.Value = types.ZeroCurrency
			},
		},
		{
			"miner fee that overflows",
			func(txn *types.Transaction) {
				txn.MinerFee = maxCurrency
			},
		},
		{
			"non-existent siacoin output",
			func(txn *types.Transaction) {
				txn.SiacoinInputs[0].Parent.ID = types.ElementID{}
			},
		},
		{
			"double-spent siacoin output",
			func(txn *types.Transaction) {
				txn.SiacoinInputs[0].Parent = spentSC
			},
		},
		{
			"invalid siacoin signature",
			func(txn *types.Transaction) {
				txn.SiacoinInputs[0].Signatures[0][0] ^= 1
			},
		},
		{
			"non-existent siafund output",
			func(txn *types.Transaction) {
				txn.SiafundInputs[0].Parent.ID = types.ElementID{}
			},
		},
		{
			"double-spent siafund output",
			func(txn *types.Transaction) {
				txn.SiafundInputs[0].Parent = spentSF
			},
		},
		{
			"invalid siafund signature",
			func(txn *types.Transaction) {
				txn.SiafundInputs[0].Signatures[0][0] ^= 1
			},
		},
		{
			"file contract that has invalid renter signature",
			func(txn *types.Transaction) {
				txn.FileContracts[0].RenterSignature[0] ^= 1
			},
		},
		{
			"file contract that has invalid host signature",
			func(txn *types.Transaction) {
				txn.FileContracts[0].HostSignature[0] ^= 1
			},
		},
		{
			"file contract whose window ends before it begins",
			func(txn *types.Transaction) {
				txn.FileContracts[0].WindowEnd = txn.FileContracts[0].WindowStart - 1
			},
		},
		{
			"revision of non-existent file contract",
			func(txn *types.Transaction) {
				txn.FileContractRevisions[0].Parent.ID = types.ElementID{}
			},
		},
		{
			"revision of already-resolved-valid file contract",
			func(txn *types.Transaction) {
				txn.FileContractRevisions[0].Parent = resolvedValidContract
			},
		},
		{
			"revision of already-resolved-missed file contract",
			func(txn *types.Transaction) {
				txn.FileContractRevisions[0].Parent = resolvedMissedContract
			},
		},
		{
			"file contract revision that does not increase revision number",
			func(txn *types.Transaction) {
				rev := &txn.FileContractRevisions[0].Revision
				rev.RevisionNumber = 0
			},
		},
		{
			"file contract revision that modifies output sum",
			func(txn *types.Transaction) {
				rev := &txn.FileContractRevisions[0].Revision
				rev.RenterOutput.Value = rev.RenterOutput.Value.Mul64(2)
			},
		},
		{
			"file contract revision whose window ends before it begins",
			func(txn *types.Transaction) {
				rev := &txn.FileContractRevisions[0].Revision
				rev.WindowEnd = rev.WindowStart - 1
			},
		},
		{
			"file contract revision that has invalid renter signature",
			func(txn *types.Transaction) {
				rev := &txn.FileContractRevisions[0].Revision
				rev.RenterSignature[0] ^= 1
			},
		},
		{
			"file contract revision that has invalid host signature",
			func(txn *types.Transaction) {
				rev := &txn.FileContractRevisions[0].Revision
				rev.HostSignature[0] ^= 1
			},
		},
		{
			"resolution of non-existent file contract",
			func(txn *types.Transaction) {
				txn.FileContractResolutions[0].Parent.ID = types.ElementID{}
			},
		},
		{
			"resolution with invalid history proof",
			func(txn *types.Transaction) {
				txn.FileContractResolutions[0].StorageProof.WindowProof = nil
			},
		},
		{
			"resolution of already-resolved-valid file contract",
			func(txn *types.Transaction) {
				txn.FileContractResolutions[0].Parent = resolvedValidContract
			},
		},
		{
			"resolution of already-resolved-missed file contract",
			func(txn *types.Transaction) {
				txn.FileContractResolutions[0].Parent = resolvedMissedContract
			},
		},
		{
			"file contract resolution whose WindowStart does not match final revision",
			func(txn *types.Transaction) {
				res := &txn.FileContractResolutions[0]
				res.StorageProof.WindowStart = b.Index()
				res.StorageProof.WindowProof = nil
			},
		},
		{
			"file contract resolution whose storage proof root does not match final Merkle root",
			func(txn *types.Transaction) {
				res := &txn.FileContractResolutions[0]
				res.StorageProof.Proof[0][0] ^= 1
			},
		},
		{
			"attestation with invalid signature",
			func(txn *types.Transaction) {
				txn.Attestations[0].Signature[0] ^= 1
			},
		},
		{
			"invalid Foundation update",
			func(txn *types.Transaction) {
				txn.NewFoundationAddress = types.StandardAddress(pubkey)
			},
		},
	}
	for _, test := range tests {
		corruptTxn := txn.DeepCopy()
		test.corrupt(&corruptTxn)
		if err := s.ValidateTransaction(corruptTxn); err == nil {
			t.Fatalf("accepted transaction with %v", test.desc)
		}
	}
}

func TestValidateSpendPolicy(t *testing.T) {
	// create a State with a height above 0
	s := State{
		Index: types.ChainIndex{Height: 100},
	}

	privkey := func(seed uint64) types.PrivateKey {
		_, privkey := testingKeypair(seed)
		return privkey
	}
	pubkey := func(seed uint64) types.PublicKey {
		pubkey, _ := testingKeypair(seed)
		return pubkey
	}

	tests := []struct {
		desc    string
		policy  types.SpendPolicy
		sign    func(sigHash types.Hash256) []types.Signature
		wantErr bool
	}{
		{
			desc: "not enough signatures",
			policy: types.PolicyThreshold(
				2,
				[]types.SpendPolicy{
					types.PolicyPublicKey(pubkey(0)),
					types.PolicyPublicKey(pubkey(1)),
				},
			),
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{privkey(0).SignHash(sigHash)}
			},
			wantErr: true,
		},
		{
			desc:    "height not above",
			policy:  types.PolicyAbove(150),
			sign:    func(types.Hash256) []types.Signature { return nil },
			wantErr: true,
		},
		{
			desc:    "anyone can spend",
			policy:  types.AnyoneCanSpend(),
			sign:    func(types.Hash256) []types.Signature { return nil },
			wantErr: false,
		},
		{
			desc: "multiple public key signatures",
			policy: types.PolicyThreshold(
				3,
				[]types.SpendPolicy{
					types.PolicyPublicKey(pubkey(0)),
					types.PolicyPublicKey(pubkey(1)),
					types.PolicyPublicKey(pubkey(2)),
				},
			),
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{
					privkey(0).SignHash(sigHash),
					privkey(1).SignHash(sigHash),
					privkey(2).SignHash(sigHash),
				}
			},
			wantErr: false,
		},
		{
			desc: "invalid foundation failsafe",
			policy: types.PolicyThreshold(
				1,
				[]types.SpendPolicy{
					types.PolicyThreshold(
						2,
						[]types.SpendPolicy{
							types.PolicyPublicKey(pubkey(0)),
							types.PolicyPublicKey(pubkey(1)),
							types.PolicyPublicKey(pubkey(2)),
						},
					),
					// failsafe policy is not satisfied because the current height is 100
					types.PolicyThreshold(
						2,
						[]types.SpendPolicy{
							types.PolicyPublicKey(pubkey(3)),
							types.PolicyAbove(150),
						},
					),
				},
			),
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{privkey(3).SignHash(sigHash)}
			},
			wantErr: true,
		},
		{
			desc: "valid foundation primary",
			policy: types.PolicyThreshold(
				1,
				[]types.SpendPolicy{
					types.PolicyThreshold(
						2,
						[]types.SpendPolicy{
							types.PolicyPublicKey(pubkey(0)),
							types.PolicyPublicKey(pubkey(1)),
							types.PolicyPublicKey(pubkey(2)),
						},
					),
					// failsafe policy is not satisfied because the current height is 100
					types.PolicyThreshold(
						2,
						[]types.SpendPolicy{
							types.PolicyPublicKey(pubkey(3)),
							types.PolicyAbove(150),
						},
					),
				},
			),
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{
					privkey(1).SignHash(sigHash),
					privkey(2).SignHash(sigHash),
				}
			},
			wantErr: false,
		},
		{
			desc: "valid foundation failsafe",
			policy: types.PolicyThreshold(
				1,
				[]types.SpendPolicy{
					types.PolicyThreshold(
						2,
						[]types.SpendPolicy{
							types.PolicyPublicKey(pubkey(0)),
							types.PolicyPublicKey(pubkey(1)),
							types.PolicyPublicKey(pubkey(2)),
						},
					),
					// failsafe policy is satisfied because the current height is 100
					types.PolicyThreshold(
						2,
						[]types.SpendPolicy{
							types.PolicyPublicKey(pubkey(3)),
							types.PolicyAbove(80),
						},
					),
				},
			),
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{privkey(3).SignHash(sigHash)}
			},
			wantErr: false,
		},
		{
			desc: "invalid legacy unlock hash",
			policy: types.SpendPolicy{Type: types.PolicyTypeUnlockConditions{
				PublicKeys: []types.PublicKey{
					pubkey(0),
					pubkey(1),
					pubkey(2),
				},
				SignaturesRequired: 2,
			}},
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{
					privkey(0).SignHash(sigHash),
				}
			},
			wantErr: true,
		},
		{
			desc: "invalid timelocked legacy unlock conditions",
			policy: types.SpendPolicy{Type: types.PolicyTypeUnlockConditions{
				PublicKeys: []types.PublicKey{
					pubkey(0),
				},
				Timelock:           150,
				SignaturesRequired: 1,
			}},
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{
					privkey(0).SignHash(sigHash),
				}
			},
			wantErr: true,
		},
		{
			desc: "valid legacy unlock hash",
			policy: types.SpendPolicy{Type: types.PolicyTypeUnlockConditions{
				PublicKeys: []types.PublicKey{
					pubkey(0),
					pubkey(1),
					pubkey(2),
				},
				SignaturesRequired: 2,
			}},
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{
					privkey(0).SignHash(sigHash),
					privkey(1).SignHash(sigHash),
				}
			},
			wantErr: false,
		},
		{
			desc: "valid timelocked legacy unlock conditions",
			policy: types.SpendPolicy{Type: types.PolicyTypeUnlockConditions{
				PublicKeys: []types.PublicKey{
					pubkey(0),
				},
				Timelock:           80,
				SignaturesRequired: 1,
			}},
			sign: func(sigHash types.Hash256) []types.Signature {
				return []types.Signature{privkey(0).SignHash(sigHash)}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		txn := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{{
				Parent: types.SiacoinElement{
					SiacoinOutput: types.SiacoinOutput{
						Address: tt.policy.Address(),
					},
				},
				SpendPolicy: tt.policy,
			}},
		}
		sigHash := s.InputSigHash(txn)
		txn.SiacoinInputs[0].Signatures = tt.sign(sigHash)
		if err := s.validateSpendPolicies(txn); (err != nil) != tt.wantErr {
			t.Fatalf("case %q failed: %v", tt.desc, err)
		}
	}
}

func TestValidateTransactionSet(t *testing.T) {
	pubkey, privkey := testingKeypair(0)
	genesisBlock := genesisWithSiacoinOutputs(types.SiacoinOutput{
		Address: types.StandardAddress(pubkey),
		Value:   types.Siacoins(1),
	})
	// also add some SF
	genesisBlock.Transactions[0].SiafundOutputs = []types.SiafundOutput{{
		Address: types.StandardAddress(pubkey),
		Value:   100,
	}}
	sau := GenesisUpdate(genesisBlock, testingDifficulty)
	s := sau.State

	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      sau.NewSiacoinElements[1],
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Address: types.StandardAddress(pubkey),
			Value:   sau.NewSiacoinElements[1].Value,
		}},
		SiafundInputs: []types.SiafundInput{{
			Parent:      sau.NewSiafundElements[0],
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiafundOutputs: []types.SiafundOutput{{
			Address: types.StandardAddress(pubkey),
			Value:   sau.NewSiafundElements[0].Value,
		}},
	}
	signAllInputs(&txn, s, privkey)

	if err := sau.State.ValidateTransactionSet([]types.Transaction{txn, txn}); err == nil {
		t.Fatal("accepted transaction set with repeated txn")
	}

	doubleSpendSCTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      sau.NewSiacoinElements[1],
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Address: types.StandardAddress(pubkey),
			Value:   sau.NewSiacoinElements[1].Value,
		}},
	}
	signAllInputs(&doubleSpendSCTxn, s, privkey)

	if err := sau.State.ValidateTransactionSet([]types.Transaction{txn, doubleSpendSCTxn}); err == nil {
		t.Fatal("accepted transaction set with double spent siacoin output")
	}

	doubleSpendSFTxn := types.Transaction{
		SiafundInputs: []types.SiafundInput{{
			Parent:      sau.NewSiafundElements[0],
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiafundOutputs: []types.SiafundOutput{{
			Address: types.StandardAddress(pubkey),
			Value:   sau.NewSiafundElements[0].Value,
		}},
	}
	signAllInputs(&doubleSpendSFTxn, s, privkey)

	if err := sau.State.ValidateTransactionSet([]types.Transaction{txn, doubleSpendSFTxn}); err == nil {
		t.Fatal("accepted transaction set with double spent siafund output")
	}

	// overfill set with copies of txn
	w := sau.State.TransactionWeight(txn)
	txns := make([]types.Transaction, (sau.State.MaxBlockWeight()/w)+1)
	for i := range txns {
		txns[i] = txn
	}
	if err := sau.State.ValidateTransactionSet(txns); err == nil {
		t.Fatal("accepted overweight transaction set")
	}
}

func TestValidateBlock(t *testing.T) {
	pubkey, privkey := testingKeypair(0)
	genesis := genesisWithSiacoinOutputs(types.SiacoinOutput{
		Address: types.StandardAddress(pubkey),
		Value:   types.Siacoins(1),
	}, types.SiacoinOutput{
		Address: types.StandardAddress(pubkey),
		Value:   types.Siacoins(1),
	})
	sau := GenesisUpdate(genesis, testingDifficulty)
	s := sau.State

	// Mine a block with a few transactions. We are not testing transaction
	// validity here, but the block should still be valid.
	txns := []types.Transaction{
		{
			SiacoinInputs: []types.SiacoinInput{{
				Parent:      sau.NewSiacoinElements[1],
				SpendPolicy: types.PolicyPublicKey(pubkey),
			}},
			SiacoinOutputs: []types.SiacoinOutput{{
				Address: types.VoidAddress,
				Value:   sau.NewSiacoinElements[1].Value,
			}},
		},
		{
			SiacoinInputs: []types.SiacoinInput{{
				Parent:      sau.NewSiacoinElements[2],
				SpendPolicy: types.PolicyPublicKey(pubkey),
			}},
			MinerFee: sau.NewSiacoinElements[2].Value,
		},
	}
	signAllInputs(&txns[0], s, privkey)
	signAllInputs(&txns[1], s, privkey)
	b := mineBlock(s, genesis, txns...)
	if err := s.ValidateBlock(b); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		desc    string
		corrupt func(*types.Block)
	}{
		{
			"incorrect header block height",
			func(b *types.Block) {
				b.Header.Height = 999
			},
		},
		{
			"incorrect header parent ID",
			func(b *types.Block) {
				b.Header.ParentID[0] ^= 1
			},
		},
		{
			"long-past header timestamp",
			func(b *types.Block) {
				b.Header.Timestamp = b.Header.Timestamp.Add(-24 * time.Hour)
			},
		},
		{
			"invalid commitment (different miner address)",
			func(b *types.Block) {
				b.Header.MinerAddress[0] ^= 1
			},
		},
		{
			"invalid commitment (different transactions)",
			func(b *types.Block) {
				b.Transactions = b.Transactions[:1]
			},
		},
	}
	for _, test := range tests {
		corruptBlock := b
		test.corrupt(&corruptBlock)
		if err := s.ValidateBlock(corruptBlock); err == nil {
			t.Fatalf("accepted block with %v", test.desc)
		}
	}
}

func TestNoDoubleContractUpdates(t *testing.T) {
	renterPub, renterPriv := testingKeypair(0)
	hostPub, hostPriv := testingKeypair(1)
	renterAddr := types.StandardAddress(renterPub)
	hostAddr := types.StandardAddress(hostPub)
	genesis := genesisWithSiacoinOutputs(types.SiacoinOutput{
		Address: renterAddr,
		Value:   types.Siacoins(100),
	}, types.SiacoinOutput{
		Address: hostAddr,
		Value:   types.Siacoins(100),
	})
	sau := GenesisUpdate(genesis, testingDifficulty)
	s := sau.State

	signRevision := func(fc *types.FileContract) {
		sigHash := s.ContractSigHash(*fc)
		fc.HostSignature = hostPriv.SignHash(sigHash)
		fc.RenterSignature = renterPriv.SignHash(sigHash)
	}

	// Mine a block with a new file contract.
	fc := types.FileContract{
		WindowStart: 20,
		WindowEnd:   30,
		RenterOutput: types.SiacoinOutput{
			Address: renterAddr,
			Value:   types.Siacoins(5),
		},
		HostOutput: types.SiacoinOutput{
			Address: hostAddr,
			Value:   types.Siacoins(10),
		},
		TotalCollateral: types.ZeroCurrency,
		RenterPublicKey: renterPub,
		HostPublicKey:   hostPub,
	}
	signRevision(&fc)
	formationTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{
			{Parent: sau.NewSiacoinElements[1], SpendPolicy: types.PolicyPublicKey(renterPub)},
			{Parent: sau.NewSiacoinElements[2], SpendPolicy: types.PolicyPublicKey(hostPub)},
		},
		SiacoinOutputs: []types.SiacoinOutput{
			{Address: renterAddr, Value: types.Siacoins(90)},
			{Address: hostAddr, Value: types.Siacoins(95).Sub(s.FileContractTax(fc))},
		},
		FileContracts: []types.FileContract{fc},
	}
	sigHash := s.InputSigHash(formationTxn)
	formationTxn.SiacoinInputs[0].Signatures = []types.Signature{renterPriv.SignHash(sigHash)}
	formationTxn.SiacoinInputs[1].Signatures = []types.Signature{hostPriv.SignHash(sigHash)}
	b := mineBlock(s, genesis, formationTxn)
	if err := s.ValidateBlock(b); err != nil {
		t.Fatal(err)
	}
	sau = ApplyBlock(s, b)
	s = sau.State
	if len(sau.NewFileContracts) != 1 {
		t.Fatal("expected 1 new file contract")
	}
	fce := sau.NewFileContracts[0]

	// mine additional blocks
	for i := 0; i < 5; i++ {
		b = mineBlock(s, b)
		if err := s.ValidateBlock(b); err != nil {
			t.Fatal(err)
		}
		sau = ApplyBlock(s, b)
		s = sau.State
		sau.UpdateElementProof(&fce.StateElement)
	}

	// helper function to return a signed revision of the file contract with the
	// given revision number.
	newRevision := func(n uint64) types.FileContract {
		fc := fce.FileContract
		fc.RevisionNumber = n
		signRevision(&fc)
		return fc
	}

	tests := [][]types.Transaction{
		{
			{
				FileContractRevisions: []types.FileContractRevision{
					{Parent: fce, Revision: newRevision(2)},
				},
			},
			{
				FileContractRevisions: []types.FileContractRevision{
					{Parent: fce, Revision: newRevision(3)},
				},
			},
		},
		{
			{
				FileContractRevisions: []types.FileContractRevision{
					{Parent: fce, Revision: newRevision(2)},
					{Parent: fce, Revision: newRevision(3)},
				},
			},
		},
		{
			{
				FileContractRevisions: []types.FileContractRevision{
					{Parent: fce, Revision: newRevision(2)},
				},
			},
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
				},
			},
		},
		{
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
				},
			},
		},
	}

	for i, set := range tests {
		if err := s.ValidateBlock(mineBlock(s, b, set...)); err == nil {
			t.Fatalf("test %v: expected invalid block error", i)
		} else if !strings.Contains(err.Error(), "multiple times (previously updated in transaction") { // TODO: use errors.Is?
			t.Fatalf("test %v: expected multiple update error, got %v", i, err)
		}
	}

	// apply a final revision
	data := frand.Bytes(64 * 2)
	revisionTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{
			{Parent: fce, Revision: fce.FileContract},
		},
	}
	revisionTxn.FileContractRevisions[0].Revision.FileMerkleRoot = merkle.NodeHash(
		merkle.StorageProofLeafHash(data[:64]),
		merkle.StorageProofLeafHash(data[64:]),
	)
	revisionTxn.FileContractRevisions[0].Revision.RevisionNumber++
	sigHash = s.ContractSigHash(revisionTxn.FileContractRevisions[0].Revision)
	revisionTxn.FileContractRevisions[0].Revision.RenterSignature = renterPriv.SignHash(sigHash)
	revisionTxn.FileContractRevisions[0].Revision.HostSignature = hostPriv.SignHash(sigHash)
	b = mineBlock(s, b, revisionTxn)
	if err := s.ValidateBlock(b); err != nil {
		t.Fatal(err)
	}
	sau = ApplyBlock(s, b)
	s = sau.State
	if len(sau.RevisedFileContracts) != 1 {
		t.Fatal("expected 1 revised file contract")
	} else if !reflect.DeepEqual(sau.RevisedFileContracts[0].FileContract, revisionTxn.FileContractRevisions[0].Revision) {
		t.Fatal("final revision did not match")
	}
	fce = sau.RevisedFileContracts[0]

	// mine until the contract proof window
	for i := s.Index.Height; i < fc.WindowStart; i++ {
		b = mineBlock(s, b)
		if err := s.ValidateBlock(b); err != nil {
			t.Fatal(err)
		}
		sau = ApplyBlock(s, b)
		s = sau.State
		sau.UpdateElementProof(&fce.StateElement)
	}

	// build a vaild proof for the contract
	proof := types.StorageProof{
		WindowStart: s.Index,
		WindowProof: sau.HistoryProof(),
	}
	proofIndex := sau.State.StorageProofLeafIndex(fc.Filesize, proof.WindowStart, fce.ID)
	copy(proof.Leaf[:], data[64*proofIndex:])
	if proofIndex == 0 {
		proof.Proof = append(proof.Proof, merkle.StorageProofLeafHash(data[64:]))
	} else {
		proof.Proof = append(proof.Proof, merkle.StorageProofLeafHash(data[:64]))
	}

	tests = [][]types.Transaction{
		{
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce, StorageProof: proof},
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
				},
			},
		},
		{
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce, StorageProof: proof},
				},
			},
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce, StorageProof: proof},
				},
			},
		},
		{
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
				},
			},
		},
	}

	for i, set := range tests {
		if err := s.ValidateBlock(mineBlock(s, b, set...)); err == nil {
			t.Fatalf("test %v: expected invalid block error", i)
		} else if !strings.Contains(err.Error(), "multiple times (previously updated in transaction") { // TODO: use errors.Is?
			t.Fatalf("test %v: expected multiple update error, got %v", i, err)
		}
	}

	// mine until after contract proof window
	for i := s.Index.Height; i < fc.WindowEnd+1; i++ {
		b = mineBlock(s, b)
		if err := s.ValidateBlock(b); err != nil {
			t.Fatal(err)
		}
		sau = ApplyBlock(s, b)
		s = sau.State
		sau.UpdateElementProof(&fce.StateElement)
	}

	tests = [][]types.Transaction{
		{
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce},
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
				},
			},
		},
		{
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce},
				},
			},
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce},
				},
			},
		},
		{
			{
				FileContractResolutions: []types.FileContractResolution{
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
					{Parent: fce, Finalization: newRevision(types.MaxRevisionNumber)},
				},
			},
		},
	}

	for i, set := range tests {
		if err := s.ValidateBlock(mineBlock(s, b, set...)); err == nil {
			t.Fatalf("test %v: expected invalid block error", i)
		} else if !strings.Contains(err.Error(), "multiple times (previously updated in transaction") { // TODO: use errors.Is?
			t.Fatalf("test %v: expected multiple update error, got %v", i, err)
		}
	}
}
