package EVMChainPoller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chainPoller "github.com/Layr-Labs/chain-indexer/pkg/chainPollers"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/contractRegistry"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence/memory"
	"github.com/Layr-Labs/chain-indexer/pkg/clients/ethereum"
	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func createTestPoller(
	ethClient ethereum.Client,
	store chainPoller.IChainPollerPersistence,
	blockHandler chainPoller.IBlockHandler,
	opts ...EVMChainPollerOption,
) *EVMChainPoller {
	ecp := &EVMChainPoller{
		ethClient:    ethClient,
		store:        store,
		blockHandler: blockHandler,
		config: &EVMChainPollerConfig{
			AvsAddress:                 "0xtest",
			ChainId:                    config.ChainId(1),
			MaxReorgDepth:              10,
			ReorgCheckEnabled:          true,
			MaxAddressesPerLogsRequest: DefaultMaxAddressesPerLogsRequest,
			LogsFetchTimeout:           DefaultLogsFetchTimeout,
		},
		logger: zap.NewNop(),
	}
	for _, opt := range opts {
		opt(ecp)
	}
	return ecp
}

func createTestPollerWithContracts(
	ethClient ethereum.Client,
	store chainPoller.IChainPollerPersistence,
	blockHandler chainPoller.IBlockHandler,
	staticContracts []string,
	maxAddrsPerReq int,
	opts ...EVMChainPollerOption,
) *EVMChainPoller {
	if maxAddrsPerReq == 0 {
		maxAddrsPerReq = DefaultMaxAddressesPerLogsRequest
	}
	ecp := &EVMChainPoller{
		ethClient:    ethClient,
		store:        store,
		blockHandler: blockHandler,
		config: &EVMChainPollerConfig{
			AvsAddress:                 "0xtest",
			ChainId:                    config.ChainId(1),
			MaxReorgDepth:              10,
			ReorgCheckEnabled:          true,
			InterestingContracts:       staticContracts,
			MaxAddressesPerLogsRequest: maxAddrsPerReq,
			LogsFetchTimeout:           10 * time.Second,
		},
		logger: zap.NewNop(),
	}
	for _, opt := range opts {
		opt(ecp)
	}
	return ecp
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

	// Expect HandleReorgBlock to be called for each orphaned block
	mockBlockHandler.EXPECT().HandleReorgBlock(ctx, uint64(99))
	mockBlockHandler.EXPECT().HandleReorgBlock(ctx, uint64(98))

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
	assert.True(t, errors.Is(err, persistence.ErrNotFound))

	_, err = store.GetBlock(ctx, config.ChainId(1), 98)
	assert.Error(t, err, "Block 98 should be deleted")
	assert.True(t, errors.Is(err, persistence.ErrNotFound))

	// Verify common ancestor block 97 still exists
	block97, err := store.GetBlock(ctx, config.ChainId(1), 97)
	assert.NoError(t, err, "Block 97 should still exist")
	assert.Equal(t, "0x97", block97.Hash)
}

func TestReconcileReorg_NoOrphanedBlocks_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

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

	chainBlock99 := &ethereum.EthereumBlock{
		Number:     ethereum.EthereumQuantity(99),
		Hash:       ethereum.EthereumHexString("0x99"),
		ParentHash: ethereum.EthereumHexString("0x98"),
		ChainId:    config.ChainId(1),
	}
	mockClient.EXPECT().GetBlockByNumber(ctx, uint64(99)).Return(chainBlock99, nil)

	poller := createTestPoller(mockClient, store, mockBlockHandler)

	err = poller.reconcileReorg(ctx, startBlock)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no orphaned blocks found")
}

func TestRegisterContracts_AddsToList(t *testing.T) {
	registry := contractRegistry.NewInMemoryContractRegistry()
	registry.RegisterContracts([]string{"0xAAA", "0xBBB", "0xCCC"})

	got := registry.ListContracts()
	sort.Strings(got)
	assert.Equal(t, []string{"0xaaa", "0xbbb", "0xccc"}, got)
}

func TestRegisterContracts_Deduplication(t *testing.T) {
	registry := contractRegistry.NewInMemoryContractRegistry()
	registry.RegisterContracts([]string{"0xAAA", "0xaaa"})

	got := registry.ListContracts()
	assert.Len(t, got, 1)
	assert.Equal(t, "0xaaa", got[0])
}

