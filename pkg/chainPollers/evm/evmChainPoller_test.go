package EVMChainPoller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	chainPoller "github.com/Layr-Labs/chain-indexer/pkg/chainPollers"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence/memory"
	"github.com/Layr-Labs/chain-indexer/pkg/clients/ethereum"
	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/mocks"
	"github.com/ethereum/go-ethereum/signer/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

// Test helper to create a test poller
func createTestPoller(
	ethClient ethereum.Client,
	store chainPoller.IChainPollerPersistence,
	blockHandler chainPoller.IBlockHandler,
) *EVMChainPoller {
	return &EVMChainPoller{
		ethClient:    ethClient,
		store:        store,
		blockHandler: blockHandler,
		config: &EVMChainPollerConfig{
			AvsAddress:        "0xtest",
			ChainId:           config.ChainId(1),
			MaxReorgDepth:     10,
			ReorgCheckEnabled: true,
		},
		logger: zap.NewNop(),
	}
}

// Test Scenario 1: No reorg - all blocks match
func TestFindOrphanedBlocks_NoReorg_AllBlocksMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	// Setup: Save block 99 in store
	err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
		Number:     99,
		Hash:       "0x99",
		ParentHash: "0x98",
		ChainId:    config.ChainId(1),
	})
	require.NoError(t, err)

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0xstart"),
		ParentHash: ethereum.EthereumHexString("0x99"),
		ChainId:    config.ChainId(1),
	}

	// Mock chain returns matching block 99
	chainBlock99 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(99),
		Hash:       ethereum.EthereumHexString("0x99"),
		ParentHash: ethereum.EthereumHexString("0x98"),
		ChainId:    config.ChainId(1),
	}
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(chainBlock99, nil)

	poller := createTestPoller(mockClient, store, mockBlockHandler)

	orphaned, err := poller.findOrphanedBlocks(ctx, startBlock, 10)

	assert.NoError(t, err)
	assert.Empty(t, orphaned, "Should find no orphaned blocks when all blocks match")

	// Verify block was saved via SaveBlock
	lastProcessed, err := store.GetLastProcessedBlock(ctx, config.ChainId(1))
	require.NoError(t, err)
	assert.Equal(t, uint64(99), lastProcessed.Number)
}

// Test Scenario 2: Simple reorg - finds orphaned blocks and common ancestor
func TestFindOrphanedBlocks_SimpleReorg_FindsOrphanedAndAncestor(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	// Setup: Save blocks 97-99 in store (old chain)
	for i := uint64(97); i <= 99; i++ {
		err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
			Number:     i,
			Hash:       fmt.Sprintf("0x%d_old", i),
			ParentHash: fmt.Sprintf("0x%d_old", i-1),
			ChainId:    config.ChainId(1),
		})
		require.NoError(t, err)
	}

	// Also save block 97 with the correct hash that will match
	err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
		Number:     97,
		Hash:       "0x97",
		ParentHash: "0x96",
		ChainId:    config.ChainId(1),
	})
	require.NoError(t, err)

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0xstart"),
		ParentHash: ethereum.EthereumHexString("0x99_new"),
		ChainId:    config.ChainId(1),
	}

	// Mock chain blocks
	chainBlock99 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(99),
		Hash:       ethereum.EthereumHexString("0x99_new"),
		ParentHash: ethereum.EthereumHexString("0x98_new"),
		ChainId:    config.ChainId(1),
	}
	chainBlock98 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(98),
		Hash:       ethereum.EthereumHexString("0x98_new"),
		ParentHash: ethereum.EthereumHexString("0x97"),
		ChainId:    config.ChainId(1),
	}
	chainBlock97 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(97),
		Hash:       ethereum.EthereumHexString("0x97"),
		ParentHash: ethereum.EthereumHexString("0x96"),
		ChainId:    config.ChainId(1),
	}

	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(chainBlock99, nil)
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(98)).Return(chainBlock98, nil)
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(97)).Return(chainBlock97, nil)

	poller := createTestPoller(mockClient, store, mockBlockHandler)

	orphaned, err := poller.findOrphanedBlocks(ctx, startBlock, 10)

	assert.NoError(t, err)
	assert.Len(t, orphaned, 2, "Should find 2 orphaned blocks")
	assert.Equal(t, uint64(99), orphaned[0].Number)
	assert.Equal(t, uint64(98), orphaned[1].Number)

	// Verify block was saved via SaveBlock
	lastProcessed, err := store.GetLastProcessedBlock(ctx, config.ChainId(1))
	require.NoError(t, err)
	assert.Equal(t, uint64(97), lastProcessed.Number)
}

