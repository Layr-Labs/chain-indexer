package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	chainPoller "github.com/Layr-Labs/chain-indexer/pkg/chainPollers"
	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSuite defines a test suite that all storage implementations must pass
type TestSuite struct {
	NewStore func() (chainPoller.IChainPollerPersistence, error)
}

// Run executes all storage interface compliance tests
func (s *TestSuite) Run(t *testing.T) {
	t.Run("ChainPollingState", s.testChainPollingState)
	t.Run("Lifecycle", s.testLifecycle)
	t.Run("ConcurrentAccess", s.testConcurrentAccess)
}

func (s *TestSuite) testChainPollingState(t *testing.T) {
	store, err := s.NewStore()
	require.NoError(t, err)
	// nolint:errcheck
	defer store.Close()

	ctx := context.Background()
	chainId := config.ChainId(1)

	// Test getting non-existent block
	_, err = store.GetLastProcessedBlock(ctx, chainId)
	assert.ErrorIs(t, err, ErrNotFound)

	// Test setting and getting block
	blockNum := uint64(12345)
	blockRecord := &chainPoller.BlockRecord{
		Number:     blockNum,
		Hash:       "0xhash12345",
		ParentHash: "0xparent12344",
		Timestamp:  1234567890,
		ChainId:    chainId,
	}
	err = store.SaveBlock(ctx, blockRecord)
	require.NoError(t, err)

	retrieved, err := store.GetLastProcessedBlock(ctx, chainId)
	require.NoError(t, err)
	assert.Equal(t, blockNum, retrieved.Number)

	// Test updating block
	newBlockNum := uint64(12346)
	newBlockRecord := &chainPoller.BlockRecord{
		Number:     newBlockNum,
		Hash:       "0xhash12346",
		ParentHash: "0xhash12345",
		Timestamp:  1234567891,
		ChainId:    chainId,
	}
	err = store.SaveBlock(ctx, newBlockRecord)
	require.NoError(t, err)

	retrieved, err = store.GetLastProcessedBlock(ctx, chainId)
	require.NoError(t, err)
	assert.Equal(t, newBlockNum, retrieved.Number)

	// Test multiple chains for same AVS
	chainId2 := config.ChainId(2)
	blockNum2 := uint64(54321)
	blockRecord2 := &chainPoller.BlockRecord{
		Number:     blockNum2,
		Hash:       "0xhash54321",
		ParentHash: "0xparent54320",
		Timestamp:  1234567892,
		ChainId:    chainId2,
	}
	err = store.SaveBlock(ctx, blockRecord2)
	require.NoError(t, err)

	retrieved2, err := store.GetLastProcessedBlock(ctx, chainId2)
	require.NoError(t, err)
	assert.Equal(t, blockNum2, retrieved2.Number)

	// Ensure first chain is unchanged
	retrieved, err = store.GetLastProcessedBlock(ctx, chainId)
	require.NoError(t, err)
	assert.Equal(t, newBlockNum, retrieved.Number)

	blockNum3 := uint64(99999)
	blockRecord3 := &chainPoller.BlockRecord{
		Number:     blockNum3,
		Hash:       "0xhash99999",
		ParentHash: "0xparent99998",
		Timestamp:  1234567893,
		ChainId:    chainId,
	}
	err = store.SaveBlock(ctx, blockRecord3)
	require.NoError(t, err)

	retrieved3, err := store.GetLastProcessedBlock(ctx, chainId)
	require.NoError(t, err)
	assert.Equal(t, blockNum3, retrieved3.Number)

	// Ensure original AVS on same chain is unchanged
	retrieved, err = store.GetLastProcessedBlock(ctx, chainId)
	require.NoError(t, err)
	assert.Equal(t, newBlockNum, retrieved.Number)
}

func (s *TestSuite) testLifecycle(t *testing.T) {
	store, err := s.NewStore()
	require.NoError(t, err)

	ctx := context.Background()

	// Add some data
	blockRecord := &chainPoller.BlockRecord{
		Number:     12345,
		Hash:       "0xhash12345",
		ParentHash: "0xparent12344",
		Timestamp:  1234567890,
		ChainId:    config.ChainId(1),
	}
	err = store.SaveBlock(ctx, blockRecord)
	require.NoError(t, err)

	// nolint:errcheck
	err = store.Close()
	require.NoError(t, err)

	// Operations after close should fail
	newBlockRecord := &chainPoller.BlockRecord{
		Number:     12346,
		Hash:       "0xhash12346",
		ParentHash: "0xhash12345",
		Timestamp:  1234567891,
		ChainId:    config.ChainId(1),
	}
	err = store.SaveBlock(ctx, newBlockRecord)
	assert.ErrorIs(t, err, ErrStoreClosed)

	_, err = store.GetLastProcessedBlock(ctx, config.ChainId(1))
	assert.ErrorIs(t, err, ErrStoreClosed)
}

func (s *TestSuite) testConcurrentAccess(t *testing.T) {
	store, err := s.NewStore()
	require.NoError(t, err)

	// nolint:errcheck
	defer store.Close()

	ctx := context.Background()
	done := make(chan bool)
	errors := make(chan error, 10)

	// Concurrent writes to different chains
	for i := 0; i < 5; i++ {
		go func(chainId config.ChainId) {
			for j := 0; j < 10; j++ {
				blockRecord := &chainPoller.BlockRecord{
					Number:     uint64(j),
					Hash:       fmt.Sprintf("0xhash%d_%d", chainId, j),
					ParentHash: fmt.Sprintf("0xparent%d_%d", chainId, j-1),
					Timestamp:  uint64(1234567890 + j),
					ChainId:    chainId,
				}
				err := store.SaveBlock(ctx, blockRecord)
				if err != nil {
					errors <- err
					return
				}
			}
			done <- true
		}(config.ChainId(i))
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func(chainId config.ChainId) {
			for j := 0; j < 10; j++ {
				_, err := store.GetLastProcessedBlock(ctx, chainId)
				if err != nil && err != ErrNotFound {
					errors <- err
					return
				}
			}
			done <- true
		}(config.ChainId(i))
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case err := <-errors:
			t.Fatalf("Concurrent access error: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent operations")
		}
	}
}
