package ethdb

import (
	"encoding/binary"
	"fmt"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/log"
	"sort"
	"time"
)

type ChangesetWalker interface {
	Walk(func([]byte, []byte) error) error
}

func FindProperIndexChunk(db Database, bucket []byte, key []byte, blockNum uint64) (*dbutils.HistoryIndexBytes, error) {
	indexBytes, err := db.GetIndexChunk(bucket, key, blockNum)
	if err == ErrKeyNotFound {
		return dbutils.NewHistoryIndex(), nil
	}
	if err != nil {
		return nil, err
	}
	fmt.Println("FindProperIndexChunk", common.Bytes2Hex(key), indexBytes)
	return dbutils.WrapHistoryIndex(indexBytes), nil
}

func keyAndChunkID(key []byte) ([]byte, uint64) {
	if len(key) == common.HashLength+8 || len(key) == common.HashLength*2+common.IncarnationLength+8 {
		return key[:len(key)-8], ^binary.BigEndian.Uint64(key[len(key)-8:])
	}
	panic(common.Bytes2Hex(key))
}

const maxChunkSize = 1000

type IndexGenerator struct {
	db            Database
	csBucket      []byte
	bucketToWrite []byte
	fixedBits     uint
	csWalker      func([]byte) ChangesetWalker
	cache         map[string][]*dbutils.HistoryIndexBytes
}

func (ig *IndexGenerator) changeSetWalker(blockNum uint64) func([]byte, []byte) error {
	return func(k, v []byte) error {
		indexes, ok := ig.cache[string(k)]
		if !ok || len(indexes) == 0 {
			var index *dbutils.HistoryIndexBytes

			index, err := FindProperIndexChunk(ig.db, ig.bucketToWrite, k, blockNum)
			if err != nil {
				return err
			}
			if index == nil || len(*index)+8 > maxChunkSize {
				index = dbutils.NewHistoryIndex()
			}

			indexes = append(indexes, index)
			ig.cache[string(k)] = indexes
		}

		lastIndex := indexes[len(indexes)-1]
		if len(*lastIndex)+8 > maxChunkSize {
			lastIndex = dbutils.NewHistoryIndex()
			indexes = append(indexes, lastIndex)
			ig.cache[string(k)] = indexes
		}
		lastIndex.Append(blockNum)
		return nil
	}
}

/*
1) Ключ - адрес контрата/ключа + инвертированный номер первого блока в куске индекса
2) Поиск - Walk(bucket, contractAddress+blocknum, fixedBits=32/64, walker{
	в k будет нужный индекс, если он есть
	return false, nil
})
*/
func (ig *IndexGenerator) generateIndex() error {
	ts := time.Now()
	defer func() {
		fmt.Println("end:", time.Since(ts))
	}()
	batchSize := ig.db.IdealBatchSize() * 3
	//addrHash - > index or addhash + inverted firshBlock for full chunk contracts
	ig.cache = make(map[string][]*dbutils.HistoryIndexBytes, batchSize)

	commit := func() error {
		tuples := common.NewTuples(len(ig.cache), 3, 1)
		for key, vals := range ig.cache {
			for _, val := range vals {
				chunkKey, err := val.Key([]byte(key))
				if err != nil {
					return err
				}
				if err := tuples.Append(ig.bucketToWrite, chunkKey, *val); err != nil {
					return err
				}

			}
		}
		sort.Sort(tuples)
		_, err := ig.db.MultiPut(tuples.Values...)
		if err != nil {
			return err
		}
		ig.cache = make(map[string][]*dbutils.HistoryIndexBytes, batchSize)
		return nil
	}

	currentKey := []byte{}
	for {
		stop := true
		err := ig.db.Walk(ig.csBucket, currentKey, 0, func(k, v []byte) (b bool, e error) {
			blockNum, _ := dbutils.DecodeTimestamp(k)
			fmt.Println(blockNum, len(ig.cache), batchSize)
			currentKey = common.CopyBytes(k)
			err := ig.csWalker(v).Walk(ig.changeSetWalker(blockNum))
			if err != nil {
				return false, err
			}

			if len(ig.cache) > batchSize {
				stop = false
				return false, nil
			}

			return true, nil
		})
		if err != nil {
			return err
		}

		if len(ig.cache) > 0 {
			err = commit()
			if err != nil {
				return err
			}
		}
		if stop {
			break
		}

	}

	log.Info("Generation index finished", "bucket", string(ig.bucketToWrite))
	return nil
}
