package state

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/changeset"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/core/rawdb"
	"github.com/ledgerwatch/turbo-geth/core/types/accounts"
	"github.com/ledgerwatch/turbo-geth/crypto"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/trie"
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

	indexBytes, _, innerErr := db.GetIndexChunk(dbutils.AccountsHistoryBucket, addrHashes[0].Bytes(), 1)
	if innerErr != nil {
		t.Fatal(err)
	}

	index := dbutils.WrapHistoryIndex(indexBytes)

	parsed, innerErr := index.Decode()
	if innerErr != nil {
		t.Fatal(innerErr)
	}
	if parsed[0] != 1 {
		t.Fatal("incorrect block num")
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

	_, err = db.Get(dbutils.AccountsHistoryBucket, addrHashes[0].Bytes())
	if err != ethdb.ErrKeyNotFound {
		t.Fatal("account must be deleted")
	}
}

func TestMutationCommitThinHistory(t *testing.T) {
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
		acc := accounts.NewAccount()
		if ok, err := rawdb.ReadAccount(db, addrHash, &acc); err != nil {
			t.Fatal("error on get account", i, err)
		} else if !ok {
			t.Fatal("error on get account", i)
		}

		if !accState[i].Equals(&acc) {
			spew.Dump("got", acc)
			spew.Dump("expected", accState[i])
			t.Fatal("Accounts not equals")
		}

		indexBytes, _, err := db.GetIndexChunk(dbutils.AccountsHistoryBucket, addrHash.Bytes(), 2)
		if err != nil {
			t.Fatal("error on get account", i, err)
		}

		index := dbutils.WrapHistoryIndex(indexBytes)
		parsedIndex, err := index.Decode()
		if err != nil {
			t.Fatal("error on get account", i, err)
		}

		if parsedIndex[0] != 1 && index.Len() != 1 {
			t.Fatal("incorrect history index")
		}

		resAccStorage := make(map[common.Hash]common.Hash)
		err = db.Walk(dbutils.CurrentStateBucket, dbutils.GenerateStoragePrefix(addrHash, acc.Incarnation), 8*(common.HashLength+8), func(k, v []byte) (b bool, e error) {
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
			res, err := db.GetAsOf(dbutils.CurrentStateBucket, dbutils.StorageHistoryBucket, dbutils.GenerateCompositeStorageKey(addrHash, acc.Incarnation, k), 1)
			if err != nil {
				t.Fatal(err)
			}

			resultHash := common.BytesToHash(res)
			if resultHash != v {
				t.Fatalf("incorrect storage history for %x %x %x", addrHash.String(), v, resultHash)
			}
		}
	}

	csData, err := db.Get(dbutils.AccountChangeSetBucket, dbutils.EncodeTimestamp(2))
	if err != nil {
		t.Fatal(err)
	}

	expectedChangeSet := changeset.NewAccountChangeSet()
	for i := range addrHashes {
		// Make ajustments for THIN_HISTORY
		c := accHistory[i].SelfCopy()
		copy(c.CodeHash[:], emptyCodeHash)
		c.Root = trie.EmptyRoot
		bLen := c.EncodingLengthForStorage()
		b := make([]byte, bLen)
		c.EncodeForStorage(b)
		innerErr := expectedChangeSet.Add(addrHashes[i].Bytes(), b)
		if innerErr != nil {
			t.Fatal(innerErr)
		}
	}
	sort.Sort(expectedChangeSet)
	expectedData, err := changeset.EncodeAccounts(expectedChangeSet)
	assert.NoError(t, err)
	if !bytes.Equal(csData, expectedData) {
		spew.Dump("res", csData)
		spew.Dump("expected", expectedData)
		t.Fatal("incorrect changeset")
	}

	csData, err = db.Get(dbutils.StorageChangeSetBucket, dbutils.EncodeTimestamp(2))
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
			keyHash, err1 := common.HashData(key.Bytes())
			if err1 != nil {
				t.Fatal(err1)
			}
			value := common.Hash{uint8(10 + j)}
			if err2 := expectedChangeSet.Add(dbutils.GenerateCompositeStorageKey(addrHash, accHistory[i].Incarnation, keyHash), value.Bytes()); err2 != nil {
				t.Fatal(err2)
			}
		}
	}
	sort.Sort(expectedChangeSet)
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
			if newValue != (common.Hash{}) {
				// Empty value is not considered to be present
				accStateStorage[i][keyHash] = newValue
			}

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
	tds.SetBlockNr(2)
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}
	return addrHashes, accState, accStateStorage, accHistory, accHistoryStateStorage
}

