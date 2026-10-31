package statedb

import (
	"context"
	"reflect"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/types"
)

type frameKey struct{}
type testFrame struct {
	id     int
	closed bool
	events *[]int
}

func (f *testFrame) PrepareAdopt() error {
	f.closed = true
	*f.events = append(*f.events, f.id)
	return nil
}
func (f *testFrame) Adopt() {}
func (f *testFrame) Abort() {
	if !f.closed {
		f.closed = true
		*f.events = append(*f.events, -f.id)
	}
}

func TestExecutionFramesRestoreParentContextAndCloseImmediately(t *testing.T) {
	var events []int
	next := 0
	ctx := sdk.Context{}.WithContext(context.Background())
	ctx = types.WithExecutionScopeHook(ctx, func(parent sdk.Context) (sdk.Context, types.ExecutionScope, error) {
		next++
		return parent.WithValue(frameKey{}, next), &testFrame{id: next, events: &events}, nil
	})
	db := &StateDB{ctx: ctx, cacheCtx: ctx, writeCache: func() {}}
	db.BeginExecutionFrame()
	for i := 0; i < 100; i++ {
		db.BeginExecutionFrame()
		child, err := db.GetCacheContext()
		if err != nil {
			t.Fatal(err)
		}
		if child.Value(frameKey{}) != i+2 {
			t.Fatal("precompile did not receive active frame context")
		}
		db.EndExecutionFrame(i%2 == 0)
		parent, err := db.GetCacheContext()
		if err != nil {
			t.Fatal(err)
		}
		if parent.Value(frameKey{}) != 1 {
			t.Fatal("parent context not restored")
		}
		if len(db.executionFrames) != 1 {
			t.Fatal("completed siblings remain active")
		}
	}
	db.EndExecutionFrame(true)
	if !reflect.DeepEqual(events[:4], []int{-2, 3, -4, 5}) || events[len(events)-1] != -1 {
		t.Fatalf("wrong frame dispositions: %v", events)
	}
	if len(db.executionFrames) != 0 {
		t.Fatal("root frame remains active")
	}
}
