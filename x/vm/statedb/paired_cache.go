package statedb

import sdk "github.com/cosmos/cosmos-sdk/types"

// PairedCache owns external effects corresponding to a disposable SDK cache.
// Savepoints preserve observations and resource charges when writes revert.
// Revert must invalidate later savepoints, and Abort is idempotent after Adopt.
type PairedCache interface {
	Snapshot() uint64
	Revert(uint64)
	PrepareAdopt() error
	Adopt()
	Abort()
}

type PairedCacheFactory func(parent, child sdk.Context) (sdk.Context, PairedCache, error)
type pairedCacheKey struct{}

func WithPairedCacheFactory(ctx sdk.Context, factory PairedCacheFactory) sdk.Context {
	ctx = ctx.WithValue(pairedCacheKey{}, factory)
	return sdk.WithCacheScopeFactory(ctx, func(parent, child sdk.Context) (sdk.Context, sdk.CacheScope, error) {
		return factory(parent, child)
	})
}

func pairedFactory(ctx sdk.Context) PairedCacheFactory {
	factory, _ := ctx.Value(pairedCacheKey{}).(PairedCacheFactory)
	return factory
}

// CacheContext pairs an explicitly owned nested SDK cache, including the cache
// surrounding EVM hooks. The returned scope must be aborted on every exit.
func CacheContext(ctx sdk.Context) (sdk.Context, func(), PairedCache, error) {
	child, write, owner, err := sdk.CacheContextWithScope(ctx)
	if err != nil || owner == nil {
		return child, write, nil, err
	}
	paired, ok := owner.(PairedCache)
	if !ok {
		panic(pairedCachePanic{Cause: "SDK cache owner lacks EVM savepoints"})
	}
	return child, write, paired, nil
}

type pairedCachePanic struct{ Cause any }

func (pairedCachePanic) FatalExecution() {}

func completeCache(action func()) {
	defer func() {
		if cause := recover(); cause != nil {
			if limit, ok := cause.(interface{ ProtocolLimit() bool }); ok && limit.ProtocolLimit() {
				panic(cause)
			}
			panic(pairedCachePanic{Cause: cause})
		}
	}()
	action()
}

func AbortCache(cache PairedCache) {
	if cache != nil {
		completeCache(cache.Abort)
	}
}

// Abort discards unadopted external effects. Every StateDB owner must defer it,
// including simulation, tracing, and query owners that never call Commit.
func (s *StateDB) Abort() { AbortCache(s.paired) }