// Test Scenario 3: Deep reorg that hits maxDepth limit
func TestFindOrphanedBlocks_DeepReorg_HitsMaxDepth(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	// Setup: Save blocks 97-99 in store (all with old hashes)
	for i := uint64(97); i <= 99; i++ {
		err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
			Number:     i,
			Hash:       fmt.Sprintf("0x%d_old", i),
			ParentHash: fmt.Sprintf("0x%d_old", i-1),
			ChainId:    config.ChainId(1),
		})
		require.NoError(t, err)
	}

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0xstart"),
		ParentHash: ethereum.EthereumHexString("0x99_new"),
		ChainId:    config.ChainId(1),
	}

	maxDepth := 3

	// Mock chain blocks - all different from stored
	for i := uint64(99); i >= 97; i-- {
		chainBlock := &ethereum.EthereumBlock{
			Number:     ethereum.EthereumQuantity(i),
			Hash:       ethereum.EthereumHexString(fmt.Sprintf("0x%d_new", i)),
			ParentHash: ethereum.EthereumHexString(fmt.Sprintf("0x%d_new", i-1)),
			ChainId:    config.ChainId(1),
		}
		mockClient.EXPECT().GetBlockByNumber(ctx, i).Return(chainBlock, nil)
	}

	poller := createTestPoller(mockClient, store, mockBlockHandler)

	orphaned, err := poller.findOrphanedBlocks(ctx, startBlock, maxDepth)

	assert.NoError(t, err)
	assert.Len(t, orphaned, 3, "Should find orphaned blocks up to maxDepth")
	assert.Equal(t, uint64(99), orphaned[0].Number)
	assert.Equal(t, uint64(98), orphaned[1].Number)
	assert.Equal(t, uint64(97), orphaned[2].Number)
}

// Test Scenario 4: Block not found in storage (returns early)
func TestFindOrphanedBlocks_BlockNotInStorage_ReturnsEarly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	// Don't save block 99 in store - it will be missing

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0xstart"),
		ParentHash: ethereum.EthereumHexString("0x99"),
		Timestamp:  ethereum.EthereumQuantity(1234567890),
		ChainId:    config.ChainId(1),
	}

	chainBlock99 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(99),
		Hash:       ethereum.EthereumHexString("0x99"),
		ParentHash: ethereum.EthereumHexString("0x98"),
		ChainId:    config.ChainId(1),
	}

	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(chainBlock99, nil)

	poller := createTestPoller(mockClient, store, mockBlockHandler)

	orphaned, err := poller.findOrphanedBlocks(ctx, startBlock, 10)

	assert.NoError(t, err)
	assert.Empty(t, orphaned, "Should return empty orphaned list when block not found")

	// Verify chainBlock99 info was saved when block not found in storage
	savedBlock, err := store.GetBlock(ctx, config.ChainId(1), 99)
	require.NoError(t, err)
	assert.Equal(t, "0x99", savedBlock.Hash)
	assert.Equal(t, "0x98", savedBlock.ParentHash)
}

// Test Scenario 5: Chain fetch fails
func TestFindOrphanedBlocks_ChainFetchFails_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0xstart"),
		ParentHash: ethereum.EthereumHexString("0x99"),
		ChainId:    config.ChainId(1),
	}

	expectedErr := errors.New("network error")
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(nil, expectedErr)

	poller := createTestPoller(mockClient, store, mockBlockHandler)

	orphaned, err := poller.findOrphanedBlocks(ctx, startBlock, 10)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch block 99 from chain")
	assert.Nil(t, orphaned)
}

// Test state changes
func TestFindOrphanedBlocks_StateChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	// Setup: Save block 99 in store
	err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
		Number:     99,
		Hash:       "0x99",
		ParentHash: "0x98",
		ChainId:    config.ChainId(1),
	})
	require.NoError(t, err)

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0xstart"),
		ParentHash: ethereum.EthereumHexString("0x99"),
		ChainId:    config.ChainId(1),
	}

	chainBlock99 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(99),
		Hash:       ethereum.EthereumHexString("0x99"),
		ParentHash: ethereum.EthereumHexString("0x98"),
		ChainId:    config.ChainId(1),
	}

	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(chainBlock99, nil)

	poller := createTestPoller(mockClient, store, mockBlockHandler)

	orphaned, err := poller.findOrphanedBlocks(ctx, startBlock, 10)

	require.NoError(t, err)
	assert.Empty(t, orphaned)

	// Verify block was saved to storage
	savedBlock, err := store.GetBlock(ctx, config.ChainId(1), 99)
	require.NoError(t, err)
	assert.Equal(t, "0x99", savedBlock.Hash)
	assert.Equal(t, "0x98", savedBlock.ParentHash)
}

