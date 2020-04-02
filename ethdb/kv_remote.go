package ethdb

import (
	"context"
	"fmt"

	"github.com/ledgerwatch/turbo-geth/ethdb/remote"
)

type remoteDB struct {
	opts   Options
	remote *remote.DB
}

func (db *remoteDB) Options() Options {
	return db.opts
}

// Close closes BoltKV
// All transactions must be closed before closing the database.
func (db *remoteDB) Close() error {
	return db.remote.Close()
}

func (db *remoteDB) Begin(ctx context.Context, writable bool) (Tx, error) {
	panic("remote db doesn't support managed transactions")
}

type remoteTx struct {
	ctx context.Context
	db  *remoteDB

	remote *remote.Tx
}

type remoteBucket struct {
	tx *remoteTx

	nameLen uint
	remote  *remote.Bucket
}

type remoteCursor struct {
	ctx    context.Context
	bucket remoteBucket

	remote *remote.Cursor

	k   []byte
	v   []byte
	err error
}

func (db *remoteDB) View(ctx context.Context, f func(tx Tx) error) (err error) {
	t := &remoteTx{db: db, ctx: ctx}
	return db.remote.View(ctx, func(tx *remote.Tx) error {
		t.remote = tx
		return f(t)
	})
}

func (db *remoteDB) Update(ctx context.Context, f func(tx Tx) error) (err error) {
	return fmt.Errorf("remote db provider doesn't support .Update method")
}

func (tx *remoteTx) Commit(ctx context.Context) error {
	panic("remote db is read-only")
}

func (tx *remoteTx) Rollback() error {
	panic("remote db is read-only")
}

func (tx *remoteTx) Bucket(name []byte) Bucket {
	b := remoteBucket{tx: tx, nameLen: uint(len(name))}
	b.remote = tx.remote.Bucket(name)
	return b
}

func (tx *remoteTx) cleanup() {
	// nothing to cleanup
}

func (c *remoteCursor) Prefix(v []byte) Cursor {
	c.remote = c.remote.Prefix(v)
	return c
}

func (c *remoteCursor) MatchBits(n uint) Cursor {
	panic("not implemented yet")
}

func (c *remoteCursor) Prefetch(v uint) Cursor {
	c.remote = c.remote.Prefetch(v)
	return c
}

func (c *remoteCursor) NoValues() NoValuesCursor {
	c.remote = c.remote.NoValues()
	return &remoteNoValuesCursor{remoteCursor: *c}
}

func (b remoteBucket) Get(key []byte) (val []byte, err error) {
	val, err = b.remote.Get(key)
	return val, err
}

func (b remoteBucket) Put(key []byte, value []byte) error {
	panic("not supported")
}

func (b remoteBucket) Delete(key []byte) error {
	panic("not supported")
}

func (b remoteBucket) Cursor() Cursor {
	c := &remoteCursor{bucket: b, ctx: b.tx.ctx, remote: b.remote.Cursor()}
	return c
}

func (c *remoteCursor) First() ([]byte, []byte, error) {
	c.k, c.v, c.err = c.remote.First()
	return c.k, c.v, c.err
}

func (c *remoteCursor) Seek(seek []byte) ([]byte, []byte, error) {
	c.k, c.v, c.err = c.remote.Seek(seek)
	return c.k, c.v, c.err
}

func (c *remoteCursor) Next() ([]byte, []byte, error) {
	c.k, c.v, c.err = c.remote.Next()
	return c.k, c.v, c.err
}

func (c *remoteCursor) Walk(walker func(k, v []byte) (bool, error)) error {
	for k, v, err := c.First(); k != nil || err != nil; k, v, err = c.Next() {
		if err != nil {
			return err
		}
		ok, err := walker(k, v)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return nil
}

type remoteNoValuesCursor struct {
	remoteCursor
}

func (c *remoteNoValuesCursor) Walk(walker func(k []byte, vSize uint32) (bool, error)) error {
	for k, vSize, err := c.First(); k != nil || err != nil; k, vSize, err = c.Next() {
		if err != nil {
			return err
		}
		ok, err := walker(k, vSize)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return nil
}

func (c *remoteNoValuesCursor) First() ([]byte, uint32, error) {
	var vSize uint32
	c.k, vSize, c.err = c.remote.FirstKey()
	return c.k, vSize, c.err
}

func (c *remoteNoValuesCursor) Seek(seek []byte) ([]byte, uint32, error) {
	var vSize uint32
	c.k, vSize, c.err = c.remote.SeekKey(seek)
	return c.k, vSize, c.err
}

func (c *remoteNoValuesCursor) Next() ([]byte, uint32, error) {
	var vSize uint32
	c.k, vSize, c.err = c.remote.NextKey()
	return c.k, vSize, c.err
}