func TestUnregisterContracts_RemovesFromList(t *testing.T) {
	registry := contractRegistry.NewInMemoryContractRegistry()
	registry.RegisterContracts([]string{"0xAAA", "0xBBB", "0xCCC"})
	registry.UnregisterContracts([]string{"0xbbb"})

	got := registry.ListContracts()
	sort.Strings(got)
	assert.Equal(t, []string{"0xaaa", "0xccc"}, got)
}

func TestUnregisterContracts_NonExistent_NoError(t *testing.T) {
	registry := contractRegistry.NewInMemoryContractRegistry()
	registry.RegisterContracts([]string{"0xAAA"})
	registry.UnregisterContracts([]string{"0xZZZ"})

	got := registry.ListContracts()
	assert.Len(t, got, 1)
	assert.Equal(t, "0xaaa", got[0])
}

func TestListAllInterestingContracts_MergesStaticAndDynamic(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	store := memory.NewInMemoryChainPollerPersistence()
	registry := contractRegistry.NewInMemoryContractRegistry()
	poller := createTestPollerWithContracts(mockClient, store, nil, []string{"0xSTATIC"}, 0, WithContractRegistry(registry))

	registry.RegisterContracts([]string{"0xDYNAMIC"})

	got := poller.listAllInterestingContracts()
	sort.Strings(got)
	assert.Equal(t, []string{"0xdynamic", "0xstatic"}, got)
}

func TestListAllInterestingContracts_DedupsStaticAndDynamic(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	store := memory.NewInMemoryChainPollerPersistence()
	registry := contractRegistry.NewInMemoryContractRegistry()
	poller := createTestPollerWithContracts(mockClient, store, nil, []string{"0xAAA"}, 0, WithContractRegistry(registry))

	registry.RegisterContracts([]string{"0xaaa"})

	got := poller.listAllInterestingContracts()
	assert.Len(t, got, 1)
	assert.Equal(t, "0xaaa", got[0])
}

func TestListAllInterestingContracts_NoRegistry_OnlyStatic(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	store := memory.NewInMemoryChainPollerPersistence()
	poller := createTestPollerWithContracts(mockClient, store, nil, []string{"0xSTATIC"}, 0)

	got := poller.listAllInterestingContracts()
	assert.Equal(t, []string{"0xstatic"}, got)
	assert.Nil(t, poller.ContractRegistry())
}

func TestConcurrentRegisterDuringPolling(t *testing.T) {
	registry := contractRegistry.NewInMemoryContractRegistry()
	poller := createTestPoller(nil, memory.NewInMemoryChainPollerPersistence(), nil, WithContractRegistry(registry))

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			registry.RegisterContracts([]string{fmt.Sprintf("0x%d", idx)})
		}(i)
		go func() {
			defer wg.Done()
			_ = poller.listAllInterestingContracts()
		}()
	}
	wg.Wait()

	got := registry.ListContracts()
	assert.Len(t, got, 10)
}

func TestChunkAddresses(t *testing.T) {
	t.Run("exact split", func(t *testing.T) {
		addrs := []string{"a", "b", "c", "d"}
		chunks := chunkAddresses(addrs, 2)
		assert.Len(t, chunks, 2)
		assert.Equal(t, []string{"a", "b"}, chunks[0])
		assert.Equal(t, []string{"c", "d"}, chunks[1])
	})
	t.Run("remainder", func(t *testing.T) {
		addrs := []string{"a", "b", "c", "d", "e"}
		chunks := chunkAddresses(addrs, 2)
		assert.Len(t, chunks, 3)
		assert.Equal(t, []string{"e"}, chunks[2])
	})
	t.Run("single chunk", func(t *testing.T) {
		addrs := []string{"a", "b"}
		chunks := chunkAddresses(addrs, 1000)
		assert.Len(t, chunks, 1)
		assert.Equal(t, []string{"a", "b"}, chunks[0])
	})
	t.Run("empty", func(t *testing.T) {
		chunks := chunkAddresses([]string{}, 100)
		assert.Len(t, chunks, 0)
	})
}

