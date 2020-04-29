package downloader

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/ledgerwatch/turbo-geth/log"
	"github.com/ledgerwatch/turbo-geth/rlp"
)

var profileNumberBodies uint64

func (d *Downloader) spawnBodyDownloadStage(id string) (bool, error) {
	// Create cancel channel for aborting mid-flight and mark the master peer
	d.cancelLock.Lock()
	d.cancelCh = make(chan struct{})
	d.cancelPeer = id
	d.cancelLock.Unlock()

	defer d.Cancel() // No matter what, we can't leave the cancel channel open
	// Figure out how many blocks have already been downloaded
	origin, err := GetStageProgress(d.stateDB, Bodies)
	if err != nil {
		return false, fmt.Errorf("getting Bodies stage progress: %v", err)
	}
	// Check invalidation (caused by reorgs of header chain)
	var invalidation uint64
	invalidation, err = GetStageInvalidation(d.stateDB, Bodies)
	if err != nil {
		return false, fmt.Errorf("getting Bodies stage invalidation: %v", err)
	}
	if invalidation != 0 {
		batch := d.stateDB.NewBatch()
		if invalidation < origin {
			// Rollback the progress
			if err = SaveStageProgress(batch, Bodies, invalidation); err != nil {
				return false, fmt.Errorf("rolling back Bodies stage progress: %v", err)
			}
			// In case of downloading bodies, we simply re-download the new branch of bodies
			log.Warn("Rolling back bodies download", "from", origin, "to", invalidation)
			origin = invalidation
		}
		// push invalidation onto further stages
		if err = SaveStageInvalidation(batch, Bodies, 0); err != nil {
			return false, fmt.Errorf("removing Bodies stage invalidation: %v", err)
		}
		if Bodies+1 < Finish {
			var postInvalidation uint64
			if postInvalidation, err = GetStageInvalidation(batch, Bodies+1); err != nil {
				return false, fmt.Errorf("getting post-Bodies stage invalidation: %v", err)
			}
			if postInvalidation == 0 || invalidation < postInvalidation {
				if err = SaveStageInvalidation(batch, Bodies+1, invalidation); err != nil {
					return false, fmt.Errorf("pushing post-Bodies stage invalidation: %v", err)
				}
			}
		}
		if _, err = batch.Commit(); err != nil {
			return false, fmt.Errorf("committing rollback and push invalidation post-Bodies: %v", err)
		}
	}
	// Figure out how many headers we have
	currentNumber := origin + 1
	if profileNumberBodies == 0 {
		profileNumberBodies = currentNumber
		f, err := os.Create(fmt.Sprintf("cpubodies-%d.prof", profileNumberBodies))
		if err != nil {
			log.Error("could not create CPU profile", "error", err)
		}
		if err1 := pprof.StartCPUProfile(f); err1 != nil {
			log.Error("could not start CPU profile", "error", err1)
		}
	}
	var missingHeader uint64
	// Go over canonical headers and insert them into the queue
	const N = 65536
	var hashes [N]common.Hash                         // Canonical hashes of the blocks
	var headers = make(map[common.Hash]*types.Header) // We use map because there might be more than one header by block number
	var hashCount = 0
	err = d.stateDB.Walk(dbutils.HeaderPrefix, dbutils.EncodeBlockNumber(currentNumber), 0, func(k, v []byte) (bool, error) {
		// Skip non relevant records
		if len(k) == 8+len(dbutils.HeaderHashSuffix) && bytes.Equal(k[8:], dbutils.HeaderHashSuffix) {
			// This is how we learn about canonical chain
			blockNumber := binary.BigEndian.Uint64(k[:8])
			if blockNumber != currentNumber {
				log.Warn("Canonical hash is missing", "number", currentNumber, "got", blockNumber)
				missingHeader = currentNumber
				return false, nil
			}
			currentNumber++
			if currentNumber-profileNumberBodies == 1000000 {
				// Flush the profiler
				pprof.StopCPUProfile()
			}
			if hashCount < len(hashes) {
				copy(hashes[hashCount][:], v)
			}
			hashCount++
			if hashCount > len(hashes) { // We allow hashCount to go +1 over what it should be, to let headers to be read
				return false, nil
			}
			return true, nil
		}
		if len(k) != 8+common.HashLength {
			return true, nil
		}
		header := new(types.Header)
		if err1 := rlp.Decode(bytes.NewReader(v), header); err1 != nil {
			log.Error("Invalid block header RLP", "hash", k[8:], "err", err1)
			return false, err1
		}
		headers[common.BytesToHash(k[8:])] = header
		return true, nil
	})
	if err != nil {
		return false, fmt.Errorf("walking over canonical hashes: %v", err)
	}
	if missingHeader != 0 {
		if err1 := SaveStageProgress(d.stateDB, Headers, missingHeader); err1 != nil {
			return false, fmt.Errorf("resetting SyncStage Headers to missing header: %v", err1)
		}
		// This will cause the sync return to the header stage
		return false, nil
	}
	d.queue.Reset()
	if hashCount <= 1 {
		// No more bodies to download
		return false, nil
	}
	from := origin + 1
	d.queue.Prepare(from, d.mode)
	d.queue.ScheduleBodies(from, hashes[:hashCount-1], headers)
	to := from + uint64(hashCount-1)
	select {
	case d.bodyWakeCh <- true:
	case <-d.cancelCh:
	}
	// Now fetch all the bodies
	fetchers := []func() error{
		func() error { return d.fetchBodies(from) },
		func() error { return d.processBodiesStage(to) },
	}
	return true, d.spawnSync(fetchers)
}

// processBodiesStage takes fetch results from the queue and imports them into the chain.
// it doesn't execute blocks
func (d *Downloader) processBodiesStage(to uint64) error {
	for {
		results := d.queue.Results(true)
		if len(results) == 0 {
			return nil
		}
		lastNumber, err := d.importBlockResults(results, false /* execute */)
		if err != nil {
			return err
		}
		if lastNumber == to {
			select {
			case d.bodyWakeCh <- false:
			case <-d.cancelCh:
			}
			return nil
		}
	}
}
