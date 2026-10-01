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
	return ctx.WithValue(pairedCacheKey{}, factory)
}

func pairedFactory(ctx sdk.Context) PairedCacheFactory {
	factory, _ := ctx.Value(pairedCacheKey{}).(PairedCacheFactory)
	return factory
}

// CacheContext pairs an explicitly owned nested SDK cache, including the cache
// surrounding EVM hooks. The returned scope must be aborted on every exit.
func CacheContext(ctx sdk.Context) (sdk.Context, func(), PairedCache, error) {
	child, write := ctx.CacheContext()
	factory := pairedFactory(ctx)
	if factory == nil {
		return child, write, nil, nil
	}
	child, scope, err := factory(ctx, child)
	if err != nil {
		if scope != nil {
			AbortCache(scope)
		}
		return child, nil, nil, err
	}
	if scope == nil {
		panic(pairedCachePanic{Cause: "paired cache factory returned no scope"})
	}
	return child, func() {
		if err := scope.PrepareAdopt(); err != nil {
			panic(pairedCachePanic{Cause: err})
		}
		completeCache(write)
		completeCache(scope.Adopt)
	}, scope, nil
}

type pairedCachePanic struct{ Cause any }

func (pairedCachePanic) FatalExecution() {}

func completeCache(action func()) {
	defer func() {
		if cause := recover(); cause != nil {
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