func TestFetchLogs_SingleBatch_UnderLimit(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	store := memory.NewInMemoryChainPollerPersistence()
	poller := createTestPollerWithContracts(mockClient, store, nil, []string{"0xA", "0xB", "0xC"}, 1000)

	expectedLogs := []*ethereum.EthereumEventLog{
		{Address: ethereum.EthereumHexString("0xa"), LogIndex: ethereum.EthereumQuantity(0)},
		{Address: ethereum.EthereumHexString("0xb"), LogIndex: ethereum.EthereumQuantity(1)},
	}
	mockClient.EXPECT().GetLogsBatch(mock.Anything, mock.MatchedBy(func(addrs []string) bool {
		return len(addrs) == 3
	}), uint64(100), uint64(100)).Return(expectedLogs, nil)

	logs, err := poller.fetchLogsForInterestingContractsForBlock(100)
	require.NoError(t, err)
	assert.Len(t, logs, 2)
}

func TestFetchLogs_BatchesByMaxAddresses(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	store := memory.NewInMemoryChainPollerPersistence()
	poller := createTestPollerWithContracts(mockClient, store, nil,
		[]string{"0xA", "0xB", "0xC", "0xD", "0xE"}, 2)

	var callCount atomic.Int32
	mockClient.EXPECT().GetLogsBatch(mock.Anything, mock.Anything, uint64(50), uint64(50)).
		RunAndReturn(func(_ context.Context, addrs []string, _, _ uint64) ([]*ethereum.EthereumEventLog, error) {
			callCount.Add(1)
			logs := make([]*ethereum.EthereumEventLog, 0)
			for i, addr := range addrs {
				logs = append(logs, &ethereum.EthereumEventLog{
					Address:  ethereum.EthereumHexString(addr),
					LogIndex: ethereum.EthereumQuantity(i),
				})
			}
			return logs, nil
		}).Times(3)

	logs, err := poller.fetchLogsForInterestingContractsForBlock(50)
	require.NoError(t, err)
	assert.Equal(t, int32(3), callCount.Load())
	assert.Len(t, logs, 5)
}

func TestFetchLogs_BatchError_FailsFast(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	store := memory.NewInMemoryChainPollerPersistence()
	poller := createTestPollerWithContracts(mockClient, store, nil, []string{"0xA", "0xB"}, 1)

	mockClient.EXPECT().GetLogsBatch(mock.Anything, mock.Anything, uint64(10), uint64(10)).
		Return(nil, fmt.Errorf("rpc error")).Maybe()
	mockClient.EXPECT().GetLogsBatch(mock.Anything, mock.Anything, uint64(10), uint64(10)).
		Return([]*ethereum.EthereumEventLog{}, nil).Maybe()

	_, err := poller.fetchLogsForInterestingContractsForBlock(10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch logs")
}

func TestFetchLogs_EmptyContracts_NoRPCCall(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	store := memory.NewInMemoryChainPollerPersistence()
	poller := createTestPollerWithContracts(mockClient, store, nil, []string{}, 1000)

	logs, err := poller.fetchLogsForInterestingContractsForBlock(100)
	require.NoError(t, err)
	assert.Empty(t, logs)
}

func TestNewEVMChainPoller_DefaultConfig(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()

	poller := NewEVMChainPoller(mockClient, nil, &EVMChainPollerConfig{
		ChainId: config.ChainId(1),
	}, nil, store, mockBlockHandler, zap.NewNop())

	assert.Equal(t, DefaultMaxAddressesPerLogsRequest, poller.config.MaxAddressesPerLogsRequest)
	assert.Equal(t, DefaultLogsFetchTimeout, poller.config.LogsFetchTimeout)
	assert.Nil(t, poller.ContractRegistry())
}

func TestNewEVMChainPoller_WithContractRegistry(t *testing.T) {
	mockClient := mocks.NewMockClient(t)
	mockBlockHandler := mocks.NewMockIBlockHandler(t)
	store := memory.NewInMemoryChainPollerPersistence()
	registry := contractRegistry.NewInMemoryContractRegistry()

	poller := NewEVMChainPoller(mockClient, nil, &EVMChainPollerConfig{
		ChainId: config.ChainId(1),
	}, nil, store, mockBlockHandler, zap.NewNop(), WithContractRegistry(registry))

	assert.NotNil(t, poller.ContractRegistry())
	registry.RegisterContracts([]string{"0xABC"})
	assert.Equal(t, []string{"0xabc"}, poller.ContractRegistry().ListContracts())
}