func TestMutationIndexChunking(t *testing.T) {
	boltDB := ethdb.NewMemDatabase()
	mutDB := boltDB.NewBatch()
	bgDB, err := ethdb.NewEphemeralBadger()
	if err != nil {
		t.Fatal(err)
	}
	dbs := []ethdb.Database{mutDB, ethdb.NewMemDatabase(), bgDB}
	for _, db := range dbs {
		db := db
		t.Run(reflect.TypeOf(db).String(), func(t *testing.T) {

			tds := NewTrieDbState(common.Hash{}, db, 0)
			blockWriter := tds.DbStateWriter()
			ctx := context.Background()
			emptyAccount := accounts.NewAccount()

			acc, addr, addrHash := randomAccount(t)

			m := make(map[string][]byte)
			for i := uint64(0); i < 250; i++ {
				tds.SetBlockNr(i)
				newAcc := acc.SelfCopy()
				newAcc.Nonce = i
				if err := blockWriter.UpdateAccountData(ctx, addr, &emptyAccount, newAcc); err != nil {
					t.Fatal(err)
				}
				if err := blockWriter.WriteChangeSets(); err != nil {
					t.Fatal(err)
				}
				if err := blockWriter.WriteHistory(); err != nil {
					t.Fatal(err)
				}

				v, k, err := db.GetIndexChunk(dbutils.AccountsHistoryBucket, addrHash.Bytes(), i)
				if err != nil && err != ethdb.ErrKeyNotFound {
					t.Error(err)
				}
				index := dbutils.WrapHistoryIndex(v)
				m[string(k)] = *index
			}

			if len(m) != 2 {
				spew.Dump(m)
				t.Fatal("incorrect number of chunks")
			}
			k := dbutils.IndexChunkKey(addrHash.Bytes(), 0)
			vv, err := dbutils.WrapHistoryIndex(m[string(k)]).Decode()
			if err != nil {
				t.Fatal(dbutils.IndexChunkKey(addrHash.Bytes(), 0), err)
			}

			firstChunkValues := make([]uint64, 247)
			for i := range firstChunkValues {
				firstChunkValues[i] = uint64(i)
			}
			if !reflect.DeepEqual(vv, firstChunkValues) {
				spew.Dump(vv)
				spew.Dump(firstChunkValues)
				t.Fatal("not equals")
			}

			k = dbutils.IndexChunkKey(addrHash.Bytes(), 247)
			vv, err = dbutils.WrapHistoryIndex(m[string(k)]).Decode()
			if err != nil {
				t.Fatal(dbutils.IndexChunkKey(addrHash.Bytes(), 0), err)
			}

			secondChunkValues := []uint64{247, 248, 249}
			if !reflect.DeepEqual(vv, secondChunkValues) {
				spew.Dump(vv)
				spew.Dump(firstChunkValues)
				t.Fatal("not equals")
			}

		})
	}
}

