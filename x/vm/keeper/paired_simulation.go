package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
)

// SetSimulationCacheFactory pairs direct query and tracing owners with a
// disposable external view. Configure it once, before serving requests.
func (k *Keeper) SetSimulationCacheFactory(factory statedb.PairedCacheFactory) {
	k.simulationCacheFactory = factory
}

func (k Keeper) simulationCache(ctx sdk.Context) (sdk.Context, statedb.PairedCache, error) {
	child, _ := ctx.CacheContext()
	if k.simulationCacheFactory == nil {
		return child, nil, nil
	}
	return k.simulationCacheFactory(ctx.WithExecMode(sdk.ExecModeSimulate), child.WithExecMode(sdk.ExecModeSimulate))
}
