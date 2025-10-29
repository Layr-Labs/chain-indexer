package memory_test

import (
	"testing"

	chainPoller "github.com/Layr-Labs/chain-indexer/pkg/chainPollers"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence/memory"
)

// Test_InMemoryChainPollerPersistence runs the standard storage test suite
func Test_InMemoryChainPollerPersistence(t *testing.T) {
	suite := &persistence.TestSuite{
		NewStore: func() (chainPoller.IChainPollerPersistence, error) {
			return memory.NewInMemoryChainPollerPersistence(), nil
		},
	}
	suite.Run(t)
}

// TestInMemorySpecific tests in-memory specific behavior
func TestInMemorySpecific(t *testing.T) {
	t.Run("MultipleInstances", func(t *testing.T) {
		// Test that multiple instances don't share state
		store1 := memory.NewInMemoryChainPollerPersistence()
		store2 := memory.NewInMemoryChainPollerPersistence()

		// Both should have independent state
		if store1 == store2 {
			t.Fatal("NewInMemoryChainPollerPersistence should create independent instances")
		}
	})
}