func TestMutationGetAsOfCheck(t *testing.T) {
	boltDB := ethdb.NewMemDatabase()
	mutDB := boltDB.NewBatch()

	/*
		todo uncomment after add thin history to badgerdb
		bgDB,err:= ethdb.NewEphemeralBadger()
		if err!=nil {
			t.Fatal(err)
		}

	*/

	dbs := []ethdb.Database{
		mutDB,
		ethdb.NewMemDatabase(),
		//bgDB,
	}
	for _, db := range dbs {
		db := db
		t.Run(reflect.TypeOf(db).String(), func(t *testing.T) {
			tds := NewTrieDbState(common.Hash{}, db, 0)
			blockWriter := tds.DbStateWriter()
			ctx := context.Background()

			firstAccNonce := uint64(2)

			acc, addr, addrHash := randomAccount(t)
			prevAcc := acc.SelfCopy()
			prevAcc.Nonce = firstAccNonce

			m := make(map[string][]byte)
			for i := uint64(5); i < 255; i++ {
				tds.SetBlockNr(i)
				newAcc := acc.SelfCopy()
				newAcc.Nonce = i
				if err := blockWriter.UpdateAccountData(ctx, addr, prevAcc, newAcc); err != nil {
					t.Fatal(err)
				}
				prevAcc = newAcc.SelfCopy()
				if err := blockWriter.WriteChangeSets(); err != nil {
					t.Fatal(err)
				}
				if err := blockWriter.WriteHistory(); err != nil {
					t.Fatal(err)
				}

				v, chunkKey, err := db.GetIndexChunk(dbutils.AccountsHistoryBucket, addrHash.Bytes(), i)
				if err != nil && err != ethdb.ErrKeyNotFound {
					t.Error(err)
				}
				index := dbutils.WrapHistoryIndex(v)
				m[string(chunkKey)] = *index
			}
			if commiter, ok := db.(ethdb.DbWithPendingMutations); ok {
				_, err := commiter.Commit()
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(m) != 2 {
				spew.Dump(m)
				t.Fatal("incorrect number of chunks")
			}

			k := dbutils.IndexChunkKey(addrHash.Bytes(), 5)
			vv, err := dbutils.WrapHistoryIndex(m[string(k)]).Decode()
			if err != nil {
				t.Fatal(dbutils.IndexChunkKey(addrHash.Bytes(), 0), err)
			}
			if len(vv) == 0 {
				t.Fatal("empty index")
			}

			firstChunkValues := make([]uint64, 247)
			for i := range firstChunkValues {
				firstChunkValues[i] = uint64(i + 5)
			}
			if !reflect.DeepEqual(vv, firstChunkValues) {
				spew.Dump(vv)
				spew.Dump(firstChunkValues)
				t.Fatal("not equals")
			}

			checkNonceForBlock := func(blockNum, correctNonce uint64) {
				t.Helper()
				v, err := db.GetAsOf(dbutils.CurrentStateBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), blockNum)
				if err != nil {
					t.Fatal(err)
				}
				gotAcc := accounts.NewAccount()
				err = gotAcc.DecodeForStorage(v)
				if err != nil {
					t.Fatal(err)
				}

				if gotAcc.Nonce != correctNonce {
					t.Fatal("incorrect nonce for ", blockNum, " block", "got", gotAcc.Nonce, "wait", correctNonce)
				}
			}

			checkNonceForBlock(1, firstAccNonce)
			checkNonceForBlock(5, firstAccNonce)
			checkNonceForBlock(6, 5)
			checkNonceForBlock(255, 254)
			checkNonceForBlock(247, 246)
			checkNonceForBlock(248, 247)
		})
	}
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

	blockWriter = tds.DbStateWriter()
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

	blockWriter = tds.DbStateWriter()
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

	resAcc := new(accounts.Account)
	ok, err := rawdb.ReadAccount(db, addrHash, resAcc)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal(errors.New("acc not found"))
	}

	if !acc.Equals(resAcc) {
		t.Fatal("Account from Get is incorrect")
	}

	b, err := db.GetAsOf(dbutils.CurrentStateBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 1)
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

	b, err = db.GetAsOf(dbutils.CurrentStateBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 2)
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

	b, err = db.GetAsOf(dbutils.CurrentStateBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 3)
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

	b, err = db.GetAsOf(dbutils.CurrentStateBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 5)
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

	b, err = db.GetAsOf(dbutils.CurrentStateBucket, dbutils.AccountsHistoryBucket, addrHash.Bytes(), 7)
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
	// TODO: remove or recover
	t.Skip()

	db := ethdb.NewMemDatabase()
	tds := NewTrieDbState(common.Hash{}, db, 1)
	blockWriter := tds.DbStateWriter()
	ctx := context.Background()
	emptyVal := common.Hash{}

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
	for i := uint8(1); i <= 7; i++ {
		addr := common.Address{i}
		addrHash, _ := common.HashData(addr[:])
		k := common.Hash{i}
		keyHash, _ := common.HashData(k[:])
		key := dbutils.GenerateCompositeStorageKey(addrHash, 1, keyHash)
		val3 := common.BytesToHash([]byte("block 3 " + strconv.Itoa(int(i))))
		val5 := common.BytesToHash([]byte("block 5 " + strconv.Itoa(int(i))))
		val := common.BytesToHash([]byte("state   " + strconv.Itoa(int(i))))
		if i <= 2 {
			if err := blockWriter.WriteAccountStorage(ctx, addr, 1, &k, &val3, &val); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := blockWriter.WriteAccountStorage(ctx, addr, 1, &k, &val3, &val5); err != nil {
				t.Fatal(err)
			}
		}
		if err := block2Expected.Add(key, []byte("block 3 "+strconv.Itoa(int(i)))); err != nil {
			t.Fatal(err)
		}
	}
	tds.SetBlockNr(3)
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}
	blockWriter = tds.DbStateWriter()
	for i := uint8(3); i <= 7; i++ {
		addr := common.Address{i}
		addrHash, _ := common.HashData(addr[:])
		k := common.Hash{i}
		keyHash, _ := common.HashData(k[:])
		key := dbutils.GenerateCompositeStorageKey(addrHash, 1, keyHash)
		val5 := common.BytesToHash([]byte("block 5 " + strconv.Itoa(int(i))))
		val := common.BytesToHash([]byte("state   " + strconv.Itoa(int(i))))
		if i > 4 {
			if err := blockWriter.WriteAccountStorage(ctx, addr, 1, &k, &val5, &emptyVal); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := blockWriter.WriteAccountStorage(ctx, addr, 1, &k, &val5, &val); err != nil {
				t.Fatal(err)
			}
		}
		if err := block4Expected.Add(key, []byte("block 5 "+strconv.Itoa(int(i)))); err != nil {
			t.Fatal(err)
		}
	}
	tds.SetBlockNr(5)
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
		t.Fatal(err)
	}
	blockWriter = tds.DbStateWriter()
	for i := uint8(1); i < 5; i++ {
		addr := common.Address{i}
		addrHash, _ := common.HashData(addr[:])
		k := common.Hash{i}
		keyHash, _ := common.HashData(k[:])
		key := dbutils.GenerateCompositeStorageKey(addrHash, uint64(1), keyHash)
		val := []byte("state   " + strconv.Itoa(int(i)))
		err := block6Expected.Add(key, val)
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
	tds.SetBlockNr(6)
	if err := blockWriter.WriteChangeSets(); err != nil {
		t.Fatal(err)
	}
	if err := blockWriter.WriteHistory(); err != nil {
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

	//walk and collect walkAsOf result
	var err error
	var startKey [72]byte
	err = db.WalkAsOf(dbutils.CurrentStateBucket, dbutils.StorageHistoryBucket, startKey[:], 0, 2, func(k []byte, v []byte) (b bool, e error) {
		err = block2.Add(common.CopyBytes(k), common.CopyBytes(v))
		if err != nil {
			t.Fatal(err)
		}

		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = db.WalkAsOf(dbutils.CurrentStateBucket, dbutils.StorageHistoryBucket, startKey[:], 0, 4, func(k []byte, v []byte) (b bool, e error) {
		err = block4.Add(common.CopyBytes(k), common.CopyBytes(v))
		if err != nil {
			t.Fatal(err)
		}

		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = db.WalkAsOf(dbutils.CurrentStateBucket, dbutils.StorageHistoryBucket, startKey[:], 0, 6, func(k []byte, v []byte) (b bool, e error) {
		err = block6.Add(common.CopyBytes(k), common.CopyBytes(v))
		if err != nil {
			t.Fatal(err)
		}

		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Sort(block2Expected)
	if !reflect.DeepEqual(block2, block2Expected) {
		spew.Dump("expected", block2Expected)
		spew.Dump("current", block2)
		t.Fatal("block 2 result is incorrect")
	}
	sort.Sort(block4Expected)
	if !reflect.DeepEqual(block4, block4Expected) {
		spew.Dump("expected", block4Expected)
		spew.Dump("current", block4)
		t.Fatal("block 4 result is incorrect")
	}
	sort.Sort(block6Expected)
	if !reflect.DeepEqual(block6, block6Expected) {
		spew.Dump("expected", block6Expected)
		spew.Dump("current", block6)
		t.Fatal("block 6 result is incorrect")
	}
}
