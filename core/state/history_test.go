package state

import (
	"bytes"
	"context"
	"fmt"
	"github.com/ledgerwatch/turbo-geth/common/changeset"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/common/debug"
	"github.com/ledgerwatch/turbo-geth/core/types/accounts"
	"github.com/ledgerwatch/turbo-geth/crypto"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/stretchr/testify/assert"
)

func TestMutation_DeleteTimestamp(t *testing.T) {
	db := ethdb.NewMemDatabase()
	mutDB := db.NewBatch()

	acc := make([]*accounts.Account, 10)
	addr := make([]common.Address, 10)
	addrHashes := make([]common.Hash, 10)
	tds := NewTrieDbState(common.Hash{}, mutDB, 1)
	blockWriter := tds.DbStateWriter()
	ctx := context.Background()
	emptyAccount := accounts.NewAccount()
	for i := range acc {
		acc[i], addr[i], addrHashes[i] = randomAccount(t)
		if err := blockWriter.UpdateAccountData(ctx, addr[i], &emptyAccount /* original */, acc[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}
	_, err := mutDB.Commit()
	if err != nil {
		t.Fatal(err)
	}

	csData, err := db.Get(dbutils.AccountChangeSetBucket, dbutils.EncodeTimestamp(1))
	if err != nil {
		t.Fatal(err)
	}

	if changeset.Len(csData) != 10 {
		t.FailNow()
	}
	if debug.IsThinHistory() {
		csData, err = db.Get(dbutils.AccountsHistoryBucket, addrHashes[0].Bytes())
		if err != nil {
			t.Fatal(err)
		}
		index := dbutils.WrapHistoryIndex(csData)
		parsed, innerErr := index.Decode()
		if innerErr != nil {
			t.Fatal(innerErr)
		}
		if parsed[0] != 1 {
			t.Fatal("incorrect block num")
		}

	} else {
		compositeKey, _ := dbutils.CompositeKeySuffix(addrHashes[0].Bytes(), 1)
		_, innerErr := db.Get(dbutils.AccountsHistoryBucket, compositeKey)
		if innerErr != nil {
			t.Fatal(innerErr)
		}
	}

	err = tds.deleteTimestamp(1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mutDB.Commit()
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Get(dbutils.AccountChangeSetBucket, dbutils.EncodeTimestamp(1))
	if err != ethdb.ErrKeyNotFound {
		t.Fatal("changeset must be deleted")
	}

	if debug.IsThinHistory() {
		_, err = db.Get(dbutils.AccountsHistoryBucket, addrHashes[0].Bytes())
		if err != ethdb.ErrKeyNotFound {
			t.Fatal("account must be deleted")
		}
	} else {
		compositeKey, _ := dbutils.CompositeKeySuffix(addrHashes[0].Bytes(), 1)
		_, err = db.Get(dbutils.AccountsHistoryBucket, compositeKey)
		if err != ethdb.ErrKeyNotFound {
			t.Fatal("account must be deleted")
		}
	}
}

func TestMutationCommit(t *testing.T) {
	if debug.IsThinHistory() {
		t.Skip()
	}
	db := ethdb.NewMemDatabase()
	mutDB := db.NewBatch()

	numOfAccounts := 5
	numOfStateKeys := 5
	addrHashes, accState, accStateStorage, accHistory, accHistoryStateStorage := generateAccountsWithStorageAndHistory(t, mutDB, numOfAccounts, numOfStateKeys)

	_, commitErr := mutDB.Commit()
	if commitErr != nil {
		t.Fatal(commitErr)
	}

	for i, addrHash := range addrHashes {
		b, err := db.Get(dbutils.AccountsBucket, addrHash.Bytes())
		if err != nil {
			t.Fatal("error on get account", i, err)
		}

		acc := accounts.NewAccount()
		err = acc.DecodeForStorage(b)
		if err != nil {
			t.Fatal("error on get account", i, err)
		}
		if !accState[i].Equals(&acc) {
			spew.Dump("got", acc)
			spew.Dump("expected", accState[i])
			t.Fatal("Accounts not equals")
		}

		compositeKey, _ := dbutils.CompositeKeySuffix(addrHash.Bytes(), 1)
		b, err = db.Get(dbutils.AccountsHistoryBucket, compositeKey)
		if err != nil {
			t.Fatal("error on get account", i, err)
		}

		acc = accounts.NewAccount()
		err = acc.DecodeForStorage(b)
		if err != nil {
			t.Fatal("error on get account", i, err)
		}
		if !accHistory[i].Equals(&acc) {
			spew.Dump("got", acc)
			spew.Dump("expected", accState[i])
			t.Fatal("Accounts not equals")
		}

		resAccStorage := make(map[common.Hash]common.Hash)
		err = db.Walk(dbutils.StorageBucket, dbutils.GenerateStoragePrefix(addrHash, acc.Incarnation), common.HashLength+8, func(k, v []byte) (b bool, e error) {
			resAccStorage[common.BytesToHash(k[common.HashLength+8:])] = common.BytesToHash(v)
			return true, nil
		})
		if err != nil {
			t.Fatal("error on get account storage", i, err)
		}

		if !reflect.DeepEqual(resAccStorage, accStateStorage[i]) {
			spew.Dump("res", resAccStorage)
			spew.Dump("expected", accHistoryStateStorage[i])
			t.Log("incorrect storage", i)
		}

		resAccStorage = make(map[common.Hash]common.Hash)
		err = db.Walk(dbutils.StorageHistoryBucket, dbutils.GenerateStoragePrefix(addrHash, acc.Incarnation), common.HashLength+8, func(k, v []byte) (b bool, e error) {
			resAccStorage[common.BytesToHash(k[common.HashLength+8:common.HashLength+8+common.HashLength])] = common.BytesToHash(v)
			return true, nil
		})
		if err != nil {
			t.Fatal("error on get account storage", i, err)
		}

		if !reflect.DeepEqual(resAccStorage, accHistoryStateStorage[i]) {
			spew.Dump("res", resAccStorage)
			spew.Dump("expected", accHistoryStateStorage[i])
			t.Fatal("incorrect history storage", i)
		}
	}

	csData, err := db.Get(dbutils.AccountChangeSetBucket, dbutils.EncodeTimestamp(1))
	if err != nil {
		t.Fatal(err)
	}

	expectedChangeSet := changeset.NewAccountChangeSet()
	for i := range addrHashes {
		b := make([]byte, accHistory[i].EncodingLengthForStorage())
		accHistory[i].EncodeForStorage(b)
		err = expectedChangeSet.Add(addrHashes[i].Bytes(), b)
		if err != nil {
			t.Fatal(err)
		}
	}

	sort.Sort(expectedChangeSet)
	expectedData, err := changeset.EncodeChangeSet(expectedChangeSet)
	assert.NoError(t, err)
	if !bytes.Equal(csData, expectedData) {
		spew.Dump("res", csData)
		spew.Dump("expected", expectedData)
		t.Fatal("incorrect account changeset")
	}

	csData, err = db.Get(dbutils.StorageChangeSetBucket, dbutils.EncodeTimestamp(1))
	if err != nil {
		t.Fatal(err)
	}

	if changeset.Len(csData) != numOfAccounts*numOfStateKeys {
		t.FailNow()
	}

	expectedChangeSet = changeset.NewStorageChangeSet()
	for i, addrHash := range addrHashes {
		for j := 0; j < numOfStateKeys; j++ {
			key := common.Hash{uint8(i*100 + j)}
			keyHash, err := common.HashData(key.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			value := common.Hash{uint8(10 + j)}
			if err := expectedChangeSet.Add(dbutils.GenerateCompositeStorageKey(addrHash, accHistory[i].Incarnation, keyHash), value.Bytes()); err != nil {
				t.Fatal(err)
			}

		}
	}

	sort.Sort(expectedChangeSet)

	expectedData, err = changeset.EncodeChangeSet(expectedChangeSet)
	if debug.IsThinHistory() {
		expectedData, err = changeset.EncodeStorage(expectedChangeSet)
	}
	assert.NoError(t, err)
	if !bytes.Equal(csData, expectedData) {
		spew.Dump("res", csData)
		spew.Dump("expected", expectedData)
		t.Fatal("incorrect storage changeset")
	}
}

func TestMutationCommitThinHistory(t *testing.T) {
	if !debug.IsThinHistory() {
		t.Skip()
	}

	db := ethdb.NewMemDatabase()
	mutDB := db.NewBatch()

	numOfAccounts := 5
	numOfStateKeys := 5

	addrHashes, accState, accStateStorage, accHistory, accHistoryStateStorage := generateAccountsWithStorageAndHistory(t, mutDB, numOfAccounts, numOfStateKeys)

	_, commitErr := mutDB.Commit()
	if commitErr != nil {
		t.Fatal(commitErr)
	}

	for i, addrHash := range addrHashes {
		b, err := db.Get(dbutils.AccountsBucket, addrHash.Bytes())
		if err != nil {
			t.Fatal("error on get account", i, err)
		}

		acc := accounts.NewAccount()
		err = acc.DecodeForStorage(b)
		if err != nil {
			t.Fatal("error on get account", i, err)
		}
		if !accState[i].Equals(&acc) {
			spew.Dump("got", acc)
			spew.Dump("expected", accState[i])
			t.Fatal("Accounts not equals")
		}

		b, err = db.Get(dbutils.AccountsHistoryBucket, addrHash.Bytes())
		if err != nil {
			t.Fatal("error on get account", i, err)
		}
		index := dbutils.WrapHistoryIndex(b)
		parsedIndex, err := index.Decode()
		if err != nil {
			t.Fatal("error on get account", i, err)
		}

		if parsedIndex[0] != 1 && index.Len() != 1 {
			t.Fatal("incorrect history index")
		}

		resAccStorage := make(map[common.Hash]common.Hash)
		err = db.Walk(dbutils.StorageBucket, dbutils.GenerateStoragePrefix(addrHash, acc.Incarnation), common.HashLength+8, func(k, v []byte) (b bool, e error) {
			resAccStorage[common.BytesToHash(k[common.HashLength+8:])] = common.BytesToHash(v)
			return true, nil
		})
		if err != nil {
			t.Fatal("error on get account storage", i, err)
		}

		if !reflect.DeepEqual(resAccStorage, accStateStorage[i]) {
			spew.Dump("res", resAccStorage)
			spew.Dump("expected", accStateStorage[i])
			t.Fatal("incorrect storage", i)
		}

		for k, v := range accHistoryStateStorage[i] {
			res, err := db.GetAsOf(dbutils.StorageBucket, dbutils.StorageHistoryBucket, dbutils.GenerateCompositeStorageKey(addrHash, acc.Incarnation, k), 1)
			if err != nil {
				t.Fatal(err)
			}

			resultHash := common.BytesToHash(res)
			if resultHash != v {
				t.Fatal("incorrect storage history for ", addrHash.String(), v, resultHash)
			}
		}
	}

	csData, err := db.Get(dbutils.AccountChangeSetBucket, dbutils.EncodeTimestamp(1))
	if err != nil {
		t.Fatal(err)
	}

	expectedChangeSet := changeset.NewAccountChangeSet()
	for i := range addrHashes {
		b := make([]byte, accHistory[i].EncodingLengthForStorage())
		accHistory[i].EncodeForStorage(b)
		innerErr := expectedChangeSet.Add(addrHashes[i].Bytes(), b)
		if innerErr != nil {
			t.Fatal(innerErr)
		}

	}

	expectedData, err := changeset.EncodeChangeSet(expectedChangeSet)
	assert.NoError(t, err)
	if !bytes.Equal(csData, expectedData) {
		t.Fatal("incorrect changeset")
	}

	csData, err = db.Get(dbutils.StorageChangeSetBucket, dbutils.EncodeTimestamp(1))
	if err != nil {
		t.Fatal(err)
	}

	if changeset.Len(csData) != numOfAccounts*numOfStateKeys {
		t.FailNow()
	}

	expectedChangeSet = changeset.NewStorageChangeSet()
	for i, addrHash := range addrHashes {
		for j := 0; j < numOfStateKeys; j++ {
			key := common.Hash{uint8(i*100 + j)}
			value := common.Hash{uint8(10 + j)}
			err := expectedChangeSet.Add(dbutils.GenerateCompositeStorageKey(addrHash, accHistory[i].Incarnation, key), value.Bytes())
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	expectedData, err = changeset.EncodeStorage(expectedChangeSet)
	assert.NoError(t, err)
	if !bytes.Equal(csData, expectedData) {
		spew.Dump("res", csData)
		spew.Dump("expected", expectedData)
		t.Fatal("incorrect changeset")
	}
}

func generateAccountsWithStorageAndHistory(t *testing.T, db ethdb.Database, numOfAccounts, numOfStateKeys int) ([]common.Hash, []*accounts.Account, []map[common.Hash]common.Hash, []*accounts.Account, []map[common.Hash]common.Hash) {
	t.Helper()

	accHistory := make([]*accounts.Account, numOfAccounts)
	accState := make([]*accounts.Account, numOfAccounts)
	accStateStorage := make([]map[common.Hash]common.Hash, numOfAccounts)
	accHistoryStateStorage := make([]map[common.Hash]common.Hash, numOfAccounts)
	addrs := make([]common.Address, numOfAccounts)
	addrHashes := make([]common.Hash, numOfAccounts)
	tds := NewTrieDbState(common.Hash{}, db, 1)
	blockWriter := tds.DbStateWriter()
	ctx := context.Background()
	for i := range accHistory {
		accHistory[i], addrs[i], addrHashes[i] = randomAccount(t)
		accHistory[i].Balance = *big.NewInt(100)
		accHistory[i].CodeHash = common.Hash{uint8(10 + i)}
		accHistory[i].Root = common.Hash{uint8(10 + i)}
		accHistory[i].Incarnation = uint64(i + 1)

		accState[i] = accHistory[i].SelfCopy()
		accState[i].Nonce++
		accState[i].Balance = *big.NewInt(200)

		accStateStorage[i] = make(map[common.Hash]common.Hash)
		accHistoryStateStorage[i] = make(map[common.Hash]common.Hash)
		for j := 0; j < numOfStateKeys; j++ {
			key := common.Hash{uint8(i*100 + j)}
			keyHash, err := common.HashData(key.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			newValue := common.Hash{uint8(j)}
			accStateStorage[i][keyHash] = newValue

			value := common.Hash{uint8(10 + j)}
			accHistoryStateStorage[i][keyHash] = value
			if err := blockWriter.WriteAccountStorage(ctx, addrs[i], accHistory[i].Incarnation, &key, &value, &newValue); err != nil {
				t.Fatal(err)
			}
		}
		if err := blockWriter.UpdateAccountData(ctx, addrs[i], accHistory[i] /* original */, accState[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}
	return addrHashes, accState, accStateStorage, accHistory, accHistoryStateStorage
}

func TestMutation_GetAsOf(t *testing.T) {
	db := ethdb.NewMemDatabase()
	mutDB := db.NewBatch()
	tds := NewTrieDbState(common.Hash{}, mutDB, 0)
	blockWriter := tds.DbStateWriter()
	ctx := context.Background()
	emptyAccount := accounts.NewAccount()

	acc, addr, addrHash := randomAccount(t)
	acc2 := acc.SelfCopy()
	acc2.Nonce = 1
	acc4 := acc.SelfCopy()
	acc4.Nonce = 3

	tds.SetBlockNr(0)
	if err := blockWriter.UpdateAccountData(ctx, addr, &emptyAccount, acc2); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}

	tds.SetBlockNr(2)
	if err := blockWriter.UpdateAccountData(ctx, addr, acc2, acc4); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}

	tds.SetBlockNr(4)
	if err := blockWriter.UpdateAccountData(ctx, addr, acc4, acc); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}

	if _, err := mutDB.Commit(); err != nil {
		t.Fatal(err)
	}

	b, err := db.Get(dbutils.AccountsBucket, addrHash.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	resAcc := new(accounts.Account)
	err = resAcc.DecodeForStorage(b)
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Equals(resAcc) {
		t.Fatal("Account from Get is incorrect")
	}

	b, err = db.GetAsOf(dbutils.AccountsBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 1)
	if err != nil {
		t.Fatal("incorrect value on block 1", err)
	}
	resAcc = new(accounts.Account)
	err = resAcc.DecodeForStorage(b)
	if err != nil {
		t.Fatal(err)
	}

	if !acc2.Equals(resAcc) {
		spew.Dump(resAcc)
		t.Fatal("Account from GetAsOf(1) is incorrect")
	}

	b, err = db.GetAsOf(dbutils.AccountsBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 2)
	if err != nil {
		t.Fatal(err)
	}
	resAcc = new(accounts.Account)
	err = resAcc.DecodeForStorage(b)
	if err != nil {
		t.Fatal(err)
	}
	if !acc2.Equals(resAcc) {
		spew.Dump(resAcc)
		t.Fatal("Account from GetAsOf(2) is incorrect")
	}

	b, err = db.GetAsOf(dbutils.AccountsBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 3)
	if err != nil {
		t.Fatal(err)
	}
	resAcc = new(accounts.Account)
	err = resAcc.DecodeForStorage(b)
	if err != nil {
		t.Fatal(err)
	}
	if !acc4.Equals(resAcc) {
		spew.Dump(resAcc)
		t.Fatal("Account from GetAsOf(2) is incorrect")
	}

	b, err = db.GetAsOf(dbutils.AccountsBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 5)
	if err != nil {
		t.Fatal(err)
	}
	resAcc = new(accounts.Account)
	err = resAcc.DecodeForStorage(b)
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Equals(resAcc) {
		t.Fatal("Account from GetAsOf(4) is incorrect")
	}

	b, err = db.GetAsOf(dbutils.AccountsBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 7)
	if err != nil {
		t.Fatal(err)
	}
	resAcc = new(accounts.Account)
	err = resAcc.DecodeForStorage(b)
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Equals(resAcc) {
		t.Fatal("Account from GetAsOf(7) is incorrect")
	}
}

func randomAccount(t *testing.T) (*accounts.Account, common.Address, common.Hash) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.NewAccount()
	acc.Initialised = true
	acc.Balance = *big.NewInt(rand.Int63())
	addr := crypto.PubkeyToAddress(key.PublicKey)
	addrHash, err := common.HashData(addr.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return &acc, addr, addrHash
}

func TestBoltDB_WalkAsOf1(t *testing.T) {
	if debug.IsThinHistory() {
		t.Skip()
	}
	db := ethdb.NewMemDatabase()

	block2Expected := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	block4Expected := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	block6Expected := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	//create state and history
	for i := uint8(1); i < 5; i++ {
		key := dbutils.GenerateCompositeStorageKey(common.Hash{i}, uint64(1), common.Hash{i})
		val := []byte("state   " + strconv.Itoa(int(i)))
		err := db.Put(dbutils.StorageBucket, key, val)
		if err != nil {
			t.Fatal(err)
		}
		err = block6Expected.Add(key, val)
		if err != nil {
			t.Fatal(err)
		}

		if i <= 2 {
			err = block4Expected.Add(key, val)
			if err != nil {
				t.Fatal(err)
			}

		}
	}
	for i := uint8(1); i <= 7; i++ {
		key := dbutils.GenerateCompositeStorageKey(common.Hash{i}, 1, common.Hash{i})
		val := []byte("block 3 " + strconv.Itoa(int(i)))
		err := db.PutS(
			dbutils.StorageHistoryBucket,
			key,
			val,
			3,
			false,
		)
		if err != nil {
			t.Fatal(err)
		}

		err = block2Expected.Add(key, val)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := uint8(3); i <= 7; i++ {
		key := dbutils.GenerateCompositeStorageKey(common.Hash{i}, 1, common.Hash{i})
		val := []byte("block 5 " + strconv.Itoa(int(i)))
		err := db.PutS(
			dbutils.StorageHistoryBucket,
			key,
			val,
			5,
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		err = block4Expected.Add(key, val)
		if err != nil {
			t.Fatal(err)
		}

	}

	block2 := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	block4 := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	block6 := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	//walk and collect walkAsOf result
	var err error
	var startKey [72]byte
	err = db.WalkAsOf(dbutils.StorageBucket, dbutils.StorageHistoryBucket, startKey[:], 0, 2, func(k []byte, v []byte) (b bool, e error) {
		err = block2.Add(k, v)
		if err != nil {
			t.Fatal(err)
		}

		//fmt.Printf("%v - %v \n", common.BytesToHash(k).String(), string(v))
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = db.WalkAsOf(dbutils.StorageBucket, dbutils.StorageHistoryBucket, startKey[:], 0, 4, func(k []byte, v []byte) (b bool, e error) {
		err = block4.Add(k, v)
		if err != nil {
			t.Fatal(err)
		}

		//fmt.Printf("%v - %v \n", common.BytesToHash(k).String(), string(v))
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = db.WalkAsOf(dbutils.StorageBucket, dbutils.StorageHistoryBucket, startKey[:], 0, 6, func(k []byte, v []byte) (b bool, e error) {
		err = block6.Add(k, v)
		if err != nil {
			t.Fatal(err)
		}

		//fmt.Printf("%v - %v \n", common.BytesToHash(k).String(), string(v))
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(block2, block2Expected) {
		spew.Dump("expected", block2Expected)
		spew.Dump("current", block2)
		t.Fatal("block 2 result is incorrect")
	}
	if !reflect.DeepEqual(block4, block4Expected) {
		spew.Dump(block6)
		t.Fatal("block 4 result is incorrect")
	}
	if !reflect.DeepEqual(block6, block6Expected) {
		spew.Dump(block6)
		t.Fatal("block 6 result is incorrect")
	}
}

func TestBoltDB_MultiWalkAsOf(t *testing.T) {
	if debug.IsThinHistory() {
		t.Skip()
	}

	db := ethdb.NewMemDatabase()

	block2Expected := &changeset.ChangeSet{
		Changes: []changeset.Change{
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{1}, 1, common.Hash{1}),
				Value: []byte("block 3 " + strconv.Itoa(1)),
			},
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{3}, 1, common.Hash{3}),
				Value: []byte("block 3 " + strconv.Itoa(3)),
			},
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{7}, 1, common.Hash{7}),
				Value: []byte("block 3 " + strconv.Itoa(7)),
			},
		},
	}

	block4Expected := &changeset.ChangeSet{
		Changes: []changeset.Change{
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{1}, 1, common.Hash{1}),
				Value: []byte("state   " + strconv.Itoa(1)),
			},
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{3}, 1, common.Hash{3}),
				Value: []byte("block 5 " + strconv.Itoa(3)),
			},
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{7}, 1, common.Hash{7}),
				Value: []byte("block 5 " + strconv.Itoa(7)),
			},
		},
	}

	block6Expected := &changeset.ChangeSet{
		Changes: []changeset.Change{
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{1}, 1, common.Hash{1}),
				Value: []byte("state   " + strconv.Itoa(1)),
			},
			{
				Key:   dbutils.GenerateCompositeStorageKey(common.Hash{3}, 1, common.Hash{3}),
				Value: []byte("state   " + strconv.Itoa(3)),
			},
		},
	}

	//create state and history
	for i := uint8(1); i < 5; i++ {
		key := dbutils.GenerateCompositeStorageKey(common.Hash{i}, uint64(1), common.Hash{i})
		val := []byte("state   " + strconv.Itoa(int(i)))
		err := db.Put(dbutils.StorageBucket, key, val)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := uint8(1); i <= 7; i++ {
		key := dbutils.GenerateCompositeStorageKey(common.Hash{i}, 1, common.Hash{i})
		val := []byte("block 3 " + strconv.Itoa(int(i)))
		err := db.PutS(
			dbutils.StorageHistoryBucket,
			key,
			val,
			3,
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := uint8(3); i <= 7; i++ {
		key := dbutils.GenerateCompositeStorageKey(common.Hash{i}, 1, common.Hash{i})
		val := []byte("block 5 " + strconv.Itoa(int(i)))
		err := db.PutS(
			dbutils.StorageHistoryBucket,
			key,
			val,
			5,
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	//walk and collect walkAsOf result
	var err error
	startKeys := [][]byte{
		dbutils.GenerateCompositeStorageKey(common.Hash{1}, 1, common.Hash{1}),
		dbutils.GenerateCompositeStorageKey(common.Hash{3}, 1, common.Hash{3}),
		dbutils.GenerateCompositeStorageKey(common.Hash{7}, 1, common.Hash{7}),
	}
	fixedBits := []uint{
		60,
		60,
		60,
	}

	if err != nil {
		t.Fatal(err)
	}

	if err != nil {
		t.Fatal(err)
	}

	if err != nil {
		t.Fatal(err)
	}

	block2 := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	block4 := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	block6 := &changeset.ChangeSet{
		Changes: make([]changeset.Change, 0),
	}

	err = db.MultiWalkAsOf(dbutils.StorageBucket, dbutils.StorageHistoryBucket, startKeys, fixedBits, 2, func(idx int, k []byte, v []byte) error {
		fmt.Printf("%v - %s - %s\n", idx, string(k), string(v))
		err = block2.Add(k, v)
		if err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.MultiWalkAsOf(dbutils.StorageBucket, dbutils.StorageHistoryBucket, startKeys, fixedBits, 4, func(idx int, k []byte, v []byte) error {
		fmt.Printf("%v - %s - %s\n", idx, string(k), string(v))
		err = block4.Add(k, v)
		if err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.MultiWalkAsOf(dbutils.StorageBucket, dbutils.StorageHistoryBucket, startKeys, fixedBits, 6, func(idx int, k []byte, v []byte) error {
		fmt.Printf("%v - %s - %s\n", idx, string(k), string(v))
		err = block6.Add(k, v)
		if err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(block2Expected, block2) {
		spew.Dump(block2)
		t.Fatal("block2")
	}
	if !reflect.DeepEqual(block4Expected, block4) {
		spew.Dump(block4)
		t.Fatal("block4")
	}
	if !reflect.DeepEqual(block6Expected, block6) {
		spew.Dump(block6)
		t.Fatal("block6")
	}
}
