package statedb_test

import (
	"errors"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/cosmos/evm/x/vm/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/stretchr/testify/require"
)

type pairedFixtureScope struct {
	value, observations int
	committed           *int
	next                uint64
	points              map[uint64]int
	closed              bool
}

func (s *pairedFixtureScope) Snapshot() uint64 {
	id := s.next
	s.next++
	s.points[id] = s.value
	return id
}
func (s *pairedFixtureScope) Revert(id uint64) {
	value, ok := s.points[id]
	if !ok {
		panic("invalidated native savepoint")
	}
	s.value = value
	for point := range s.points {
		if point > id {
			delete(s.points, point)
		}
	}
}
func (s *pairedFixtureScope) PrepareAdopt() error { return nil }
func (s *pairedFixtureScope) Adopt()              { *s.committed = s.value; s.closed = true }
func (s *pairedFixtureScope) Abort()              { s.closed = true }

type pairedFixtureKeeper struct {
	*mocks.EVMKeeper
	key       *storetypes.KVStoreKey
	transient *storetypes.TransientStoreKey
	fail      bool
}

func (k pairedFixtureKeeper) KVStoreKeys() map[string]*storetypes.KVStoreKey {
	return map[string]*storetypes.KVStoreKey{"paired": k.key}
}
func (k pairedFixtureKeeper) TransientStoreKeys() map[string]*storetypes.TransientStoreKey {
	return map[string]*storetypes.TransientStoreKey{"paired-transient": k.transient}
}
func (k pairedFixtureKeeper) SetAccount(ctx sdk.Context, _ common.Address, _ statedb.Account) error {
	ctx.KVStore(k.key).Set([]byte("keeper-write"), []byte("account"))
	if k.fail {
		return errors.New("keeper failed after cached write")
	}
	return nil
}

func pairedFixture(t *testing.T) (sdk.Context, pairedFixtureKeeper, *pairedFixtureScope) {
	t.Helper()
	key := storetypes.NewKVStoreKey("paired")
	transient := storetypes.NewTransientStoreKey("paired-transient")
	store := rootmulti.NewStore(dbm.NewMemDB(), log.NewNopLogger(), metrics.NewNoOpMetrics())
	store.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
	store.MountStoreWithDB(transient, storetypes.StoreTypeTransient, nil)
	require.NoError(t, store.LoadLatestVersion())
	committed := 0
	scope := &pairedFixtureScope{committed: &committed, points: make(map[uint64]int)}
	ctx := sdk.NewContext(store, tmproto.Header{}, false, log.NewNopLogger())
	ctx = statedb.WithPairedCacheFactory(ctx, func(parent, child sdk.Context) (sdk.Context, statedb.PairedCache, error) {
		return child, scope, nil
	})
	return ctx, pairedFixtureKeeper{mocks.NewEVMKeeper(), key, transient, false}, scope
}

func TestPairedCacheSnapshots(t *testing.T) {
	ctx, keeper, native := pairedFixture(t)
	db := statedb.New(ctx, keeper, statedb.NewEmptyTxConfig())
	defer db.Abort()
	outer := db.Snapshot() // before lazy native binding
	child, err := db.GetCacheContext()
	require.NoError(t, err)
	mutate := func() {
		snapshot := db.MultiStoreSnapshot()
		require.NoError(t, db.AddPrecompileFn(snapshot))
		native.value++
		native.observations++
		child.KVStore(keeper.key).Set([]byte("precompile"), []byte{byte(native.value)})
		child.TransientStore(keeper.transient).Set([]byte("precompile"), []byte{byte(native.value)})
	}
	mutate()
	inner := db.Snapshot()
	mutate()
	db.RevertToSnapshot(inner)
	require.Equal(t, 1, native.value)
	require.Equal(t, []byte{1}, child.KVStore(keeper.key).Get([]byte("precompile")))
	require.Equal(t, []byte{1}, child.TransientStore(keeper.transient).Get([]byte("precompile")))
	db.RevertToSnapshot(outer)
	require.Zero(t, native.value)
	require.Equal(t, 2, native.observations)
	require.Nil(t, child.KVStore(keeper.key).Get([]byte("precompile")))
	require.Nil(t, child.TransientStore(keeper.transient).Get([]byte("precompile")))
	point := db.MultiStoreSnapshot()
	native.value = 9
	db.RevertMultiStore(point)
	require.Zero(t, native.value)
}

func TestPairedCacheCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		ctx, keeper, native := pairedFixture(t)
		keeper.fail = fail
		db := statedb.New(ctx, keeper, statedb.NewEmptyTxConfig())
		child, err := db.GetCacheContext()
		require.NoError(t, err)
		child.KVStore(keeper.key).Set([]byte("precompile"), []byte("native"))
		native.value = 1
		db.SetNonce(common.Address{1}, 1, tracing.NonceChangeUnspecified)
		err = db.Commit()
		db.Abort()
		require.Equal(t, fail, err != nil)
		require.True(t, native.closed)
		if fail {
			require.Zero(t, *native.committed)
			require.Nil(t, ctx.KVStore(keeper.key).Get([]byte("precompile")))
			require.Nil(t, ctx.KVStore(keeper.key).Get([]byte("keeper-write")))
		} else {
			require.Equal(t, 1, *native.committed)
			require.Equal(t, []byte("native"), ctx.KVStore(keeper.key).Get([]byte("precompile")))
			require.Equal(t, []byte("account"), ctx.KVStore(keeper.key).Get([]byte("keeper-write")))
		}
	}
}