// Test reconcileReorg successfully deletes orphaned blocks
func TestReconcileReorg_Success_DeletesOrphanedBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockChainPollerPersistence := mocks.NewMockIChainPollerPersistence(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	// Setup: Save blocks 98-99 in store (will be orphaned)
	for i := uint64(98); i <= 99; i++ {
		err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
			Number:     i,
			Hash:       fmt.Sprintf("0x%d_old", i),
			ParentHash: fmt.Sprintf("0x%d_old", i-1),
			ChainId:    config.ChainId(1),
		})
		require.NoError(t, err)
	}

	// Also save block 97 that will match (common ancestor)
	err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
		Number:     97,
		Hash:       "0x97",
		ParentHash: "0x96",
		ChainId:    config.ChainId(1),
	})
	require.NoError(t, err)

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0x100_new"),
		ParentHash: ethereum.EthereumHexString("0x99_new"),
		ChainId:    config.ChainId(1),
	}

	// Mock chain blocks - different from stored until block 97
	chainBlock99 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(99),
		Hash:       ethereum.EthereumHexString("0x99_new"),
		ParentHash: ethereum.EthereumHexString("0x98_new"),
		ChainId:    config.ChainId(1),
	}
	chainBlock98 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(98),
		Hash:       ethereum.EthereumHexString("0x98_new"),
		ParentHash: ethereum.EthereumHexString("0x97"),
		ChainId:    config.ChainId(1),
	}
	chainBlock97 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(97),
		Hash:       ethereum.EthereumHexString("0x97"),
		ParentHash: ethereum.EthereumHexString("0x96"),
		ChainId:    config.ChainId(1),
	}

	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(chainBlock99, nil)
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(98)).Return(chainBlock98, nil)
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(97)).Return(chainBlock97, nil)

	// Expect CancelBlock to be called for each orphaned block
	mockChainPollerPersistence.EXPECT().DeleteBlock(ctx, config.ChainId(1), uint64(99))
	mockChainPollerPersistence.EXPECT().DeleteBlock(ctx, config.ChainId(1), uint64(98))

	mockBlockHandler.EXPECT().HandleReorgBlock(ctx, chainBlock98)

	poller := &EVMChainPoller{
		ethClient:    mockClient,
		store:        store,
		blockHandler: mockBlockHandler,
		config: &EVMChainPollerConfig{
			AvsAddress:    "0xtest",
			ChainId:       config.ChainId(1),
			MaxReorgDepth: 10,
		},
		logger: zap.NewNop(),
	}

	// Execute reconcileReorg
	err = poller.reconcileReorg(ctx, startBlock)

	// Verify no error
	assert.NoError(t, err)

	// Verify orphaned blocks were deleted from storage
	_, err = store.GetBlock(ctx, config.ChainId(1), 99)
	assert.Error(t, err, "Block 99 should be deleted")
	assert.True(t, errors.Is(err, storage.ErrNotFound))

	_, err = store.GetBlock(ctx, config.ChainId(1), 98)
	assert.Error(t, err, "Block 98 should be deleted")
	assert.True(t, errors.Is(err, storage.ErrNotFound))

	// Verify common ancestor block 97 still exists
	block97, err := store.GetBlock(ctx, config.ChainId(1), 97)
	assert.NoError(t, err, "Block 97 should still exist")
	assert.Equal(t, "0x97", block97.Hash)
}

// Test reconcileReorg returns error when no orphaned blocks are found
func TestReconcileReorg_NoOrphanedBlocks_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	// Setup: Save block 99 that matches chain (no reorg)
	err := store.SaveBlock(ctx, &chainPoller.BlockRecord{
		Number:     99,
		Hash:       "0x99",
		ParentHash: "0x98",
		ChainId:    config.ChainId(1),
	})
	require.NoError(t, err)

	startBlock := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(100),
		Hash:       ethereum.EthereumHexString("0x100"),
		ParentHash: ethereum.EthereumHexString("0x99"),
		ChainId:    config.ChainId(1),
	}

	// Mock chain returns matching block (no reorg)
	chainBlock99 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(99),
		Hash:       ethereum.EthereumHexString("0x99"),
		ParentHash: ethereum.EthereumHexString("0x98"),
		ChainId:    config.ChainId(1),
	}
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(chainBlock99, nil)

	poller := &EVMChainPoller{
		ethClient:    mockClient,
		store:        store,
		blockHandler: mockBlockHandler,
		config: &EVMChainPollerConfig{
			AvsAddress:    "0xtest",
			ChainId:       config.ChainId(1),
			MaxReorgDepth: 10,
		},
		logger: zap.NewNop(),
	}

	// Execute reconcileReorg
	err = poller.reconcileReorg(ctx, startBlock)

	// Should return error when no orphaned blocks found
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no orphaned blocks found")
}
